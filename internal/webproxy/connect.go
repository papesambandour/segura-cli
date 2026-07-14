package webproxy

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"

	"segura-cli/internal/config"
)

// Connect authenticates to senhasegura and opens a terminal session.
// It uses the Guacamole WebSocket protocol to provide an interactive terminal.
func Connect(cfg *config.Config, credential, device string) error {
	return ConnectAndRun(cfg, credential, device)
}

// ConnectBrowser authenticates and opens the session in a web browser (fallback).
func ConnectBrowser(cfg *config.Config, credential, device string) error {
	fmt.Println("Authenticating to senhasegura...")

	client, err := NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Login(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	fmt.Println("Authentication successful.")

	fmt.Println("Fetching available credentials...")
	credentials, err := client.FetchAllCredentials()
	if err != nil {
		return err
	}
	if len(credentials) == 0 {
		return fmt.Errorf("no credentials found on dashboard")
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

	fmt.Printf("Opening session for %s@%s...\n", credential, device)
	proxyURL, err := client.GetProxyURL(cred.SRToken)
	if err != nil {
		return fmt.Errorf("failed to get proxy URL: %w", err)
	}

	fmt.Printf("Opening terminal in browser...\n")
	fmt.Printf("URL: %s\n", proxyURL)

	return openBrowser(proxyURL)
}

const (
	// maxCredentialPages is a safety backstop so the pagination loop can never
	// spin forever if the server ever changes its end-of-list behavior.
	maxCredentialPages = 1000
	// credentialPageBatch is how many dashboard pages are fetched concurrently
	// per round. Bounds both parallelism and the wasted empty-page fetches at
	// the tail (at most credentialPageBatch-1 extra requests).
	credentialPageBatch = 8
)

// FetchAllCredentials returns every credential across all dashboard pages.
// The senhasegura dashboard paginates via ?page=N (verified against a live
// instance): each page holds a fixed slice, and any page past the last — including
// far-out-of-range values — returns an EMPTY list rather than clamping to page 1.
// The page count isn't reliably known up front (the pagination control is
// windowed), so pages are fetched in concurrent batches and the walk stops at
// the first empty page (processed in page order). Results are de-duplicated
// across pages by identity (username + ip + device).
func (c *Client) FetchAllCredentials() ([]Credential, error) {
	var all []Credential
	seen := make(map[string]bool)

	for start := 1; start <= maxCredentialPages; start += credentialPageBatch {
		pages, err := c.fetchCredentialPages(start, credentialPageBatch)
		if err != nil {
			return nil, err
		}

		reachedEnd := false
		for _, creds := range pages { // page order preserved
			if len(creds) == 0 {
				reachedEnd = true
				break
			}
			for _, cr := range creds {
				key := cr.Username + "@" + cr.IP + "|" + cr.Device
				if !seen[key] {
					seen[key] = true
					all = append(all, cr)
				}
			}
		}
		if reachedEnd {
			break
		}
	}

	return all, nil
}

// fetchCredentialPages fetches `count` dashboard pages starting at `start`
// concurrently and returns their parsed credentials in page order.
func (c *Client) fetchCredentialPages(start, count int) ([][]Credential, error) {
	pages := make([][]Credential, count)
	errs := make([]error, count)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		page := start + i
		if page > maxCredentialPages {
			continue // leaves pages[i] nil -> treated as an empty (end) page
		}
		wg.Add(1)
		go func(i, page int) {
			defer wg.Done()
			html, err := c.GetPage(fmt.Sprintf("/flow/coge/desktop/dashboard?page=%d", page))
			if err != nil {
				errs[i] = fmt.Errorf("failed to fetch dashboard page %d: %w", page, err)
				return
			}
			pages[i] = ParseCredentials(html)
		}(i, page)
	}
	wg.Wait()

	// Surface the earliest (lowest-page) error conservatively — we can't yet
	// know which pages are past the real end.
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return pages, nil
}

// ListCredentials authenticates and returns all available credentials.
func ListCredentials(cfg *config.Config) ([]Credential, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Login(); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client.FetchAllCredentials()
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s. Open the URL manually: %s", runtime.GOOS, url)
	}

	return cmd.Start()
}
