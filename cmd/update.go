package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for updates and upgrade segura to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		fmt.Printf("Current version: %s\n", version)

		// Fetch latest release from GitHub
		latest, err := fetchLatestRelease()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}

		latestTag := latest.TagName
		fmt.Printf("Latest version:  %s\n", latestTag)

		if !isNewer(version, latestTag) {
			fmt.Println("You are already on the latest version.")
			return nil
		}

		fmt.Printf("\nNew version available: %s -> %s\n", version, latestTag)

		// Download new binary
		binaryURL := fmt.Sprintf(
			"https://github.com/%s/releases/download/%s/segura-%s-%s",
			githubRepo, latestTag, runtime.GOOS, runtime.GOARCH,
		)

		fmt.Printf("Downloading %s...\n", binaryURL)

		tmpFile, err := downloadRelease(binaryURL)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		defer os.Remove(tmpFile)

		// Replace current binary
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot determine current binary path: %w", err)
		}

		if err := replaceBinary(tmpFile, execPath); err != nil {
			return fmt.Errorf("failed to replace binary: %w", err)
		}

		fmt.Printf("Updated segura to %s\n", latestTag)

		// Refresh shell completion so new commands/flags autocomplete.
		// Best-effort: never fail the update over completion.
		if shell := detectShell(); shell != "" {
			if path, _, cerr := installShellCompletion(shell); cerr != nil {
				fmt.Fprintf(os.Stderr, "Note: could not refresh %s completion: %v\n", shell, cerr)
			} else {
				fmt.Printf("Refreshed %s completion (%s)\n", shell, path)
			}
		}
		return nil
	},
}

func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

type progressWriter struct {
	total      int64
	downloaded int64
	lastPct    int
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)
	if pw.total > 0 {
		pct := int(pw.downloaded * 100 / pw.total)
		if pct != pw.lastPct {
			pw.lastPct = pct
			fmt.Fprintf(os.Stderr, "\r  [%-50s] %3d%%", strings.Repeat("#", pct/2), pct)
		}
	}
	return n, nil
}

func downloadRelease(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "segura-update-*")
	if err != nil {
		return "", err
	}

	pw := &progressWriter{total: resp.ContentLength}
	if _, err := io.Copy(tmp, io.TeeReader(resp.Body, pw)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	fmt.Fprintln(os.Stderr) // newline after progress bar

	tmp.Close()
	return tmp.Name(), nil
}

func replaceBinary(src, dst string) error {
	// Read downloaded binary
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Get permissions of current binary
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}

	// Write to destination (atomic-ish: write tmp next to target, then rename)
	tmpDst := dst + ".new"
	if err := os.WriteFile(tmpDst, data, info.Mode()); err != nil {
		return fmt.Errorf("cannot write new binary (try with sudo): %w", err)
	}

	if err := os.Rename(tmpDst, dst); err != nil {
		os.Remove(tmpDst)
		return fmt.Errorf("cannot replace binary (try with sudo): %w", err)
	}

	return nil
}

// isNewer returns true if latestTag is a newer version than current.
// Handles both semver tags (v1.2.3) and commit-based versions.
func isNewer(current, latestTag string) bool {
	// If current is "dev" or a commit hash, any release is newer
	if current == "dev" || current == "" || !strings.HasPrefix(current, "v") {
		if strings.HasPrefix(latestTag, "v") {
			return true
		}
		return current != latestTag
	}

	// Simple semver compare: strip "v" prefix and compare parts
	cur := strings.TrimPrefix(current, "v")
	lat := strings.TrimPrefix(latestTag, "v")

	// Remove -dirty or other suffixes
	if i := strings.IndexByte(cur, '-'); i != -1 {
		cur = cur[:i]
	}
	if i := strings.IndexByte(lat, '-'); i != -1 {
		lat = lat[:i]
	}

	curParts := strings.Split(cur, ".")
	latParts := strings.Split(lat, ".")

	for i := 0; i < len(curParts) || i < len(latParts); i++ {
		var c, l int
		if i < len(curParts) {
			fmt.Sscanf(curParts[i], "%d", &c)
		}
		if i < len(latParts) {
			fmt.Sscanf(latParts[i], "%d", &l)
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}

	return false // equal
}
