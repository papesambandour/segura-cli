package webproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

// BrowserLogin opens Chrome, navigates to senhasegura, and automatically
// fills in credentials and TOTP. The browser stays open after login.
func BrowserLogin(cfg *config.Config) error {
	baseURL := strings.TrimRight(cfg.URL, "/")

	// Find Chrome binary
	chromePath, err := findChrome()
	if err != nil {
		return err
	}
	fmt.Printf("Using Chrome: %s\n", chromePath)

	// Find a free port for CDP
	port, err := findFreePort()
	if err != nil {
		return fmt.Errorf("failed to find free port: %w", err)
	}

	// Create a dedicated user data directory so Chrome launches as a new
	// instance even if another Chrome is already running.
	userDataDir := filepath.Join(os.TempDir(), "segura-chrome-profile")
	lockFile := filepath.Join(userDataDir, "SingletonLock")
	if _, err := os.Lstat(lockFile); err == nil {
		userDataDir = filepath.Join(os.TempDir(), fmt.Sprintf("segura-chrome-%d", time.Now().UnixNano()))
	}
	if err := os.MkdirAll(userDataDir, 0700); err != nil {
		return fmt.Errorf("failed to create Chrome profile dir: %w", err)
	}

	// Launch Chrome with the senhasegura URL directly (opens a single tab)
	fmt.Println("Launching Chrome...")
	chromeCmd := exec.Command(chromePath,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		baseURL,
	)
	chromeCmd.Stdout = nil
	chromeCmd.Stderr = nil
	if err := chromeCmd.Start(); err != nil {
		return fmt.Errorf("failed to launch Chrome: %w", err)
	}
	// Detach — Chrome continues when our process exits
	go func() { chromeCmd.Wait() }()

	// Wait for CDP to be ready
	if err := waitForCDP(port, 15*time.Second); err != nil {
		return fmt.Errorf("Chrome did not become ready: %w", err)
	}

	// Get browser WS URL and existing tab's target ID via HTTP API
	browserWSURL, tabTargetID, err := getCDPInfo(port)
	if err != nil {
		return fmt.Errorf("failed to get CDP info: %w", err)
	}

	// Connect to browser and attach to the EXISTING tab (no new tab created)
	allocCtx, _ := chromedp.NewRemoteAllocator(context.Background(), browserWSURL)
	// NOTE: we intentionally do NOT defer cancel() so the tab stays open
	tabCtx, _ := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(tabTargetID)))

	// Timeout for the login flow
	ctx, timeoutCancel := context.WithTimeout(tabCtx, 60*time.Second)
	defer timeoutCancel()

	// Step 1: Wait for the login form to be fully rendered.
	// The page redirects and may take time to load the form.
	fmt.Println("Waiting for login form...")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`input[name="username"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("login form did not appear: %w", err)
	}

	var currentURL string
	_ = chromedp.Run(ctx, chromedp.Location(&currentURL))
	fmt.Printf("Current page: %s\n", currentURL)

	// Step 2: Fill credentials using JavaScript for maximum compatibility
	fmt.Println("Filling credentials...")
	fillScript := fmt.Sprintf(`
		(function() {
			var user = document.querySelector('input[name="username"]')
				|| document.querySelector('input[type="text"]')
				|| document.querySelector('input[id*="user"]');
			var pass = document.querySelector('input[name="password"]')
				|| document.querySelector('input[type="password"]');

			if (!user) return "error:username_not_found";
			if (!pass) return "error:password_not_found";

			user.value = "%s";
			user.dispatchEvent(new Event('input', {bubbles: true}));
			user.dispatchEvent(new Event('change', {bubbles: true}));

			pass.value = "%s";
			pass.dispatchEvent(new Event('input', {bubbles: true}));
			pass.dispatchEvent(new Event('change', {bubbles: true}));

			return "filled:" + user.name + "," + pass.name;
		})()
	`, escapeJS(cfg.User), escapeJS(cfg.Password))

	var fillResult string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fillScript, &fillResult),
	); err != nil {
		return fmt.Errorf("failed to fill login form: %w", err)
	}
	fmt.Printf("Fill result: %s\n", fillResult)

	if strings.HasPrefix(fillResult, "error:") {
		return fmt.Errorf("login form issue: %s", fillResult)
	}

	// Step 3: Submit the login form
	fmt.Println("Submitting credentials...")
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				var btn = document.querySelector('button[type="submit"]');
				if (!btn) btn = document.querySelector('input[type="submit"]');
				if (!btn) btn = document.querySelector('.btn-primary');
				if (!btn) btn = document.querySelector('button');
				if (btn) { btn.click(); return "clicked:" + btn.textContent.trim(); }
				var form = document.querySelector('form');
				if (form) { form.submit(); return "submitted"; }
				return "not_found";
			})()
		`, nil),
	); err != nil {
		return fmt.Errorf("failed to submit login form: %w", err)
	}

	// Step 4: Wait for TOTP page
	fmt.Println("Waiting for TOTP page...")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`input[name="totp[]"]`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		fmt.Println("TOTP form not detected, checking alternative OTP form...")
		var hasTokenOTP bool
		_ = chromedp.Run(ctx,
			chromedp.Evaluate(`!!document.querySelector('input[name="token_otp"]')`, &hasTokenOTP),
		)
		if hasTokenOTP {
			return submitTokenOTP(ctx, cfg)
		}
		fmt.Println("No TOTP form found. Checking if already logged in...")
		_ = chromedp.Run(ctx, chromedp.Location(&currentURL))
		if strings.Contains(currentURL, "dashboard") || strings.Contains(currentURL, "desktop") {
			fmt.Println("Login successful! Browser is ready.")
			return nil
		}
		return fmt.Errorf("unexpected page after login — TOTP form not found (URL: %s)", currentURL)
	}

	// Step 5: Generate and fill TOTP
	fmt.Println("Generating TOTP code...")
	totpCode, err := auth.GenerateTOTP(cfg.MFAToken)
	if err != nil {
		return fmt.Errorf("failed to generate TOTP: %w", err)
	}

	fmt.Println("Filling TOTP code...")
	totpScript := fmt.Sprintf(`
		(function() {
			var inputs = document.querySelectorAll('input[name="totp[]"]');
			var code = "%s";
			for (var i = 0; i < inputs.length && i < code.length; i++) {
				inputs[i].value = code[i];
				inputs[i].dispatchEvent(new Event('input', {bubbles: true}));
				inputs[i].dispatchEvent(new Event('change', {bubbles: true}));
			}
			return inputs.length;
		})()
	`, totpCode)

	if err := chromedp.Run(ctx,
		chromedp.Evaluate(totpScript, nil),
	); err != nil {
		return fmt.Errorf("failed to fill TOTP: %w", err)
	}

	// Step 6: Submit TOTP form
	fmt.Println("Submitting TOTP...")
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				var btn = document.querySelector('button[type="submit"]');
				if (!btn) btn = document.querySelector('input[type="submit"]');
				if (btn) { btn.click(); return "clicked"; }
				var form = document.querySelector('form');
				if (form) { form.submit(); return "submitted"; }
				return "not_found";
			})()
		`, nil),
	); err != nil {
		return fmt.Errorf("failed to submit TOTP: %w", err)
	}

	// Step 7: Wait for dashboard to load
	fmt.Println("Waiting for dashboard...")
	if err := chromedp.Run(ctx,
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return fmt.Errorf("error waiting for dashboard: %w", err)
	}

	// Verify we reached the dashboard
	var finalURL string
	_ = chromedp.Run(ctx, chromedp.Location(&finalURL))

	if strings.Contains(finalURL, "/error/") {
		return fmt.Errorf("login failed — redirected to error page: %s", finalURL)
	}

	var stillOnTOTP bool
	_ = chromedp.Run(ctx,
		chromedp.Evaluate(`!!document.querySelector('input[name="totp[]"]')`, &stillOnTOTP),
	)
	if stillOnTOTP {
		return fmt.Errorf("TOTP verification failed — invalid code")
	}

	fmt.Println("Login successful! Browser is ready.")
	fmt.Println("You can now use senhasegura in the browser.")
	// We intentionally do NOT cancel the chromedp contexts here.
	// This ensures the tab stays open. Chrome was launched as a separate
	// process, so it continues running after our program exits.
	return nil
}

// submitTokenOTP handles the alternative token_otp form.
func submitTokenOTP(ctx context.Context, cfg *config.Config) error {
	totpCode, err := auth.GenerateTOTP(cfg.MFAToken)
	if err != nil {
		return fmt.Errorf("failed to generate TOTP: %w", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.SetValue(`input[name="token_otp"]`, totpCode, chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				var btn = document.querySelector('button[type="submit"]');
				if (!btn) btn = document.querySelector('input[type="submit"]');
				if (btn) { btn.click(); return "clicked"; }
				var form = document.querySelector('form');
				if (form) { form.submit(); return "submitted"; }
				return "not_found";
			})()
		`, nil),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return fmt.Errorf("failed to submit OTP: %w", err)
	}

	fmt.Println("Login successful! Browser is ready.")
	return nil
}

