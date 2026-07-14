package sftpclient

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
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
type Session struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	lastUsed   time.Time
	mu         sync.Mutex
	credential string
	device     string
}

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
	totp, err := auth.GenerateTOTP(m.cfg.MFAToken)
	if err != nil {
		return nil, fmt.Errorf("TOTP generation failed: %w", err)
	}

	// Try multiple username formats: with %tenant, then without
	formats := []string{
		fmt.Sprintf("%s[%s@%s]%s%%%s", m.cfg.User, credential, device, totp, m.cfg.Tenant),
		fmt.Sprintf("%s[%s@%s]%s", m.cfg.User, credential, device, totp),
	}

	addr := fmt.Sprintf("%s:22", m.cfg.Host)

	for i, sshUser := range formats {
		log.Printf("SFTP dial attempt %d: user=%s addr=%s", i+1, sshUser, addr)

		sshConfig := &ssh.ClientConfig{
			User: sshUser,
			Auth: []ssh.AuthMethod{
				ssh.Password(m.cfg.Password),
				ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
					log.Printf("SFTP KBI: user=%q instruction=%q questions=%d", user, instruction, len(questions))
					answers := make([]string, len(questions))
					for i := range answers {
						answers[i] = m.cfg.Password
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
			log.Printf("SFTP dial attempt %d failed: %v", i+1, err)
			// Regenerate TOTP for next attempt (may have expired)
			if i < len(formats)-1 {
				totp, _ = auth.GenerateTOTP(m.cfg.MFAToken)
				formats[i+1] = fmt.Sprintf("%s[%s@%s]%s", m.cfg.User, credential, device, totp)
			}
			continue
		}

		log.Printf("SFTP SSH connected with format %d", i+1)

		sftpClient, err := sftp.NewClient(sshClient)
		if err != nil {
			sshClient.Close()
			log.Printf("SFTP subsystem failed: %v", err)
			continue
		}

		log.Printf("SFTP session established for %s@%s", credential, device)
		return &Session{
			sshClient:  sshClient,
			sftpClient: sftpClient,
			lastUsed:   time.Now(),
			credential: credential,
			device:     device,
		}, nil
	}

	return nil, fmt.Errorf("SSH connection failed: all formats tried for %s@%s on %s", credential, device, addr)
}

func (s *Session) close() {
	if s.sftpClient != nil {
		s.sftpClient.Close()
		s.sftpClient = nil
	}
	if s.sshClient != nil {
		s.sshClient.Close()
		s.sshClient = nil
	}
}

// ListDir lists directory contents.
func (s *Session) ListDir(path string) ([]FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

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

// runSudoCmd executes a command with sudo, piping the password via stdin (-S flag).
func (s *Session) runSudoCmd(cmd string, password string) ([]byte, error) {
	session, err := s.sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("SSH session failed: %w", err)
	}
	defer session.Close()

	// Use sudo -S to read password from stdin
	fullCmd := fmt.Sprintf("echo '%s' | sudo -S %s", password, cmd)
	log.Printf("SFTP sudo exec: %s", cmd)
	output, err := session.CombinedOutput(fullCmd)
	return output, err
}

// WriteFileSudo writes content via sudo: upload to /tmp then sudo cp to target.
func (s *Session) WriteFileSudo(path string, data []byte, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

	// 1. Upload to a temp file in /tmp
	tmpPath := fmt.Sprintf("/tmp/.segura_tmp_%d", time.Now().UnixNano())

	f, err := s.sftpClient.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	f.Close()

	// 2. sudo cp temp -> target
	cmd := fmt.Sprintf("cp '%s' '%s' && chmod --reference='%s' '%s' 2>/dev/null; rm -f '%s'",
		tmpPath, path, path, path, tmpPath)
	output, err := s.runSudoCmd(cmd, password)
	if err != nil {
		s.sftpClient.Remove(tmpPath)
		return fmt.Errorf("sudo write failed: %s %w", string(output), err)
	}

	log.Printf("SFTP sudo write: %s (%d bytes)", path, len(data))
	return nil
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
	if err := s.sftpClient.Rename(oldPath, newPath); err != nil {
		// Cross-device or other SFTP rename failures fall back to mv.
		out, cerr := s.runCmd("mv -f -- " + shellQuote(oldPath) + " " + shellQuote(newPath))
		if cerr != nil {
			return cmdError("move", out, cerr)
		}
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

// UploadSudo uploads a file via sudo: write to /tmp then sudo cp to target.
func (s *Session) UploadSudo(path string, r io.Reader, password string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

	// 1. Upload to temp file
	tmpPath := fmt.Sprintf("/tmp/.segura_up_%d", time.Now().UnixNano())
	f, err := s.sftpClient.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file: %w", err)
	}

	n, err := io.Copy(f, r)
	f.Close()
	if err != nil {
		s.sftpClient.Remove(tmpPath)
		return n, fmt.Errorf("upload to temp failed: %w", err)
	}

	// 2. sudo cp temp to target, create parent dirs if needed
	cmd := fmt.Sprintf("mkdir -p \"$(dirname '%s')\" && cp '%s' '%s' && rm -f '%s'",
		path, tmpPath, path, tmpPath)
	output, err := s.runSudoCmd(cmd, password)
	if err != nil {
		s.sftpClient.Remove(tmpPath)
		return 0, fmt.Errorf("sudo upload failed: %s %w", string(output), err)
	}

	log.Printf("SFTP sudo upload: %s (%d bytes)", path, n)
	return n, nil
}

// Stat returns file info for a path.
func (s *Session) Stat(path string) (fs.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

	return s.sftpClient.Stat(path)
}
