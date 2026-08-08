package sftpclient

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

// FileInfo represents a remote file entry.
type FileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Mode    string `json:"mode"`
}

// Session holds an SSH+SFTP connection to a specific device.
//
// senhasegura allows only ONE device session per credential, so SFTP and an
// interactive shell are mutually exclusive: the session toggles between them.
// SFTP methods call ensureSFTP(); sudo methods call ensureShell() (which opens an
// interactive device bash where senhasegura auto-injects the sudo password —
// "SUDO Automation in progress" — since SSH exec is blocked by the proxy).
type Session struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	lastUsed   time.Time
	mu         sync.Mutex
	credential string
	device     string
	cfg        *config.Config

	// Interactive PTY shell (device bash) for sudo, on its own connection.
	sudoClient *ssh.Client
	sudoSess   *ssh.Session
	sudoIn     io.WriteCloser
	sudoBuf    *bytes.Buffer
	sudoBufMu  sync.Mutex
}

// sudoRCRe matches the exit-code sentinel we append after each shell command. The
// echoed command line contains literal "$?" (no digits) so it never matches.
var sudoRCRe = regexp.MustCompile(`__SEGRC__(\d+)__`)

// Manager manages SFTP sessions per device connection.
type Manager struct {
	cfg      *config.Config
	sessions map[string]*Session // key: "credential@device"
	mu       sync.RWMutex
}

// NewManager creates a new SFTP session manager.
func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		cfg:      cfg,
		sessions: make(map[string]*Session),
	}
	// Cleanup goroutine for idle sessions
	go m.cleanupLoop()
	return m
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		for key, sess := range m.sessions {
			sess.mu.Lock()
			if time.Since(sess.lastUsed) > 5*time.Minute {
				sess.close()
				delete(m.sessions, key)
			}
			sess.mu.Unlock()
		}
		m.mu.Unlock()
	}
}

// GetSession returns an existing or new SFTP session for the given credential@device.
func (m *Manager) GetSession(credential, device string) (*Session, error) {
	key := credential + "@" + device

	// Try existing session
	m.mu.RLock()
	sess, ok := m.sessions[key]
	m.mu.RUnlock()

	if ok {
		sess.mu.Lock()
		sess.lastUsed = time.Now()
		// Test if connection is still alive
		if sess.sftpClient != nil {
			if _, err := sess.sftpClient.Getwd(); err == nil {
				sess.mu.Unlock()
				return sess, nil
			}
		}
		// Connection dead, close and reconnect
		sess.close()
		sess.mu.Unlock()
	}

	// Create new session
	m.mu.Lock()
	defer m.mu.Unlock()

	newSess, err := m.dial(credential, device)
	if err != nil {
		return nil, err
	}
	m.sessions[key] = newSess
	return newSess, nil
}

func (m *Manager) dial(credential, device string) (*Session, error) {
	sshClient, err := DialClient(m.cfg, credential, device)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("SFTP subsystem failed: %w", err)
	}

	log.Printf("SFTP session established for %s@%s", credential, device)
	return &Session{
		sshClient:  sshClient,
		sftpClient: sftpClient,
		lastUsed:   time.Now(),
		credential: credential,
		device:     device,
		cfg:        m.cfg,
	}, nil
}

