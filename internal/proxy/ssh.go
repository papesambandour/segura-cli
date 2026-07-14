package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

// ttyDebugger logs the raw session byte stream (both directions, with relative
// timing) to $HOME/.segura/tty-debug.log when SEGURA_DEBUG_TTY is set. It's a
// diagnostic aid for terminal-escape issues (e.g. vim key-repeat / bell): the
// "up" direction reveals the terminal's replies to vim's capability queries and
// whether they arrive late enough for vim to misread them as keystrokes.
type ttyDebugger struct {
	mu    sync.Mutex
	f     *os.File
	start time.Time
}

func newTTYDebugger() *ttyDebugger {
	if os.Getenv("SEGURA_DEBUG_TTY") == "" {
		return nil
	}
	dir := filepath.Join(os.Getenv("HOME"), ".segura")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "tty-debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SEGURA_DEBUG_TTY: cannot open %s: %v\n", path, err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "SEGURA_DEBUG_TTY: logging raw TTY stream to %s\n", path)
	return &ttyDebugger{f: f, start: time.Now()}
}

// log records a chunk. dir is "down" (remote->local) or "up" (local->remote).
func (d *ttyDebugger) log(dir string, b []byte) {
	if d == nil {
		return
	}
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c == 0x1b:
			sb.WriteString("\\e")
		case c == '\n':
			sb.WriteString("\\n")
		case c == '\r':
			sb.WriteString("\\r")
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "\\x%02x", c)
		}
	}
	d.mu.Lock()
	fmt.Fprintf(d.f, "%8.3f %-4s %s\n", time.Since(d.start).Seconds(), dir, sb.String())
	d.mu.Unlock()
}

func (d *ttyDebugger) close() {
	if d != nil && d.f != nil {
		d.f.Close()
	}
}

// daSequenceFilter strips CSI private Device Attributes sequences —
// ESC '[' '?' <params> 'c' (e.g. ESC [ ? 6 c) — from a byte stream. On the way
// to the terminal these are stray sequences a well-behaved terminal (iTerm2)
// simply ignores; dropping them makes any terminal behave that way, so none is
// ever provoked into the DA feedback loop that floods the session. It only ever
// matches sequences ending in 'c', so mode toggles like ESC[?25h/l (cursor) and
// ESC[?2004h/l (bracketed paste) pass through untouched. The filter is a small
// state machine so it works even when a sequence is split across separate reads.
type daSequenceFilter struct {
	// held buffers a sequence that starts with ESC and still looks like it could
	// become a DA reply. It is either dropped (on match) or flushed (on divergence
	// or when it grows past any plausible DA reply).
	held []byte
}

// maxDASeq caps how long a candidate DA reply may grow before we give up and
// flush it as ordinary bytes (a real reply like ESC[?6c or ESC[?1;2c is short).
const maxDASeq = 16

func (f *daSequenceFilter) strip(in []byte) []byte {
	out := make([]byte, 0, len(f.held)+len(in))

	for _, b := range in {
		if f.held == nil {
			if b == 0x1b { // ESC — begin a candidate sequence
				f.held = append(f.held, b)
			} else {
				out = append(out, b)
			}
			continue
		}

		f.held = append(f.held, b)
		if drop, done, flush := f.classify(); done {
			f.held = nil
			if drop {
				continue
			}
			// If the divergence byte is itself an ESC, it may begin a new DA
			// reply — emit everything before it and re-hold the trailing ESC.
			if n := len(flush); n > 0 && flush[n-1] == 0x1b {
				out = append(out, flush[:n-1]...)
				f.held = append(f.held, 0x1b)
			} else {
				out = append(out, flush...)
			}
		}
	}
	return out
}

