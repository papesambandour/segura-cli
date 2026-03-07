package webproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestGuacScreenshot connects, types a command, and saves a screenshot PNG
func TestGuacScreenshot(t *testing.T) {
	cfg := testConfig(t)
	client, _ := NewClient(cfg)
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	dashHTML, _ := client.GetPage("/flow/coge/desktop/dashboard")
	creds := ParseCredentials(dashHTML)
	if len(creds) == 0 {
		t.Fatal("No credentials")
	}
	t.Logf("Credential: %s@%s (%s)", creds[0].Username, creds[0].IP, creds[0].Device)

	proxyURL, err := client.GetProxyURL(creds[0].SRToken)
	if err != nil {
		t.Fatalf("GetProxyURL: %v", err)
	}

	parts := strings.Split(proxyURL, "/client/")
	fullPath := strings.TrimRight(parts[1], "/,")
	tokenParts := strings.SplitN(fullPath, "/", 2)
	tenant := tokenParts[0]
	oasToken := strings.TrimRight(tokenParts[1], ",")

	resp, _ := client.http.PostForm(client.baseURL+"/proxy/api/tokens", url.Values{"token": {oasToken}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var tr map[string]interface{}
	json.Unmarshal(body, &tr)
	authToken := tr["authToken"].(string)

	wsURL := fmt.Sprintf("wss://%s/proxy/websocket-tunnel?ssotoken=%s&tenant=%s&token=%s&GUAC_DATA_SOURCE=default&GUAC_ID=%s&GUAC_TYPE=c&GUAC_WIDTH=800&GUAC_HEIGHT=600&GUAC_DPI=96&GUAC_TIMEZONE=Africa%%2FDakar",
		cfg.Host,
		url.QueryEscape(oasToken),
		url.QueryEscape(tenant),
		url.QueryEscape(authToken),
		url.QueryEscape(oasToken))

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		Jar:             client.http.Jar,
		Subprotocols:    []string{"guacamole"},
	}
	header := http.Header{}
	header.Set("Origin", client.baseURL)

	ws, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("WebSocket: %v", err)
	}
	defer ws.Close()
	t.Log("Connected!")

	// Framebuffer
	fb := image.NewRGBA(image.Rect(0, 0, 800, 600))
	var mu sync.Mutex
	imgCount := 0

	// Stream state tracking
	type sstate struct {
		isImg  bool
		data   bytes.Buffer
		x, y   int
	}
	streams := make(map[string]*sstate)

	done := make(chan struct{})

	// Reader goroutine
	go func() {
		defer close(done)
		for {
			ws.SetReadDeadline(time.Now().Add(30 * time.Second))
			_, msg, err := ws.ReadMessage()
			if err != nil {
				t.Logf("Reader done: %v", err)
				return
			}

			for _, inst := range strings.Split(string(msg), ";") {
				inst = strings.TrimSpace(inst)
				if inst == "" {
					continue
				}

				op, args := parseGuacInstruction(inst)
				switch op {
				case "sync":
					if len(args) > 0 {
						ts := args[0]
						mu.Lock()
						ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("4.sync,%d.%s;", len(ts), ts)))
						mu.Unlock()
					}
				case "img":
					if len(args) >= 6 {
						streamIdx := args[2]
						x, _ := strconv.Atoi(args[4])
						y, _ := strconv.Atoi(args[5])
						ss := streams[streamIdx]
						if ss == nil {
							ss = &sstate{}
							streams[streamIdx] = ss
						}
						ss.isImg = true
						ss.x = x
						ss.y = y
						ss.data.Reset()
					}
				case "argv":
					if len(args) >= 1 {
						ss := streams[args[0]]
						if ss == nil {
							ss = &sstate{}
							streams[args[0]] = ss
						}
						ss.isImg = false
						ss.data.Reset()
					}
				case "blob":
					if len(args) >= 2 {
						ss := streams[args[0]]
						if ss == nil {
							ss = &sstate{}
							streams[args[0]] = ss
						}
						ss.data.WriteString(args[1])
					}
				case "end":
					if len(args) >= 1 {
						ss := streams[args[0]]
						if ss != nil && ss.isImg {
							data, err := base64.StdEncoding.DecodeString(ss.data.String())
							if err == nil {
								img, err := png.Decode(bytes.NewReader(data))
								if err == nil {
									mu.Lock()
									bounds := img.Bounds()
									destRect := image.Rect(ss.x, ss.y, ss.x+bounds.Dx(), ss.y+bounds.Dy())
									draw.Draw(fb, destRect, img, bounds.Min, draw.Over)
									imgCount++
									mu.Unlock()
								}
							}
						}
						if ss != nil {
							ss.isImg = false
							ss.data.Reset()
						}
					}
				case "name":
					if len(args) > 0 {
						t.Logf("[NAME] %s", args[0])
					}
				case "log":
					if len(args) > 0 {
						decoded, _ := base64.StdEncoding.DecodeString(args[0])
						t.Logf("[LOG] %s", string(decoded))
					}
				case "size":
					if len(args) >= 3 {
						layer, _ := strconv.Atoi(args[0])
						w, _ := strconv.Atoi(args[1])
						h, _ := strconv.Atoi(args[2])
						if layer == 0 && w > 0 && h > 0 {
							mu.Lock()
							fb = image.NewRGBA(image.Rect(0, 0, w, h))
							mu.Unlock()
							t.Logf("[SIZE] %dx%d", w, h)
						}
					}
				}
			}
		}
	}()

	// Wait for initial render
	t.Log("Waiting for initial render (8s)...")
	time.Sleep(8 * time.Second)
	t.Logf("Images composited: %d", imgCount)

	// Save screenshot BEFORE typing
	savePNG(t, fb, "/tmp/segura_before.png")

	// Type "whoami" + Enter
	t.Log("Sending: whoami + Enter")
	mu.Lock()
	for _, ch := range "whoami" {
		ks := fmt.Sprintf("%d", ch)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("3.key,%d.%s,1.1;", len(ks), ks)))
		time.Sleep(30 * time.Millisecond)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("3.key,%d.%s,1.0;", len(ks), ks)))
		time.Sleep(30 * time.Millisecond)
	}
	// Enter
	ws.WriteMessage(websocket.TextMessage, []byte("3.key,5.65293,1.1;"))
	time.Sleep(30 * time.Millisecond)
	ws.WriteMessage(websocket.TextMessage, []byte("3.key,5.65293,1.0;"))
	mu.Unlock()

	// Wait for response render
	t.Log("Waiting for response (5s)...")
	time.Sleep(5 * time.Second)

	// Type "ls /" + Enter
	t.Log("Sending: ls / + Enter")
	mu.Lock()
	for _, ch := range "ls /" {
		ks := fmt.Sprintf("%d", ch)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("3.key,%d.%s,1.1;", len(ks), ks)))
		time.Sleep(30 * time.Millisecond)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("3.key,%d.%s,1.0;", len(ks), ks)))
		time.Sleep(30 * time.Millisecond)
	}
	ws.WriteMessage(websocket.TextMessage, []byte("3.key,5.65293,1.1;"))
	time.Sleep(30 * time.Millisecond)
	ws.WriteMessage(websocket.TextMessage, []byte("3.key,5.65293,1.0;"))
	mu.Unlock()

	// Wait for response render
	t.Log("Waiting for response (5s)...")
	time.Sleep(5 * time.Second)

	t.Logf("Total images composited: %d", imgCount)

	// Save final screenshot
	savePNG(t, fb, "/tmp/segura_terminal.png")
	t.Log("Screenshot saved to /tmp/segura_terminal.png")
}

func savePNG(t *testing.T, img *image.RGBA, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Logf("Cannot create %s: %v", path, err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Logf("PNG encode error: %v", err)
		return
	}
	t.Logf("Saved %s", path)
}

func safeGet(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return "?"
}