// getCDPInfo queries Chrome's DevTools HTTP API to get the browser WebSocket
// URL and the target ID of the first page tab.
func getCDPInfo(port int) (browserWSURL string, tabTargetID string, err error) {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Retry a few times — Chrome may not have created the page target yet
	for i := 0; i < 10; i++ {
		// Get browser WebSocket URL
		resp, err := http.Get(base + "/json/version")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var version struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		json.NewDecoder(resp.Body).Decode(&version)
		resp.Body.Close()
		browserWSURL = version.WebSocketDebuggerURL

		if browserWSURL == "" {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Get list of targets to find the first page tab
		resp, err = http.Get(base + "/json")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var targets []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		json.NewDecoder(resp.Body).Decode(&targets)
		resp.Body.Close()

		for _, t := range targets {
			if t.Type == "page" {
				return browserWSURL, t.ID, nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return "", "", fmt.Errorf("could not find a page target via CDP on port %d", port)
}

// findChrome locates the Chrome binary on the system.
func findChrome() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		paths := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			os.Getenv("HOME") + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	case "linux":
		names := []string{"google-chrome", "google-chrome-stable", "chromium-browser", "chromium"}
		for _, name := range names {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
	case "windows":
		paths := []string{
			os.Getenv("PROGRAMFILES") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("PROGRAMFILES(X86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LOCALAPPDATA") + `\Google\Chrome\Application\chrome.exe`,
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("Chrome not found. Please install Google Chrome or set it in your PATH")
}

// findFreePort finds an available TCP port.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// escapeJS escapes a string for safe embedding in JavaScript string literals.
func escapeJS(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`'`, `\'`,
		"\n", `\n`,
		"\r", `\r`,
		"`", "\\`",
	)
	return r.Replace(s)
}

// waitForCDP waits for Chrome's CDP endpoint to become available.
func waitForCDP(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			time.Sleep(500 * time.Millisecond)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for Chrome CDP on port %d", port)
}
