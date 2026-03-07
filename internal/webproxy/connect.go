package webproxy

import (
	"fmt"
	"os/exec"
	"runtime"

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
	dashboardHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		return fmt.Errorf("failed to fetch dashboard: %w", err)
	}

	credentials := ParseCredentials(dashboardHTML)
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

// ListCredentials authenticates and returns available credentials.
func ListCredentials(cfg *config.Config) ([]Credential, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Login(); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	dashboardHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dashboard: %w", err)
	}

	return ParseCredentials(dashboardHTML), nil
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