// DialClient opens an authenticated SSH connection to the senhasegura gateway for
// credential@device and returns the raw *ssh.Client (the caller must Close it).
// It is the single source of gateway auth — same username formats (%tenant then
// without), TOTP, and kex/cipher set used by SFTP sessions. Callers that need a
// direct-tcpip channel (proxy / port-forward) build on the returned client.
func DialClient(cfg *config.Config, credential, device string) (*ssh.Client, error) {
	totp, err := auth.GenerateTOTP(cfg.MFAToken)
	if err != nil {
		return nil, fmt.Errorf("TOTP generation failed: %w", err)
	}

	// Try multiple username formats: with %tenant, then without.
	formats := []string{
		fmt.Sprintf("%s[%s@%s]%s%%%s", cfg.User, credential, device, totp, cfg.Tenant),
		fmt.Sprintf("%s[%s@%s]%s", cfg.User, credential, device, totp),
	}

	addr := fmt.Sprintf("%s:22", cfg.Host)

	for i, sshUser := range formats {
		log.Printf("gateway dial attempt %d: user=%s addr=%s", i+1, sshUser, addr)

		sshConfig := &ssh.ClientConfig{
			User: sshUser,
			Auth: []ssh.AuthMethod{
				ssh.Password(cfg.Password),
				ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
					answers := make([]string, len(questions))
					for i := range answers {
						answers[i] = cfg.Password
					}
					return answers, nil
				}),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         15 * time.Second,
			Config: ssh.Config{
				KeyExchanges: []string{
					"curve25519-sha256",
					"curve25519-sha256@libssh.org",
					"ecdh-sha2-nistp256",
					"ecdh-sha2-nistp384",
					"ecdh-sha2-nistp521",
					"diffie-hellman-group14-sha256",
					"diffie-hellman-group14-sha1",
					"diffie-hellman-group1-sha1",
					"diffie-hellman-group-exchange-sha256",
				},
				Ciphers: []string{
					"aes128-gcm@openssh.com",
					"aes256-gcm@openssh.com",
					"chacha20-poly1305@openssh.com",
					"aes128-ctr",
					"aes192-ctr",
					"aes256-ctr",
					"aes128-cbc",
					"aes192-cbc",
					"aes256-cbc",
					"3des-cbc",
				},
			},
		}

		sshClient, err := ssh.Dial("tcp", addr, sshConfig)
		if err != nil {
			log.Printf("gateway dial attempt %d failed: %v", i+1, err)
			// Regenerate TOTP for the next attempt (the code may have expired).
			if i < len(formats)-1 {
				totp, _ = auth.GenerateTOTP(cfg.MFAToken)
				formats[i+1] = fmt.Sprintf("%s[%s@%s]%s", cfg.User, credential, device, totp)
			}
			continue
		}

		log.Printf("gateway SSH connected with format %d", i+1)
		return sshClient, nil
	}

	return nil, fmt.Errorf("SSH connection failed: all formats tried for %s@%s on %s", credential, device, addr)
}

func (s *Session) close() {
	s.closeSudoShell()
	if s.sftpClient != nil {
		s.sftpClient.Close()
		s.sftpClient = nil
	}
	if s.sshClient != nil {
		s.sshClient.Close()
		s.sshClient = nil
	}
}

// closeSudoShell tears down the interactive shell + its connection.
func (s *Session) closeSudoShell() {
	if s.sudoIn != nil {
		s.sudoIn.Close()
		s.sudoIn = nil
	}
	if s.sudoSess != nil {
		s.sudoSess.Close()
		s.sudoSess = nil
	}
	if s.sudoClient != nil {
		s.sudoClient.Close()
		s.sudoClient = nil
	}
}

// ensureSFTP switches the session into SFTP mode (closing the shell if open and
// reconnecting the SFTP subsystem if needed). Caller must hold s.mu.
func (s *Session) ensureSFTP() error {
	if s.sudoIn != nil || s.sudoClient != nil {
		s.closeSudoShell()
	}
	if s.sftpClient != nil {
		return nil
	}
	c, err := DialClient(s.cfg, s.credential, s.device)
	if err != nil {
		return fmt.Errorf("SFTP reconnect: %w", err)
	}
	sf, err := sftp.NewClient(c)
	if err != nil {
		c.Close()
		return fmt.Errorf("SFTP subsystem: %w", err)
	}
	s.sshClient = c
	s.sftpClient = sf
	return nil
}

