package webproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"

	"segura-cli/internal/config"
)

// streamState tracks what type of data a stream is currently expecting.
type streamState int

const (
	streamIdle streamState = iota
	streamImage
	streamArgv
)

// streamInfo tracks the state and accumulated data for a Guacamole stream.
type streamInfo struct {
	state streamState
	data  bytes.Buffer
	// Image-specific state
	imgX, imgY int
}

// GuacClient manages a Guacamole WebSocket terminal session.
type GuacClient struct {
	ws          *websocket.Conn
	mu          sync.Mutex
	framebuffer *image.RGBA
	width       int
	height      int
	displayMode string // "iterm2", "kitty", "none"
	done        chan struct{}
	streams     map[string]*streamInfo
}

// NewGuacClient creates a new Guacamole terminal client.
func NewGuacClient(width, height int) *GuacClient {
	return &GuacClient{
		width:       width,
		height:      height,
		framebuffer: image.NewRGBA(image.Rect(0, 0, width, height)),
		displayMode: detectDisplayMode(),
		done:        make(chan struct{}),
		streams:     make(map[string]*streamInfo),
	}
}

// detectDisplayMode checks what image display protocol the terminal supports.
func detectDisplayMode() string {
	tp := os.Getenv("TERM_PROGRAM")
	if tp == "iTerm.app" {
		return "iterm2"
	}
	lc := os.Getenv("LC_TERMINAL")
	if lc == "iTerm2" {
		return "iterm2"
	}
	te := os.Getenv("TERM")
	if strings.Contains(te, "kitty") {
		return "kitty"
	}
	if tp == "WezTerm" {
		return "kitty"
	}
	// Default to iterm2 on macOS (widely supported)
	return "iterm2"
}

// ConnectAndRun establishes the Guacamole connection and runs the terminal.
func ConnectAndRun(cfg *config.Config, credential, device string) error {
	fmt.Println("Authenticating to senhasegura...")

	client, err := NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Login(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	fmt.Println("Authentication successful.")

	credentials, err := client.FetchAllCredentials()
	if err != nil {
		return err
	}
	if len(credentials) == 0 {
		return fmt.Errorf("no credentials found")
	}

	cred := FindCredential(credentials, credential, device)
	if cred == nil {
		fmt.Println("\nAvailable credentials:")
		for _, c := range credentials {
			fmt.Printf("  %s@%s", c.Username, c.IP)
			if c.Device != "" {
				fmt.Printf(" (%s)", c.Device)
			}
			fmt.Println()
		}
		return fmt.Errorf("credential %s@%s not found", credential, device)
	}

	fmt.Printf("Connecting to %s@%s...\n", credential, device)

	proxyURL, err := client.GetProxyURL(cred.SRToken)
	if err != nil {
		return fmt.Errorf("failed to get proxy URL: %w", err)
	}

	session, err := client.ExchangeTokens(proxyURL)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Calculate terminal pixel dimensions
	guacWidth := 1024
	guacHeight := 768
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		cols, rows, err := term.GetSize(fd)
		if err == nil && cols > 0 && rows > 0 {
			guacWidth = cols * 8
			guacHeight = rows * 18
		}
	}

	// Connect WebSocket
	wsURL := fmt.Sprintf("wss://%s/proxy/websocket-tunnel?ssotoken=%s&tenant=%s&token=%s&GUAC_DATA_SOURCE=default&GUAC_ID=%s&GUAC_TYPE=c&GUAC_WIDTH=%d&GUAC_HEIGHT=%d&GUAC_DPI=96&GUAC_TIMEZONE=Africa%%2FDakar",
		session.Host,
		url.QueryEscape(session.OASToken),
		url.QueryEscape(session.Tenant),
		url.QueryEscape(session.AuthToken),
		url.QueryEscape(session.OASToken),
		guacWidth, guacHeight)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		Jar:             session.Jar,
		Subprotocols:    []string{"guacamole"},
	}
	header := http.Header{}
	header.Set("Origin", session.BaseURL)

	ws, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("WebSocket connection failed: %w", err)
	}

	gc := NewGuacClient(guacWidth, guacHeight)
	gc.ws = ws

	fmt.Printf("Connected to %s@%s (%s)\n", credential, device, cred.Device)
	fmt.Println("Press Ctrl+] to disconnect.")
	fmt.Println()

	return gc.run()
}

