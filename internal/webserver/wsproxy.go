package webserver

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"segura-cli/internal/webproxy"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	Subprotocols: []string{"guacamole"},
}

// msgboxMarker is a cheap pre-filter so the msgbox parser only runs on frames
// that could contain one (they are rare — errors only), keeping the hot relay path fast.
var msgboxMarker = []byte("msgbox")

// handleWS proxies a WebSocket connection between the browser and senhasegura Guacamole.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	ip := r.URL.Query().Get("ip")
	width := r.URL.Query().Get("width")
	height := r.URL.Query().Get("height")

	if username == "" || ip == "" {
		http.Error(w, `{"error":"username and ip required"}`, http.StatusBadRequest)
		return
	}
	if width == "" {
		width = "1024"
	}
	if height == "" {
		height = "768"
	}

	// Upgrade browser connection FIRST so the WebSocket handshake completes immediately.
	// Auth + upstream connection happen after the upgrade.
	browserConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}
	defer browserConn.Close()

	dbg := newWSDebugger()
	defer dbg.close()
	dbg.log("info", []byte(fmt.Sprintf("WS upgraded for %s@%s, authenticating...", username, ip)))

	log.Printf("WS upgraded, authenticating for %s@%s...", username, ip)

	// Keep the browser's Guacamole tunnel alive during authentication. The client
	// tunnel (guacamole-common-js) has a 15s receiveTimeout: if no data arrives
	// within that window it fires UPSTREAM_TIMEOUT and closes the socket. senhasegura
	// auth can take longer than 15s (observed 21s for some credentials), which made
	// the terminal spin forever. Periodic Guacamole "nop" instructions reset the
	// client's timer without side effects. This is the ONLY writer to browserConn
	// until it stops, and we wait for it to fully finish before the relay starts
	// (gorilla/websocket forbids concurrent writers on one connection).
	keepaliveStop := make(chan struct{})
	keepaliveDone := make(chan struct{})
	go func() {
		defer close(keepaliveDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-keepaliveStop:
				return
			case <-ticker.C:
				if err := browserConn.WriteMessage(websocket.TextMessage, []byte("3.nop;")); err != nil {
					return
				}
				dbg.log("keepalive->browser", []byte("3.nop;"))
			}
		}
	}()

	// Authenticate and get credentials
	upstream, err := s.connectUpstream(username, ip, width, height)

	// Stop the keepalive and wait for the goroutine to exit before writing anything
	// else to browserConn (avoids concurrent writers).
	close(keepaliveStop)
	<-keepaliveDone

	if err != nil {
		log.Printf("WS upstream error: %v", err)
		dbg.log("error", []byte("connectUpstream failed: "+err.Error()))
		// Send Guacamole-protocol error so guacamole-common-js shows it
		errMsg := fmt.Sprintf("5.error,%d.%s,3.519;", len(err.Error()), err.Error())
		browserConn.WriteMessage(websocket.TextMessage, []byte(errMsg))
		browserConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
		return
	}
	defer upstream.Close()

	log.Printf("WS proxy started: %s@%s", username, ip)
	dbg.log("info", []byte("upstream connected; relay started"))

	// Bidirectional relay
	done := make(chan struct{})
	var once sync.Once
	cleanup := func() {
		log.Printf("WS proxy ended: %s@%s", username, ip)
	}

	// Browser → Upstream (this direction carries the browser's key/mouse events)
	go func() {
		defer once.Do(cleanup)
		defer close(done)
		for {
			msgType, msg, err := browserConn.ReadMessage()
			if err != nil {
				log.Printf("WS browser read error: %v", err)
				return
			}
			dbg.log("browser->upstream", msg)
			if err := upstream.WriteMessage(msgType, msg); err != nil {
				log.Printf("WS upstream write error: %v", err)
				return
			}
		}
	}()

	// Upstream → Browser (blocks the handler goroutine)
	func() {
		defer once.Do(cleanup)
		for {
			msgType, msg, err := upstream.ReadMessage()
			if err != nil {
				log.Printf("WS upstream read error: %v", err)
				return
			}
			dbg.log("upstream->browser", msg)
			if err := browserConn.WriteMessage(msgType, msg); err != nil {
				log.Printf("WS browser write error: %v", err)
				return
			}
			// senhasegura signals fatal connection problems (e.g. server-side
			// credential decryption failures) with a non-standard `msgbox` Error
			// instruction that guacamole-common-js ignores — leaving the terminal
			// spinning forever. Translate it into a standard Guacamole `error` so the
			// client's onerror handler shows the real reason and stops waiting. This
			// goroutine is the only writer to browserConn, so the extra write is safe.
			if bytes.Contains(msg, msgboxMarker) {
				if sev, m, ok := parseGuacMsgbox(msg); ok && strings.EqualFold(sev, "error") {
					log.Printf("WS upstream msgbox error for %s@%s: %s", username, ip, m)
					dbg.log("msgbox->error", []byte(m))
					errInstr := fmt.Sprintf("5.error,%d.%s,3.512;", len(m), m)
					browserConn.WriteMessage(websocket.TextMessage, []byte(errInstr))
				}
			}
		}
	}()

	<-done
}

