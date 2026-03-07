package webproxy

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"segura-cli/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	url := os.Getenv("SEGURA_URL")
	user := os.Getenv("SEGURA_USER")
	pass := os.Getenv("SEGURA_PASSWORD")
	mfa := os.Getenv("SEGURA_MFA_TOKEN")
	if url == "" || user == "" || pass == "" || mfa == "" {
		t.Skip("SEGURA_URL, SEGURA_USER, SEGURA_PASSWORD, SEGURA_MFA_TOKEN must be set")
	}
	parsed, _ := config.Load("")
	return parsed
}

func TestFullLogin(t *testing.T) {
	cfg := testConfig(t)

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Log("Logging in...")
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	t.Log("Login successful!")

	// Test: fetch dashboard
	t.Log("Fetching dashboard...")
	dashHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	t.Logf("Dashboard length: %d", len(dashHTML))
	t.Logf("Has credentials: %v", strings.Contains(strings.ToLower(dashHTML), "credential"))

	// Parse credentials
	creds := ParseCredentials(dashHTML)
	t.Logf("Found %d credentials", len(creds))
	for _, c := range creds {
		t.Logf("  %s@%s (%s) sr=%s...", c.Username, c.IP, c.Device, c.SRToken[:min(20, len(c.SRToken))])
	}

	// Test proxy URL for first credential
	if len(creds) > 0 {
		c := creds[0]
		t.Logf("Getting proxy URL for %s@%s...", c.Username, c.IP)
		proxyURL, err := client.GetProxyURL(c.SRToken)
		if err != nil {
			t.Logf("GetProxyURL error: %v", err)
		} else {
			t.Logf("Proxy URL: %s...", proxyURL[:min(100, len(proxyURL))])
		}
	}
}

func TestLoginStepByStep(t *testing.T) {
	cfg := testConfig(t)

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Step 1: GET /
	resp, err := client.http.Get(client.baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	srRe := regexp.MustCompile(`_sr=([^'"&\s]+)`)
	srMatch := srRe.FindStringSubmatch(string(body))
	if len(srMatch) < 2 {
		t.Fatalf("No _sr token")
	}
	t.Logf("Step 1: Got _sr token (%d chars)", len(srMatch[1]))

	// Step 2: GET signin page
	signinURL := client.baseURL + "/flow/auth/signin?_sr=" + srMatch[1]
	resp, err = client.http.Get(signinURL)
	if err != nil {
		t.Fatalf("GET signin: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	csrfName, csrfValue := extractOrbiniCSRF(string(body))
	t.Logf("Step 2: CSRF = %s", csrfName)

	// Step 3: POST credentials
	form := make(map[string][]string)
	form["username"] = []string{cfg.User}
	form["password"] = []string{cfg.Password}
	if csrfName != "" {
		form[csrfName] = []string{csrfValue}
	}

	resp, err = client.http.PostForm(signinURL, form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("Step 3: Status %d, Location: %s", resp.StatusCode, resp.Header.Get("Location"))

	// Step 4: GET TOTP page
	totpURL := resp.Header.Get("Location")
	if !strings.HasPrefix(totpURL, "http") {
		totpURL = client.baseURL + totpURL
	}
	resp, err = client.http.Get(totpURL)
	if err != nil {
		t.Fatalf("GET TOTP: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	bodyStr := string(body)
	csrfName2, _ := extractOrbiniCSRF(bodyStr)
	t.Logf("Step 4: TOTP page, CSRF = %s, has totp[]=%v", csrfName2, strings.Contains(bodyStr, "totp[]"))

	// Step 5: POST TOTP with individual digits
	totpCode := "123456" // Placeholder - use real code
	_ = totpCode
	t.Logf("Step 4 complete: TOTP page obtained with %d input fields", strings.Count(bodyStr, "totp[]"))
}

func TestDashboardAnalysis(t *testing.T) {
	cfg := testConfig(t)

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	dashHTML, err := client.GetPage("/")
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}

	// Find all action/session links
	re := regexp.MustCompile(`action/session[^"]*`)
	matches := re.FindAllString(dashHTML, -1)
	t.Logf("Found %d action/session links", len(matches))
	for i, m := range matches {
		if i < 5 {
			t.Logf("  Link: %s", m[:min(80, len(m))])
		}
	}

	// Find the context around the first link
	idx := strings.Index(dashHTML, "action/session")
	if idx > 0 {
		start := idx - 500
		if start < 0 {
			start = 0
		}
		end := idx + 200
		if end > len(dashHTML) {
			end = len(dashHTML)
		}
		t.Logf("Context around first link:\n%s", dashHTML[start:end])
	}

	// Find credential-related content
	credIdx := strings.Index(strings.ToLower(dashHTML), "last used credentials")
	if credIdx > 0 {
		end := credIdx + 2000
		if end > len(dashHTML) {
			end = len(dashHTML)
		}
		t.Logf("Credentials section:\n%s", dashHTML[credIdx:end])
	} else {
		t.Log("'Last used credentials' NOT found in dashboard")
		// Log first 2000 chars of dashboard
		snippet := dashHTML
		if len(snippet) > 2000 {
			snippet = snippet[:2000]
		}
		t.Logf("Dashboard start:\n%s", snippet)
	}

	// Analyze the structure around each action/session link in dashboard2
	t.Log("Trying explicit dashboard URL...")
	dashHTML2, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		t.Logf("Dashboard error: %v", err)
	} else {
		t.Logf("Dashboard2 length: %d", len(dashHTML2))
		if strings.Contains(dashHTML2, "action/session") {
			t.Log("Dashboard2 HAS action/session links!")
			re2 := regexp.MustCompile(`action/session[^"]*`)
			matches2 := re2.FindAllString(dashHTML2, -1)
			t.Logf("Found %d links", len(matches2))
		}
		// Show wide context around the first action/session link
		re3 := regexp.MustCompile(`action/session/default\?_sr=`)
		firstLoc := re3.FindStringIndex(dashHTML2)
		if firstLoc != nil {
			start := firstLoc[0] - 1500
			if start < 0 {
				start = 0
			}
			end := firstLoc[1] + 200
			if end > len(dashHTML2) {
				end = len(dashHTML2)
			}
			t.Logf("Wide context around first link:\n%s\n---", dashHTML2[start:end])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
