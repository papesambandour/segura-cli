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

	// Transform the remote path to senhasegura format
	src = rewriteRemotePath(cfg, src, totp)
	dest = rewriteRemotePath(cfg, dest, totp)

	scpArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "PubkeyAuthentication=no",
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

// rewriteRemotePath converts "credential@device:/path" to
// "vaultUser[credential@device]totp@host:/path" for the senhasegura proxy.
// Local paths are returned unchanged.
func rewriteRemotePath(cfg *config.Config, path string, totp string) string {
	// Check if this is a remote path (contains ":" with "@" before it)
	atIdx := strings.Index(path, "@")
	colonIdx := strings.Index(path, ":")

	if atIdx == -1 || colonIdx == -1 || atIdx > colonIdx {
		// Local path
		return path
	}

	// Split into credential@device and remote path
	credential := path[:atIdx]
	rest := path[atIdx+1:] // device:/path
	deviceAndPath := strings.SplitN(rest, ":", 2)
	device := deviceAndPath[0]
	remotePath := ""
	if len(deviceAndPath) > 1 {
		remotePath = deviceAndPath[1]
	}

	// Build senhasegura format: vaultUser[credential@device]totp%tenant@host:/path
	return fmt.Sprintf("%s[%s@%s]%s%%%s@%s:%s",
		cfg.User, credential, device, totp, cfg.Tenant, cfg.Host, remotePath)
}

// IsRemotePath checks if a path string refers to a remote location.
func IsRemotePath(path string) bool {
	atIdx := strings.Index(path, "@")
	colonIdx := strings.Index(path, ":")
	return atIdx != -1 && colonIdx != -1 && atIdx < colonIdx
}