// run starts the interactive terminal session.
func (gc *GuacClient) run() error {
	defer gc.ws.Close()

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	// Clear screen and hide cursor
	os.Stdout.WriteString("\033[2J\033[H\033[?25l")
	defer os.Stdout.WriteString("\033[?25h") // Show cursor on exit

	// Handle window resize
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			if cols, rows, err := term.GetSize(fd); err == nil && cols > 0 && rows > 0 {
				newW := cols * 8
				newH := rows * 18
				gc.sendGuacSize(newW, newH)
			}
			gc.displayFrame()
		}
	}()
	defer signal.Stop(sigwinch)

	// Start WebSocket reader
	go gc.readLoop()

	// Start keyboard input reader
	gc.inputLoop(fd)

	// Restore terminal
	os.Stdout.WriteString("\033[?25h\033[2J\033[H")
	return nil
}

// readLoop reads and processes Guacamole protocol messages from WebSocket.
func (gc *GuacClient) readLoop() {
	defer func() {
		select {
		case <-gc.done:
		default:
			close(gc.done)
		}
	}()

	dirty := false
	lastDisplay := time.Time{}
	minInterval := 50 * time.Millisecond // ~20 FPS max

	for {
		gc.ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, msg, err := gc.ws.ReadMessage()
		if err != nil {
			if dirty {
				gc.displayFrame()
			}
			return
		}

		for _, inst := range strings.Split(string(msg), ";") {
			inst = strings.TrimSpace(inst)
			if inst == "" {
				continue
			}
			if gc.processInstruction(inst) {
				dirty = true
			}
		}

		if dirty && time.Since(lastDisplay) >= minInterval {
			gc.displayFrame()
			dirty = false
			lastDisplay = time.Now()
		}
	}
}

// processInstruction handles a single Guacamole protocol instruction.
// Returns true if the framebuffer was updated.
func (gc *GuacClient) processInstruction(inst string) bool {
	opcode, args := parseGuacInstruction(inst)

	switch opcode {
	case "sync":
		if len(args) > 0 {
			ts := args[0]
			syncResp := fmt.Sprintf("4.sync,%d.%s;", len(ts), ts)
			gc.mu.Lock()
			gc.ws.WriteMessage(websocket.TextMessage, []byte(syncResp))
			gc.mu.Unlock()
		}
		return true // frame boundary

	case "img":
		// img: layer, mode, stream, mimetype, x, y
		if len(args) >= 6 {
			streamIdx := args[2]
			x, _ := strconv.Atoi(args[4])
			y, _ := strconv.Atoi(args[5])

			si := gc.getStream(streamIdx)
			si.state = streamImage
			si.imgX = x
			si.imgY = y
			si.data.Reset()
		}

	case "argv":
		// argv: stream, mimetype, name
		if len(args) >= 1 {
			si := gc.getStream(args[0])
			si.state = streamArgv
			si.data.Reset()
		}

	case "blob":
		// blob: stream, data (base64)
		if len(args) >= 2 {
			si := gc.getStream(args[0])
			si.data.WriteString(args[1])
		}

	case "end":
		// end: stream — composite image but don't trigger display (wait for sync)
		if len(args) >= 1 {
			streamIdx := args[0]
			si, ok := gc.streams[streamIdx]
			if ok && si.state == streamImage {
				gc.compositeImage(si)
				si.state = streamIdle
				si.data.Reset()
			}
			if ok {
				si.state = streamIdle
				si.data.Reset()
			}
		}

	case "name":
		if len(args) > 0 {
			os.Stdout.WriteString(fmt.Sprintf("\033]0;%s\007", args[0]))
		}

	case "size":
		if len(args) >= 3 {
			layer, _ := strconv.Atoi(args[0])
			w, _ := strconv.Atoi(args[1])
			h, _ := strconv.Atoi(args[2])
			if layer == 0 && w > 0 && h > 0 && (w != gc.width || h != gc.height) {
				gc.width = w
				gc.height = h
				gc.framebuffer = image.NewRGBA(image.Rect(0, 0, w, h))
			}
		}

	case "disconnect":
		select {
		case <-gc.done:
		default:
			close(gc.done)
		}
	}

	return false
}

