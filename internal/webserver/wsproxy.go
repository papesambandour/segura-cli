package webserver

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"

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

	log.Printf("WS upgraded, authenticating for %s@%s...", username, ip)

	// Authenticate and get credentials
	upstream, err := s.connectUpstream(username, ip, width, height)
	if err != nil {
		log.Printf("WS upstream error: %v", err)
		// Send Guacamole-protocol error so guacamole-common-js shows it
		errMsg := fmt.Sprintf("5.error,%d.%s,3.519;", len(err.Error()), err.Error())
		browserConn.WriteMessage(websocket.TextMessage, []byte(errMsg))
		browserConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
		return
	}
	defer upstream.Close()

	log.Printf("WS proxy started: %s@%s", username, ip)

	// Bidirectional relay
	done := make(chan struct{})
	var once sync.Once
	cleanup := func() {
		log.Printf("WS proxy ended: %s@%s", username, ip)
	}

	// Browser → Upstream
	go func() {
		defer once.Do(cleanup)
		defer close(done)
		for {
			msgType, msg, err := browserConn.ReadMessage()
			if err != nil {
				log.Printf("WS browser read error: %v", err)
				return
			}
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
			if err := browserConn.WriteMessage(msgType, msg); err != nil {
				log.Printf("WS browser write error: %v", err)
				return
			}
		}
	}()

	<-done
}

// connectUpstream authenticates and opens a WebSocket to the upstream Guacamole server.
func (s *Server) connectUpstream(username, ip, width, height string) (*websocket.Conn, error) {
	client, err := s.sm.GetClient()
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	dashboardHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dashboard: %w", err)
	}

	credentials := webproxy.ParseCredentials(dashboardHTML)
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
