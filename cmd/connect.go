package cmd

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/picker"
	"segura-cli/internal/proxy"
	"segura-cli/internal/recent"
	"segura-cli/internal/webproxy"
)

var connectPort int
var useBrowser bool
var browserPort int

var connectCmd = &cobra.Command{
	Use:   "connect [credential@device]",
	Short: "Open a terminal session to a device through senhasegura",
	Long: `Opens an interactive terminal session to a target device through the senhasegura PAM platform.

Run with no argument to open an interactive fuzzy picker of your credentials,
with favorites (★) and recently-used (•) targets listed first. A partial
argument without an '@' pre-filters the picker.

By default, connects via SSH Terminal Proxy.
Use --browser to open the session in the local web terminal instead.
Use --port to specify a custom SSH port.

Examples:
  segura connect                       # interactive picker
  segura connect web                   # picker pre-filtered by "web"
  segura connect root@192.168.1.10
  segura connect admin@webserver01
  segura connect root@192.168.1.10 --port 2222
  segura connect root@192.168.1.10 --browser`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		var credential, device, ip string
		if len(args) == 1 && strings.Contains(args[0], "@") {
			credential, device, err = parseTarget(args[0])
			if err != nil {
				return err
			}
		} else {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			credential, device, ip, err = pickTarget(cfg, query)
			if err != nil {
				if errors.Is(err, picker.ErrCancelled) {
					fmt.Println("Cancelled.")
					return nil
				}
				return err
			}
		}

		return runConnection(cfg, credential, device, ip)
	},
}

// runConnection records the target and opens the session (browser or SSH proxy).
// Shared by `connect` and `recent` so they behave identically.
func runConnection(cfg *config.Config, credential, device, ip string) error {
	// Remember this target so the picker can surface it next time (best-effort).
	recordRecent(credential, device, ip)

	if useBrowser {
		return connectBrowser(credential, device, browserPort)
	}

	// Default: SSH Terminal Proxy
	fmt.Printf("Connecting to %s@%s via SSH Terminal Proxy on %s...\n", credential, device, cfg.Host)
	return proxy.Connect(cfg, credential, device, connectPort)
}

// pickTarget loads the credential list (cached) and opens the interactive picker,
// returning the chosen credential/device/ip.
func pickTarget(cfg *config.Config, query string) (credential, device, ip string, err error) {
	creds, err := webproxy.ListCredentials(cfg)
	if err != nil {
		return "", "", "", fmt.Errorf("could not load credentials: %w", err)
	}
	store, _ := recent.Load()
	item, err := picker.Select(picker.Build(creds, store), query)
	if err != nil {
		return "", "", "", err
	}
	return item.Username, item.Device, item.IP, nil
}

// recordRecent upserts a target into the recents store; failures are non-fatal.
func recordRecent(credential, device, ip string) {
	store, err := recent.Load()
	if err != nil {
		return
	}
	store.Record(credential, device, ip)
	_ = store.Save()
}

func init() {
	connectCmd.Flags().IntVar(&connectPort, "port", 22, "SSH port on the senhasegura host")
	connectCmd.Flags().BoolVar(&useBrowser, "browser", false, "Open session in local web terminal")
	connectCmd.Flags().IntVar(&browserPort, "web-port", 8080, "Web server port (used with --browser)")
	rootCmd.AddCommand(connectCmd)
}

// connectBrowser ensures the web server is running and opens the terminal in the browser.
func connectBrowser(credential, device string, port int) error {
	// Check if web server is already running on this port
	if !isPortOpen(port) {
		fmt.Printf("Starting web server on port %d...\n", port)
		if err := daemonStart(port); err != nil {
			// If already running error, that's fine
			if !strings.Contains(err.Error(), "already running") {
				return fmt.Errorf("failed to start web server: %w", err)
			}
		}
		// Wait for server to be ready
		for i := 0; i < 20; i++ {
			time.Sleep(250 * time.Millisecond)
			if isPortOpen(port) {
				break
			}
		}
	}

	termURL := fmt.Sprintf("http://localhost:%d/console.html?username=%s&ip=%s&device=%s",
		port,
		url.QueryEscape(credential),
		url.QueryEscape(device),
		url.QueryEscape(device),
	)

	fmt.Printf("Opening terminal for %s@%s in browser...\n", credential, device)
	fmt.Printf("URL: %s\n", termURL)

	return openBrowser(termURL)
}

func isPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

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
		return fmt.Errorf("unsupported platform. Open manually: %s", url)
	}
	return cmd.Start()
}

// parseTarget splits "credential@device" into its components.
func parseTarget(target string) (credential, device string, err error) {
	parts := strings.SplitN(target, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid target format: expected <credential>@<device>, got %q", target)
	}
	return parts[0], parts[1], nil
}
