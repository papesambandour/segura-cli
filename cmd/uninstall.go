package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	uninstallCmd.Flags().BoolVarP(&forceUninstall, "yes", "y", false, "skip confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}

var forceUninstall bool

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove segura binary, data, and environment variables",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot determine binary path: %w", err)
		}
		execPath, _ = filepath.EvalSymlinks(execPath)
		dataDir := pidDir() // ~/.segura

		fmt.Println("This will remove:")
		fmt.Printf("  Binary:   %s\n", execPath)
		fmt.Printf("  Data dir: %s\n", dataDir)
		fmt.Println("  SEGURA_* env vars from shell RC file")

		if !forceUninstall {
			fmt.Print("\nProceed? [y/N] ")
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// 1. Stop daemon if running
		if pid, err := readPID(); err == nil {
			fmt.Printf("Stopping daemon (PID %d)...\n", pid)
			_ = daemonStop()
		}

		// 2. Remove data directory (~/.segura)
		if _, err := os.Stat(dataDir); err == nil {
			if err := os.RemoveAll(dataDir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", dataDir, err)
			} else {
				fmt.Printf("Removed %s\n", dataDir)
			}
		}

		// 3. Clean SEGURA_* env vars from shell RC files
		cleanShellRC()

		// 4. Remove binary (do this last since we're running it)
		if err := os.Remove(execPath); err != nil {
			fmt.Fprintf(os.Stderr, "Could not remove binary: %v\n", err)
			fmt.Fprintf(os.Stderr, "You may need to run: sudo rm %s\n", execPath)
		} else {
			fmt.Printf("Removed %s\n", execPath)
		}

		fmt.Println("\nSegura has been uninstalled.")
		return nil
	},
}

func cleanShellRC() {
	home := os.Getenv("HOME")
	rcFiles := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
	}

	seguraVars := []string{
		"SEGURA_URL", "SEGURA_USER", "SEGURA_PASSWORD",
		"SEGURA_MFA_TOKEN", "SEGURA_TENANT",
	}

	for _, rcFile := range rcFiles {
		data, err := os.ReadFile(rcFile)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		var cleaned []string
		modified := false
		skipBlock := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)

			// Skip SEGURA-CLI env block (from Makefile install)
			if trimmed == "# SEGURA-CLI env" {
				skipBlock = true
				modified = true
				continue
			}
			if skipBlock {
				if trimmed == "# SEGURA-CLI env end" {
					skipBlock = false
				}
				continue
			}

			// Skip individual SEGURA_* exports (from install.sh)
			isSeguraVar := false
			for _, v := range seguraVars {
				if strings.HasPrefix(trimmed, "export "+v+"=") {
					isSeguraVar = true
					modified = true
					break
				}
			}
			if isSeguraVar {
				continue
			}

			// Skip the installer PATH comment + line
			if trimmed == "# Added by segura installer" {
				modified = true
				continue
			}

			cleaned = append(cleaned, line)
		}

		if modified {
			// Remove trailing empty lines
			for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
				cleaned = cleaned[:len(cleaned)-1]
			}
			cleaned = append(cleaned, "") // ensure final newline

			if err := os.WriteFile(rcFile, []byte(strings.Join(cleaned, "\n")), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not clean %s: %v\n", rcFile, err)
			} else {
				fmt.Printf("Cleaned SEGURA env vars from %s\n", rcFile)
			}
		}
	}
}