// ensureShell switches the session into interactive-shell mode: it closes the
// SFTP subsystem (senhasegura only grants one device session per credential, so
// the shell would otherwise land in the proxy shell) and opens a device bash on a
// fresh connection. Caller must hold s.mu.
// errDeviceBusy means the interactive shell landed in the senhasegura proxy shell
// because the device session was not (yet) free — worth a short retry.
var errDeviceBusy = fmt.Errorf("device shell unavailable: another session is active for this credential (senhasegura allows one at a time)")

// ensureShell opens the interactive device shell, retrying briefly: after the SFTP
// subsystem is closed, senhasegura may take a moment to release the device session,
// during which a new connection lands in the proxy shell.
func (s *Session) ensureShell() error {
	if s.sudoIn != nil && s.sudoClient != nil {
		return nil
	}
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(4 * time.Second)
		}
		err = s.openShellOnce()
		if err == nil {
			return nil
		}
		if err != errDeviceBusy {
			return err
		}
	}
	return err
}

func (s *Session) openShellOnce() error {
	if s.sudoIn != nil && s.sudoClient != nil {
		return nil
	}
	// Release the device session held by SFTP.
	if s.sftpClient != nil {
		s.sftpClient.Close()
		s.sftpClient = nil
	}
	if s.sshClient != nil {
		s.sshClient.Close()
		s.sshClient = nil
	}

	c, err := DialClient(s.cfg, s.credential, s.device)
	if err != nil {
		return fmt.Errorf("shell connect: %w", err)
	}
	sess, err := c.NewSession()
	if err != nil {
		c.Close()
		return fmt.Errorf("shell session: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 0, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}
	if err := sess.RequestPty("xterm", 40, 200, modes); err != nil {
		sess.Close()
		c.Close()
		return fmt.Errorf("shell pty: %w", err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		c.Close()
		return err
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		c.Close()
		return err
	}
	buf := &bytes.Buffer{}
	s.sudoBuf = buf
	go func() {
		b := make([]byte, 8192)
		for {
			n, e := out.Read(b)
			if n > 0 {
				s.sudoBufMu.Lock()
				buf.Write(b[:n])
				s.sudoBufMu.Unlock()
			}
			if e != nil {
				return
			}
		}
	}()
	if err := sess.Shell(); err != nil {
		sess.Close()
		c.Close()
		return fmt.Errorf("shell start: %w", err)
	}
	s.sudoClient = c
	s.sudoSess = sess
	s.sudoIn = in

	// PASSIVELY wait for the device bash to auto-connect. senhasegura lands you in
	// its proxy shell first, then auto-opens the device session (target is encoded
	// in the login name). Sending ANY input before the device bash is ready aborts
	// that auto-connect ("terminated by Segura System Proxy") and leaves us stuck in
	// the proxy shell — so we must NOT write anything here. The device login shell
	// prints "Last login:" / a "user@host:~$" prompt when ready.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		s.sudoBufMu.Lock()
		b := buf.Bytes()
		ready := bytes.Contains(b, []byte("Last login:")) ||
			bytes.Contains(b, []byte(":~$")) || bytes.Contains(b, []byte(":~#"))
		s.sudoBufMu.Unlock()
		if ready {
			time.Sleep(1500 * time.Millisecond) // let the prompt settle
			s.sudoBufMu.Lock()
			buf.Reset()
			s.sudoBufMu.Unlock()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	// Never reached device bash → the device session is unavailable (busy/blocked).
	s.closeSudoShell()
	return errDeviceBusy
}

// ListDir lists directory contents.
func (s *Session) ListDir(path string) ([]FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return nil, err
	}

	entries, err := s.sftpClient.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", path, err)
	}

	var files []FileInfo
	for _, entry := range entries {
		fullPath := path
		if fullPath != "/" {
			fullPath += "/"
		}
		fullPath += entry.Name()

		files = append(files, FileInfo{
			Name:    entry.Name(),
			Path:    fullPath,
			IsDir:   entry.IsDir(),
			Size:    entry.Size(),
			ModTime: entry.ModTime().Format(time.RFC3339),
			Mode:    entry.Mode().String(),
		})
	}
	return files, nil
}

// ReadFile reads a remote file and returns its content.
func (s *Session) ReadFile(path string, maxSize int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return nil, err
	}

	f, err := s.sftpClient.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	// Limit read size
	if maxSize <= 0 {
		maxSize = 10 * 1024 * 1024 // 10MB default
	}

	data, err := io.ReadAll(io.LimitReader(f, maxSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return data, nil
}

// IsPermissionError checks if an error is a permission denied error.
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "Permission denied") ||
		strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "operation not permitted")
}