// getStream returns or creates a stream info object.
func (gc *GuacClient) getStream(idx string) *streamInfo {
	si, ok := gc.streams[idx]
	if !ok {
		si = &streamInfo{}
		gc.streams[idx] = si
	}
	return si
}

// compositeImage decodes a PNG and composites it onto the framebuffer.
func (gc *GuacClient) compositeImage(si *streamInfo) {
	data, err := base64.StdEncoding.DecodeString(si.data.String())
	if err != nil {
		return
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}

	bounds := img.Bounds()
	destRect := image.Rect(si.imgX, si.imgY, si.imgX+bounds.Dx(), si.imgY+bounds.Dy())
	draw.Draw(gc.framebuffer, destRect, img, bounds.Min, draw.Over)
}

// displayFrame renders the current framebuffer to the terminal.
func (gc *GuacClient) displayFrame() {
	switch gc.displayMode {
	case "iterm2":
		gc.displayITerm2()
	case "kitty":
		gc.displayKitty()
	}
}

// displayITerm2 renders the framebuffer using iTerm2 inline image protocol.
func (gc *GuacClient) displayITerm2() {
	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, gc.framebuffer); err != nil {
		return
	}

	b64 := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	// Build the entire escape sequence in a single buffer for flicker-free output
	var out bytes.Buffer
	out.WriteString("\033[H\033[?25l") // Move to top-left, ensure cursor hidden
	fmt.Fprintf(&out, "\033]1337;File=inline=1;width=auto;height=auto;preserveAspectRatio=1:%s\a", b64)
	os.Stdout.Write(out.Bytes())
}

// displayKitty renders using Kitty graphics protocol.
func (gc *GuacClient) displayKitty() {
	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, gc.framebuffer); err != nil {
		return
	}

	b64 := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	var out bytes.Buffer
	out.WriteString("\033[H\033[?25l")
	first := true
	for len(b64) > 0 {
		chunk := b64
		more := 0
		if len(b64) > 4096 {
			chunk = b64[:4096]
			b64 = b64[4096:]
			more = 1
		} else {
			b64 = ""
		}
		if first {
			fmt.Fprintf(&out, "\033_Gf=100,a=T,t=d,q=2,m=%d;%s\033\\", more, chunk)
			first = false
		} else {
			fmt.Fprintf(&out, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}
	os.Stdout.Write(out.Bytes())
}

// inputLoop reads keyboard input and sends Guacamole key events.
func (gc *GuacClient) inputLoop(fd int) {
	buf := make([]byte, 256)
	for {
		select {
		case <-gc.done:
			return
		default:
		}

		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			select {
			case <-gc.done:
				return
			default:
				continue
			}
		}

		data := buf[:n]

		// Check for Ctrl+] (0x1D) to disconnect
		for _, b := range data {
			if b == 0x1D {
				select {
				case <-gc.done:
				default:
					close(gc.done)
				}
				return
			}
		}

		gc.sendKeys(data)
	}
}

