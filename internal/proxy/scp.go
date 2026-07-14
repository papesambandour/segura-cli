package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

// CopyFile transfers a file via SCP through the senhasegura Terminal Proxy.
// It shells out to the system `sshpass` + `scp` command with the structured connection string.
//
// src/dest format: credential@device:/path or local path
// The senhasegura proxy expects: vaultUser[credential@device]totp@host:/path
func CopyFile(cfg *config.Config, src, dest string, port int) error {
	totp, err := auth.GenerateTOTP(cfg.MFAToken)
	if err != nil {
		return fmt.Errorf("failed to generate TOTP: %w", err)
	}

	// Transform the remote path to senhasegura format.
	// The proxy username ends in "%tenant"; OpenSSH percent-expands -o User=
	// values (e.g. "%y" -> "unknown key %y"), so the "%" must be escaped as
	// "%%" (a literal percent per the ssh_config TOKENS spec) before use.
	var sshUser string
	srcUser, src, srcRemote := rewriteRemotePath(cfg, src, totp)
	destUser, dest, destRemote := rewriteRemotePath(cfg, dest, totp)
	if srcRemote {
		sshUser = srcUser
	} else if destRemote {
		sshUser = destUser
	}

	scpArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "PubkeyAuthentication=no",
		"-o", fmt.Sprintf("User=%s", strings.ReplaceAll(sshUser, "%", "%%")),
		"-P", fmt.Sprintf("%d", port),
		src, dest,
	}

	var cmd *exec.Cmd

	// Use sshpass if available for automated password input
	if sshpassPath, lookErr := exec.LookPath("sshpass"); lookErr == nil {
		fullArgs := append([]string{"-e", "scp"}, scpArgs...)
		cmd = exec.Command(sshpassPath, fullArgs...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("SSHPASS=%s", cfg.Password))
	} else {
		fmt.Fprintf(os.Stderr, "Note: install 'sshpass' for automated password input\n")
		cmd = exec.Command("scp", scpArgs...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scp failed: %w", err)
	}
	return nil
}

// rewriteRemotePath converts "credential@device:/path" to the senhasegura proxy format.
// Returns: sshUser (for -o User=), remotePath (host:/path), isRemote.
// sshUser contains a raw "%tenant" suffix; the caller must escape "%" -> "%%"
// before passing it to scp -o User=, since OpenSSH percent-expands that value.
func rewriteRemotePath(cfg *config.Config, path string, totp string) (sshUser string, rewritten string, isRemote bool) {
	atIdx := strings.Index(path, "@")
	colonIdx := strings.Index(path, ":")

	if atIdx == -1 || colonIdx == -1 || atIdx > colonIdx {
		return "", path, false
	}

	credential := path[:atIdx]
	rest := path[atIdx+1:]
	deviceAndPath := strings.SplitN(rest, ":", 2)
	device := deviceAndPath[0]
	remotePath := ""
	if len(deviceAndPath) > 1 {
		remotePath = deviceAndPath[1]
	}

	user := fmt.Sprintf("%s[%s@%s]%s%%%s", cfg.User, credential, device, totp, cfg.Tenant)
	return user, fmt.Sprintf("%s:%s", cfg.Host, remotePath), true
}

// IsRemotePath checks if a path string refers to a remote location.
func IsRemotePath(path string) bool {
	atIdx := strings.Index(path, "@")
	colonIdx := strings.Index(path, ":")
	return atIdx != -1 && colonIdx != -1 && atIdx < colonIdx
}