// WriteFile writes content to a remote file.
func (s *Session) WriteFile(path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return err
	}

	f, err := s.sftpClient.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// runSudoCmd runs cmd as root via the interactive device shell. SSH exec is blocked
// by the proxy, so it cannot use `sudo -S`; instead it runs `sudo bash -c '<cmd>'`
// in a PTY where senhasegura injects the device password. The password argument is
// unused (kept for signature compatibility). A non-zero exit becomes an error.
func (s *Session) runSudoCmd(cmd string, password string) ([]byte, error) {
	_ = password
	if err := s.ensureShell(); err != nil {
		return nil, err
	}
	log.Printf("SFTP sudo (pty): %s", cmd)
	escaped := strings.ReplaceAll(cmd, `'`, `'\''`)
	out, code, err := s.sendShellLine("sudo bash -c '" + escaped + "'")
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("sudo exited %d", code)
	}
	return out, nil
}

// writeSudoShell writes content to target as root. It stages the raw bytes in
// /tmp over SFTP (always user-writable), then switches to the interactive shell
// and runs a single simple `sudo cp` (heredocs over the PTY proved unreliable with
// senhasegura's sudo automation). The staged temp is removed on success.
func (s *Session) writeSudoShell(target string, content []byte) error {
	// 1) Stage over SFTP.
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	tmp := fmt.Sprintf("/tmp/.segura_stage_%d", time.Now().UnixNano())
	f, err := s.sftpClient.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("stage temp file: %w", err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return fmt.Errorf("stage write: %w", err)
	}
	f.Close()

	// 2) Privileged copy via a single sudo (ensureShell closes SFTP; the staged
	//    /tmp file persists on the device).
	dir := path.Dir(target)
	cmd := "mkdir -p " + shellQuote(dir) + " && cp -f " + shellQuote(tmp) + " " + shellQuote(target) + " && rm -f " + shellQuote(tmp)
	log.Printf("SFTP sudo write: %s (%d bytes)", target, len(content))
	if _, err := s.runSudoCmd(cmd, ""); err != nil {
		return err
	}
	return nil
}