// parseGuacMsgbox scans a Guacamole frame for a `msgbox` instruction and returns
// its severity and message. Guacamole elements are LENGTH.VALUE, comma-separated,
// each instruction terminated by ';' (e.g. "6.msgbox,5.Error,20.some message;").
func parseGuacMsgbox(frame []byte) (severity, message string, found bool) {
	s := string(frame)
	i := 0
	for i < len(s) {
		var elems []string
		for i < len(s) {
			dot := strings.IndexByte(s[i:], '.')
			if dot < 0 {
				return "", "", false
			}
			n, err := strconv.Atoi(s[i : i+dot])
			if err != nil || n < 0 {
				return "", "", false
			}
			start := i + dot + 1
			if start+n > len(s) {
				return "", "", false
			}
			elems = append(elems, s[start:start+n])
			i = start + n
			if i >= len(s) {
				break
			}
			sep := s[i]
			i++ // consume ',' or ';'
			if sep == ';' {
				break
			}
		}
		if len(elems) > 0 && elems[0] == "msgbox" {
			if len(elems) >= 3 {
				return elems[1], elems[2], true
			}
			if len(elems) == 2 {
				return "", elems[1], true
			}
			return "", "", true
		}
	}
	return "", "", false
}

// wsDebugger dumps the Guacamole-protocol frames of a web-terminal session to
// $HOME/.segura/ws-debug.log when SEGURA_DEBUG_WS is set. It is a diagnostic aid
// for the web "0 repeats / can't type" bug: the browser->upstream direction shows
// whether the frontend is emitting runaway "key" instructions.
type wsDebugger struct {
	mu    sync.Mutex
	f     *os.File
	start time.Time
}

func newWSDebugger() *wsDebugger {
	if os.Getenv("SEGURA_DEBUG_WS") == "" {
		return nil
	}
	dir := filepath.Join(os.Getenv("HOME"), ".segura")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "ws-debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		log.Printf("SEGURA_DEBUG_WS: cannot open %s: %v", path, err)
		return nil
	}
	log.Printf("SEGURA_DEBUG_WS: logging web-terminal frames to %s", path)
	return &wsDebugger{f: f, start: time.Now()}
}

func (d *wsDebugger) log(dir string, msg []byte) {
	if d == nil {
		return
	}
	s := string(msg)
	if len(s) > 400 { // drawing ops can be huge; the key/mouse frames we care about are short
		s = s[:400] + "…"
	}
	d.mu.Lock()
	fmt.Fprintf(d.f, "%9.3f %-17s %s\n", time.Since(d.start).Seconds(), dir, s)
	d.mu.Unlock()
}

func (d *wsDebugger) close() {
	if d != nil && d.f != nil {
		d.f.Close()
	}
}

// connectUpstream authenticates and opens a WebSocket to the upstream Guacamole server.
func (s *Server) connectUpstream(username, ip, width, height string) (*websocket.Conn, error) {
	client, err := s.sm.GetClient()
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	credentials, err := client.FetchAllCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch credentials: %w", err)
	}

	cred := webproxy.FindCredential(credentials, username, ip)
	if cred == nil {
		// A cached session that expired server-side returns the login page, which
		// parses to zero credentials → the target looks "not found". Re-authenticate
		// once with a fresh client before giving up.
		log.Printf("WS credential %s@%s not found (%d listed); refreshing session and retrying", username, ip, len(credentials))
		client, err = s.sm.RefreshClient()
		if err != nil {
			return nil, fmt.Errorf("re-authentication failed: %w", err)
		}
		credentials, err = client.FetchAllCredentials()
		if err != nil {
			return nil, fmt.Errorf("failed to fetch credentials: %w", err)
		}
		cred = webproxy.FindCredential(credentials, username, ip)
		if cred == nil {
			return nil, fmt.Errorf("credential %s@%s not found", username, ip)
		}
	}

	proxyURL, err := client.GetProxyURL(cred.SRToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxy URL: %w", err)
	}

	session, err := client.ExchangeTokens(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	upstreamURL := fmt.Sprintf("wss://%s/proxy/websocket-tunnel?ssotoken=%s&tenant=%s&token=%s&GUAC_DATA_SOURCE=default&GUAC_ID=%s&GUAC_TYPE=c&GUAC_WIDTH=%s&GUAC_HEIGHT=%s&GUAC_DPI=96&GUAC_TIMEZONE=Africa%%2FDakar",
		session.Host,
		url.QueryEscape(session.OASToken),
		url.QueryEscape(session.Tenant),
		url.QueryEscape(session.AuthToken),
		url.QueryEscape(session.OASToken),
		width, height)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		Jar:             session.Jar,
		Subprotocols:    []string{"guacamole"},
	}
	header := http.Header{}
	header.Set("Origin", session.BaseURL)

	log.Printf("WS connecting upstream for %s@%s...", username, ip)
	upstream, _, err := dialer.Dial(upstreamURL, header)
	if err != nil {
		return nil, fmt.Errorf("upstream connection failed: %w", err)
	}

	return upstream, nil
}
