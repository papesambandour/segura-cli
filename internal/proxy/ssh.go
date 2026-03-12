package proxy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

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

	// Read output from SSH, detect password prompt, auto-send password
	go func() {
		buf := make([]byte, 4096)
		var accumulated string
		passwordSent := false

		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				os.Stdout.Write(buf[:n])

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

	// Forward user stdin to the PTY
	go func() {
		io.Copy(ptmx, os.Stdin)
	}()

	return cmd.Wait()
}
