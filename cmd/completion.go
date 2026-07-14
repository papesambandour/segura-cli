package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const completionBlockTag = "# SEGURA-CLI completion"

func init() {
	rootCmd.AddCommand(installCompletionCmd)
}

// installCompletionCmd sets up shell autocompletion for the current shell.
// The same logic runs automatically at the end of `segura update`.
var installCompletionCmd = &cobra.Command{
	Use:   "install-completion",
	Short: "Install shell autocompletion for segura (auto-detects your shell)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		shell := detectShell()
		if shell == "" {
			return fmt.Errorf("could not detect shell ($SHELL is empty); see 'segura completion --help'")
		}

		path, rc, err := installShellCompletion(shell)
		if err != nil {
			return err
		}

		fmt.Printf("Installed %s completion -> %s\n", shell, path)
		if rc != "" {
			fmt.Printf("Wired into %s\n", rc)
		}
		fmt.Println("Restart your shell (or source your rc) to activate it.")
		return nil
	},
}

// detectShell returns the base name of the login shell ("zsh", "bash", "fish").
func detectShell() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		return ""
	}
	return filepath.Base(sh)
}

// installShellCompletion generates the completion script for shell, writes it to
// the shell's standard location, and (for zsh/bash) ensures the rc file loads it.
// Returns the script path and the rc file touched (empty if none). Idempotent.
func installShellCompletion(shell string) (scriptPath, rcPath string, err error) {
	home, err := userHome()
	if err != nil {
		return "", "", err
	}

	switch shell {
	case "zsh":
		dir := filepath.Join(home, ".zsh", "completions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		path := filepath.Join(dir, "_segura")
		if err := writeGen(path, func(b *bytes.Buffer) error { return rootCmd.GenZshCompletion(b) }); err != nil {
			return "", "", err
		}
		rc := filepath.Join(home, ".zshrc")
		block := fmt.Sprintf("%s\nfpath=(%q $fpath)\nautoload -Uz compinit && compinit\n%s end\n",
			completionBlockTag, dir, completionBlockTag)
		if err := ensureRcBlock(rc, completionBlockTag, block); err != nil {
			return "", "", err
		}
		return path, rc, nil

	case "bash":
		dir := filepath.Join(home, ".bash_completion.d")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		path := filepath.Join(dir, "segura")
		if err := writeGen(path, func(b *bytes.Buffer) error { return rootCmd.GenBashCompletionV2(b, true) }); err != nil {
			return "", "", err
		}
		rc := filepath.Join(home, ".bashrc")
		block := fmt.Sprintf("%s\n[ -f %q ] && source %q\n%s end\n",
			completionBlockTag, path, path, completionBlockTag)
		if err := ensureRcBlock(rc, completionBlockTag, block); err != nil {
			return "", "", err
		}
		return path, rc, nil

	case "fish":
		// fish auto-loads this directory; no rc edit needed.
		dir := filepath.Join(home, ".config", "fish", "completions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		path := filepath.Join(dir, "segura.fish")
		if err := writeGen(path, func(b *bytes.Buffer) error { return rootCmd.GenFishCompletion(b, true) }); err != nil {
			return "", "", err
		}
		return path, "", nil

	default:
		return "", "", fmt.Errorf("unsupported shell %q; use 'segura completion %s --help' to set it up manually", shell, shell)
	}
}

// writeGen runs a Cobra completion generator into a buffer and writes it to path.
func writeGen(path string, gen func(*bytes.Buffer) error) error {
	var buf bytes.Buffer
	if err := gen(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ensureRcBlock appends block to rcPath unless a line containing tag is already
// present, so repeated installs/updates never duplicate it.
func ensureRcBlock(rcPath, tag, block string) error {
	if data, err := os.ReadFile(rcPath); err == nil && strings.Contains(string(data), tag) {
		return nil
	}
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n" + block)
	return err
}

// userHome resolves the invoking user's home even under sudo, so completion is
// installed for the real user rather than root when `sudo segura update` is used.
func userHome() (string, error) {
	if su := os.Getenv("SUDO_USER"); su != "" && su != "root" {
		if u, err := user.Lookup(su); err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	return os.UserHomeDir()
}
