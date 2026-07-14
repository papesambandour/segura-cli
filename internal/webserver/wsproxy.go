package webserver

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"segura-cli/internal/webproxy"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	Subprotocols: []string{"guacamole"},
}

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

	// Authenticate and get credentials
	upstream, err := s.connectUpstream(username, ip, width, height)
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
		}
	}()

	<-done
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
		return nil, fmt.Errorf("credential %s@%s not found", username, ip)
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