// sendKeys converts terminal input bytes to Guacamole key events.
func (gc *GuacClient) sendKeys(data []byte) {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	for i := 0; i < len(data); i++ {
		b := data[i]

		var keysym int
		switch {
		case b == 0x0D || b == 0x0A: // Enter
			keysym = 0xFF0D
		case b == 0x08 || b == 0x7F: // Backspace
			keysym = 0xFF08
		case b == 0x09: // Tab
			keysym = 0xFF09
		case b == 0x1B: // Escape or escape sequence
			if i+2 < len(data) && data[i+1] == '[' {
				switch data[i+2] {
				case 'A':
					keysym = 0xFF52 // Up
					i += 2
				case 'B':
					keysym = 0xFF54 // Down
					i += 2
				case 'C':
					keysym = 0xFF53 // Right
					i += 2
				case 'D':
					keysym = 0xFF51 // Left
					i += 2
				case '3':
					if i+3 < len(data) && data[i+3] == '~' {
						keysym = 0xFFFF // Delete
						i += 3
					} else {
						keysym = 0xFF1B
					}
				case 'H':
					keysym = 0xFF50 // Home
					i += 2
				case 'F':
					keysym = 0xFF57 // End
					i += 2
				case '5':
					if i+3 < len(data) && data[i+3] == '~' {
						keysym = 0xFF55 // Page Up
						i += 3
					}
				case '6':
					if i+3 < len(data) && data[i+3] == '~' {
						keysym = 0xFF56 // Page Down
						i += 3
					}
				default:
					keysym = 0xFF1B
				}
			} else if i+2 < len(data) && data[i+1] == 'O' {
				switch data[i+2] {
				case 'P':
					keysym = 0xFFBE // F1
				case 'Q':
					keysym = 0xFFBF // F2
				case 'R':
					keysym = 0xFFC0 // F3
				case 'S':
					keysym = 0xFFC1 // F4
				}
				i += 2
			} else {
				keysym = 0xFF1B // Escape
			}
		case b >= 0x01 && b <= 0x1A: // Ctrl+A through Ctrl+Z
			letter := int('a') + int(b) - 1
			gc.sendKeyEvent(0xFFE3, true)  // Ctrl down
			gc.sendKeyEvent(letter, true)
			gc.sendKeyEvent(letter, false)
			gc.sendKeyEvent(0xFFE3, false) // Ctrl up
			continue
		default:
			keysym = int(b)
		}

		if keysym > 0 {
			gc.sendKeyEvent(keysym, true)
			gc.sendKeyEvent(keysym, false)
		}
	}
}

// sendKeyEvent sends a single Guacamole key event.
func (gc *GuacClient) sendKeyEvent(keysym int, pressed bool) {
	ks := strconv.Itoa(keysym)
	p := "0"
	if pressed {
		p = "1"
	}
	msg := fmt.Sprintf("3.key,%d.%s,1.%s;", len(ks), ks, p)
	gc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
}

// sendGuacSize sends a Guacamole display size instruction to the server.
func (gc *GuacClient) sendGuacSize(width, height int) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	w := strconv.Itoa(width)
	h := strconv.Itoa(height)
	msg := fmt.Sprintf("4.size,%d.%s,%d.%s;", len(w), w, len(h), h)
	gc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
}

// parseGuacInstruction parses a Guacamole instruction into opcode and args.
func parseGuacInstruction(inst string) (string, []string) {
	dotIdx := strings.Index(inst, ".")
	if dotIdx < 0 {
		return "", nil
	}
	rest := inst[dotIdx+1:]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return rest, nil
	}
	opcode := rest[:commaIdx]

	var args []string
	s := rest
	for s != "" {
		ci := strings.Index(s, ",")
		if ci < 0 {
			break
		}
		s = s[ci+1:]
		di := strings.Index(s, ".")
		if di < 0 {
			break
		}
		lenStr := s[:di]
		argLen, _ := strconv.Atoi(lenStr)
		s = s[di+1:]
		if argLen > len(s) {
			argLen = len(s)
		}
		args = append(args, s[:argLen])
		s = s[argLen:]
	}
	return opcode, args
}