// classify inspects f.held. done reports whether the candidate has resolved;
// drop reports it was a complete DA reply (discard); flush is the bytes to emit
// verbatim when it was not a DA reply. While still ambiguous, done is false and
// the bytes stay held for the next byte.
func (f *daSequenceFilter) classify() (drop, done bool, flush []byte) {
	h := f.held
	// h[0] is ESC. Validate the expected prefix ESC '[' '?'.
	if len(h) >= 2 && h[1] != '[' {
		return false, true, h // ESC not followed by '[' -> not a DA reply
	}
	if len(h) >= 3 && h[2] != '?' {
		return false, true, h // CSI not private ('?') -> not a DA reply
	}
	if len(h) >= 4 {
		last := h[len(h)-1]
		if last == 'c' { // ESC [ ? <params> c -> complete DA reply
			return true, true, nil
		}
		if last != ';' && (last < '0' || last > '9') {
			return false, true, h // unexpected terminator -> not a DA reply
		}
	}
	if len(h) > maxDASeq {
		return false, true, h // too long to be a DA reply -> flush
	}
	return false, false, nil // still ambiguous; keep holding
}

// BuildSSHUser constructs the senhasegura Terminal Proxy username.
// Format: vaultUser[credential@device]totp%tenant
func BuildSSHUser(cfg *config.Config, credential, device string) (string, error) {
	totp, err := auth.GenerateTOTP(cfg.MFAToken)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s[%s@%s]%s%%%s", cfg.User, credential, device, totp, cfg.Tenant), nil
}

// Connect opens an interactive SSH session through the senhasegura Terminal Proxy.
func Connect(cfg *config.Config, credential, device string, port int) error {
	sshUser, err := BuildSSHUser(cfg, credential, device)
	if err != nil {
		return fmt.Errorf("failed to build SSH user: %w", err)
	}

	sshArgs := []string{
		"-tt",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "PubkeyAuthentication=no",
		"-l", sshUser,
		"-p", fmt.Sprintf("%d", port),
		cfg.Host,
	}

	cmd := exec.Command("ssh", sshArgs...)

	// Start ssh in a PTY for full interactive support
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start ssh: %w", err)
	}
	defer ptmx.Close()

	dbg := newTTYDebugger()
	defer dbg.close()

	// Set local terminal to raw mode (only if stdin is a terminal)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err == nil {
			defer term.Restore(fd, oldState)
		}

		// Sync PTY size with local terminal
		syncSize := func() {
			if w, h, err := term.GetSize(fd); err == nil {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
			}
		}
		syncSize()

		// Handle window resize
		sigwinch := make(chan os.Signal, 1)
		signal.Notify(sigwinch, syscall.SIGWINCH)
		go func() {
			for range sigwinch {
				syncSize()
			}
		}()
		defer signal.Stop(sigwinch)
	}

	// Read SSH output, act as a well-behaved terminal ("embed iTerm2"), detect
	// the password prompt and auto-send the password.
	//
	// The senhasegura proxy shell echoes stray CSI private Device Attributes
	// sequences (ESC [ ? <n> c) onto its prompt. A robust terminal (iTerm2)
	// ignores those; a fragile one instead answers each with its own DA reply,
	// which the proxy echoes again — an infinite feedback loop that floods the
	// session (the "0 repeats / can't type / bell" bug in vim). We stop that at
	// the source: before writing to the real terminal we drop those DA sequences,
	// so the terminal is never provoked into replying. Password detection and the
	// debug log run on the RAW stream; only the on-screen bytes are filtered.
	go func() {
		var shim daSequenceFilter
		buf := make([]byte, 4096)
		var accumulated string
		passwordSent := false

		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				dbg.log("down", buf[:n])
				if out := shim.strip(buf[:n]); len(out) > 0 {
					os.Stdout.Write(out)
				}

				if !passwordSent {
					accumulated += chunk
					lower := strings.ToLower(accumulated)
					if strings.Contains(lower, "password") {
						time.Sleep(100 * time.Millisecond)
						ptmx.Write([]byte(cfg.Password + "\r"))
						passwordSent = true
						accumulated = ""
					}
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Forward user stdin to the PTY verbatim — never filter what the user types.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				dbg.log("up", buf[:n])
				ptmx.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	return cmd.Wait()
}