// sendShellLine writes a command (which may span multiple lines, e.g. a heredoc)
// to the interactive shell, appends an exit-code sentinel, and waits for it —
// allowing time for senhasegura's "SUDO Automation". Returns output + exit code.
func (s *Session) sendShellLine(cmd string) ([]byte, int, error) {
	// Phase 1: run the command alone. We must NOT append the exit-code probe on the
	// same input burst: senhasegura's "SUDO Automation" swallows any pending input
	// while it injects the password, which would eat our probe and hang forever.
	s.sudoBufMu.Lock()
	s.sudoBuf.Reset()
	s.sudoBufMu.Unlock()
	if _, err := io.WriteString(s.sudoIn, cmd+"\n"); err != nil {
		s.closeSudoShell()
		return nil, -1, fmt.Errorf("shell write: %w", err)
	}
	// Wait for the command (incl. SUDO Automation) to finish — i.e. output goes idle.
	out, ok := s.waitIdle(3*time.Second, 90*time.Second)
	if !ok {
		s.sudoBufMu.Lock()
		dump := s.sudoBuf.String()
		s.sudoBufMu.Unlock()
		if len(dump) > 400 {
			dump = dump[len(dump)-400:]
		}
		log.Printf("SFTP sudo TIMEOUT (phase1); buffer tail=%q", dump)
		s.closeSudoShell()
		return nil, -1, fmt.Errorf("shell command timed out (SUDO Automation did not complete)")
	}

	// Phase 2: now that the prompt is back, probe the exit code separately.
	s.sudoBufMu.Lock()
	s.sudoBuf.Reset()
	s.sudoBufMu.Unlock()
	if _, err := io.WriteString(s.sudoIn, "echo __SEGRC__$?__\n"); err != nil {
		s.closeSudoShell()
		return out, -1, fmt.Errorf("shell write: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.sudoBufMu.Lock()
		m := sudoRCRe.FindSubmatch(s.sudoBuf.Bytes())
		s.sudoBufMu.Unlock()
		if m != nil {
			code, _ := strconv.Atoi(string(m[1]))
			return out, code, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	// Couldn't read the code, but phase 1 completed — assume success.
	return out, 0, nil
}

// waitIdle blocks until the sudo buffer receives no new bytes for `quiet` (and is
// non-empty), or until `max` elapses. Returns the buffer contents and whether it
// went idle. Used to detect a command's completion (prompt returned).
func (s *Session) waitIdle(quiet, max time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(max)
	lastLen := -1
	lastChange := time.Now()
	for time.Now().Before(deadline) {
		s.sudoBufMu.Lock()
		n := s.sudoBuf.Len()
		s.sudoBufMu.Unlock()
		if n != lastLen {
			lastLen = n
			lastChange = time.Now()
		} else if n > 0 && time.Since(lastChange) >= quiet {
			s.sudoBufMu.Lock()
			out := make([]byte, s.sudoBuf.Len())
			copy(out, s.sudoBuf.Bytes())
			s.sudoBufMu.Unlock()
			return out, true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, false
}

// WriteFileSudo writes content via sudo: upload to /tmp then sudo cp to target.
func (s *Session) WriteFileSudo(path string, data []byte, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	return s.writeSudoShell(path, data)
}

// runCmd executes a plain (non-sudo) command over SSH and returns its combined output.
func (s *Session) runCmd(cmd string) ([]byte, error) {
	session, err := s.sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("SSH session failed: %w", err)
	}
	defer session.Close()
	log.Printf("SFTP exec: %s", cmd)
	return session.CombinedOutput(cmd)
}

// shellQuote single-quotes a string so it is safe to embed in a shell command,
// preventing path/command injection.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cmdError wraps a command failure with its output so IsPermissionError can see it.
func cmdError(action string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	return fmt.Errorf("%s failed: %s", action, msg)
}

// Exists reports whether a path exists on the remote.
func (s *Session) Exists(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureSFTP(); err != nil {
		return false
	}
	_, err := s.sftpClient.Stat(path)
	return err == nil
}

// Mkdir creates a directory (and any missing parents).
func (s *Session) Mkdir(path string, sudo bool, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if sudo {
		out, err := s.runSudoCmd("mkdir -p -- "+shellQuote(path), password)
		if err != nil {
			return cmdError("mkdir", out, err)
		}
		return nil
	}
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	if err := s.sftpClient.MkdirAll(path); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	return nil
}

// CreateFile creates a new empty file (fails if it already exists).
func (s *Session) CreateFile(path string, sudo bool, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if sudo {
		out, err := s.runSudoCmd("touch -- "+shellQuote(path), password)
		if err != nil {
			return cmdError("create file", out, err)
		}
		return nil
	}
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	f, err := s.sftpClient.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	f.Close()
	return nil
}

// Rename moves or renames a file or directory.
func (s *Session) Rename(oldPath, newPath string, sudo bool, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if sudo {
		out, err := s.runSudoCmd("mv -f -- "+shellQuote(oldPath)+" "+shellQuote(newPath), password)
		if err != nil {
			return cmdError("move", out, err)
		}
		return nil
	}
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	if err := s.sftpClient.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("move %s -> %s: %w", oldPath, newPath, err)
	}
	return nil
}

// Copy copies a file or directory (recursive, preserving attributes) to dst.
func (s *Session) Copy(src, dst string, sudo bool, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if sudo {
		out, err := s.runSudoCmd("cp -a -- "+shellQuote(src)+" "+shellQuote(dst), password)
		if err != nil {
			return cmdError("copy", out, err)
		}
		return nil
	}
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	return s.copyRecursive(src, dst)
}

func (s *Session) copyRecursive(src, dst string) error {
	info, err := s.sftpClient.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if info.IsDir() {
		if err := s.sftpClient.Mkdir(dst); err != nil {
			return fmt.Errorf("mkdir %s: %w", dst, err)
		}
		_ = s.sftpClient.Chmod(dst, info.Mode().Perm())
		entries, err := s.sftpClient.ReadDir(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
		for _, e := range entries {
			if err := s.copyRecursive(joinRemote(src, e.Name()), joinRemote(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	sf, err := s.sftpClient.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer sf.Close()
	df, err := s.sftpClient.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer df.Close()
	if _, err := io.Copy(df, sf); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	_ = s.sftpClient.Chmod(dst, info.Mode().Perm())
	return nil
}

// joinRemote joins remote path segments with "/" (remote paths are POSIX).
func joinRemote(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// Remove deletes a file or directory (recursive).
func (s *Session) Remove(path string, sudo bool, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if sudo {
		out, err := s.runSudoCmd("rm -rf -- "+shellQuote(path), password)
		if err != nil {
			return cmdError("delete", out, err)
		}
		return nil
	}
	if err := s.ensureSFTP(); err != nil {
		return err
	}
	return s.removeRecursive(path)
}

func (s *Session) removeRecursive(p string) error {
	info, err := s.sftpClient.Lstat(p)
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if info.IsDir() {
		entries, err := s.sftpClient.ReadDir(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		for _, e := range entries {
			if err := s.removeRecursive(joinRemote(p, e.Name())); err != nil {
				return err
			}
		}
		return s.sftpClient.RemoveDirectory(p)
	}
	return s.sftpClient.Remove(p)
}

// Download streams a file to the writer.
func (s *Session) Download(path string, w io.Writer) (int64, fs.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return 0, nil, err
	}

	f, err := s.sftpClient.Open(path)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to stat %s: %w", path, err)
	}

	n, err := io.Copy(w, f)
	if err != nil {
		return n, info, fmt.Errorf("download failed: %w", err)
	}
	return n, info, nil
}

// Upload writes from a reader to a remote file.
func (s *Session) Upload(path string, r io.Reader) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return 0, err
	}

	f, err := s.sftpClient.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, fmt.Errorf("failed to create %s: %w", path, err)
	}
	defer f.Close()

	n, err := io.Copy(f, r)
	if err != nil {
		return n, fmt.Errorf("upload failed: %w", err)
	}
	return n, nil
}

// UploadSudo uploads a file as root via the interactive shell (senhasegura injects
// the sudo password). The content is buffered then written with a single sudo, so
// it works for directories outside the user's home where SFTP is denied.
func (s *Session) UploadSudo(path string, r io.Reader, password string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("read upload: %w", err)
	}
	if err := s.writeSudoShell(path, data); err != nil {
		return 0, err
	}
	log.Printf("SFTP sudo upload: %s (%d bytes)", path, len(data))
	return int64(len(data)), nil
}

// Stat returns file info for a path.
func (s *Session) Stat(path string) (fs.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return nil, err
	}

	return s.sftpClient.Stat(path)
}

// Getwd returns the remote working directory (the SFTP session's home dir).
func (s *Session) Getwd() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
	if err := s.ensureSFTP(); err != nil {
		return "", err
	}

	return s.sftpClient.Getwd()
}
