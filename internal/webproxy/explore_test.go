package webproxy

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestExploreCredentialAPI is an exploration test that logs in to the senhasegura
// web interface and probes for credential/password retrieval endpoints.
// It dumps data-actions JSON from the dashboard, tries known API paths,
// and searches the HTML for password-related UI elements.
//
// Run with: go test -v -run TestExploreCredentialAPI ./internal/webproxy/
func TestExploreCredentialAPI(t *testing.T) {
	cfg := mkCfg()

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Log("=== Step 1: Login ===")
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	t.Log("Login successful")

	// ------------------------------------------------------------------
	// Step 2: Fetch the dashboard and dump ALL data-actions content
	// ------------------------------------------------------------------
	t.Log("=== Step 2: Fetch dashboard and extract data-actions ===")
	dashHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	t.Logf("Dashboard HTML length: %d bytes", len(dashHTML))

	// Extract ALL data-actions attributes from the entire page
	dataActionsRe := regexp.MustCompile(`data-actions\s*=\s*'([^']*)'`)
	dataActionsMatches := dataActionsRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d data-actions attributes (single-quoted)", len(dataActionsMatches))
	for i, m := range dataActionsMatches {
		t.Logf("  data-actions[%d]: %s", i, m[1])
	}

	// Also try double-quoted data-actions
	dataActionsRe2 := regexp.MustCompile(`data-actions\s*=\s*"([^"]*)"`)
	dataActionsMatches2 := dataActionsRe2.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d data-actions attributes (double-quoted)", len(dataActionsMatches2))
	for i, m := range dataActionsMatches2 {
		// HTML-encoded JSON may use &quot; — unescape it
		decoded := strings.ReplaceAll(m[1], "&quot;", `"`)
		decoded = strings.ReplaceAll(decoded, "&amp;", "&")
		decoded = strings.ReplaceAll(decoded, "&#039;", "'")
		decoded = strings.ReplaceAll(decoded, "&lt;", "<")
		decoded = strings.ReplaceAll(decoded, "&gt;", ">")
		t.Logf("  data-actions[%d] (decoded): %s", i, decoded)
	}

	// Also search for data-action (singular) attributes
	dataActionRe := regexp.MustCompile(`data-action\s*=\s*["']([^"']*)["']`)
	dataActionMatches := dataActionRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d data-action (singular) attributes", len(dataActionMatches))
	for i, m := range dataActionMatches {
		t.Logf("  data-action[%d]: %s", i, m[1])
	}

	// Search for any data-* attributes that might contain credential IDs or actions
	dataAttrRe := regexp.MustCompile(`data-([a-zA-Z_-]+)\s*=\s*["']([^"']{1,500})["']`)
	dataAttrMatches := dataAttrRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d data-* attributes total", len(dataAttrMatches))
	// Log only interesting ones (not common CSS/UI ones)
	skipAttrs := map[string]bool{
		"toggle": true, "target": true, "dismiss": true, "backdrop": true,
		"bs-toggle": true, "bs-target": true, "bs-dismiss": true,
	}
	for _, m := range dataAttrMatches {
		attrName := m[1]
		attrValue := m[2]
		if skipAttrs[attrName] {
			continue
		}
		// Log attributes that look credential/action related
		lower := strings.ToLower(attrName + attrValue)
		if strings.Contains(lower, "cred") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "senha") ||
			strings.Contains(lower, "action") ||
			strings.Contains(lower, "session") ||
			strings.Contains(lower, "copy") ||
			strings.Contains(lower, "view") ||
			strings.Contains(lower, "id") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "sr") {
			t.Logf("  data-%s = %s", attrName, attrValue)
		}
	}

	// ------------------------------------------------------------------
	// Step 3: Search for credential IDs in the page
	// ------------------------------------------------------------------
	t.Log("=== Step 3: Search for credential IDs and action URLs ===")

	// Look for any URL patterns that reference credential IDs
	credIDRe := regexp.MustCompile(`(?i)credential[/=](\d+)`)
	credIDMatches := credIDRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d credential ID references", len(credIDMatches))
	for i, m := range credIDMatches {
		t.Logf("  credential ID[%d]: %s (full match: %s)", i, m[1], m[0])
	}

	// Look for any URLs with numeric IDs that could be credential IDs
	numericIDURLRe := regexp.MustCompile(`/flow/[a-z]+/[a-z]+/(\d+)`)
	numericIDMatches := numericIDURLRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d /flow/*/ID patterns", len(numericIDMatches))
	seen := make(map[string]bool)
	for _, m := range numericIDMatches {
		if !seen[m[0]] {
			seen[m[0]] = true
			t.Logf("  URL: %s", m[0])
		}
	}

	// Look for onclick handlers or JavaScript calls
	onclickRe := regexp.MustCompile(`onclick\s*=\s*["']([^"']{1,500})["']`)
	onclickMatches := onclickRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d onclick handlers", len(onclickMatches))
	for i, m := range onclickMatches {
		t.Logf("  onclick[%d]: %s", i, m[1])
	}

	// ------------------------------------------------------------------
	// Step 4: Try senhasegura API endpoints with session cookies
	// ------------------------------------------------------------------
	t.Log("=== Step 4: Probe API endpoints ===")

	apiEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/iso/pam/credential"},
		{"GET", "/iso/pam/credential/list"},
		{"GET", "/api/pam/credential"},
		{"GET", "/flow/coge/credential/list"},
		{"GET", "/iso/pam/credential/1"},
		{"GET", "/iso/pam/credential/password/1"},
		{"GET", "/api/pam/credential/1"},
		{"GET", "/api/pam/credential/password"},
		{"GET", "/flow/coge/credential/view"},
		{"GET", "/flow/coge/credential/password"},
		{"GET", "/flow/pam/credential"},
		{"GET", "/flow/pam/credential/list"},
		{"GET", "/iso/coge/password"},
		{"GET", "/iso/coge/credential"},
		{"GET", "/flow/coge/desktop/credential"},
		// REST API with OAuth-style paths
		{"GET", "/iso/pam/list/credential"},
		{"GET", "/iso/pam/list/credentials"},
		// Try paths that might list or view credentials
		{"GET", "/flow/coge/credential"},
		{"GET", "/flow/coge/view/credential"},
		{"GET", "/flow/coge/action/view"},
		{"GET", "/flow/coge/action/password"},
		{"GET", "/flow/coac/credential"},
		{"GET", "/flow/coac/credential/list"},
		// senhasegura v3.x API paths
		{"GET", "/api/pam/credential/list"},
		{"GET", "/api/pam/credentials"},
	}

	for _, ep := range apiEndpoints {
		fullURL := client.baseURL + ep.path
		t.Logf("--- %s %s ---", ep.method, ep.path)

		var resp *http.Response
		var reqErr error

		switch ep.method {
		case "GET":
			resp, reqErr = client.http.Get(fullURL)
		}

		if reqErr != nil {
			t.Logf("  Error: %v", reqErr)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		bodyStr := string(body)
		t.Logf("  Status: %d", resp.StatusCode)
		t.Logf("  Content-Type: %s", resp.Header.Get("Content-Type"))

		// If there was a redirect, log the Location header
		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
			t.Logf("  Location: %s", resp.Header.Get("Location"))
		}

		// Log first 500 chars of body
		snippet := bodyStr
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		t.Logf("  Body (first 500): %s", snippet)
	}

	// ------------------------------------------------------------------
	// Step 5: Search dashboard HTML for password/view/copy UI elements
	// ------------------------------------------------------------------
	t.Log("=== Step 5: Search for password-related UI elements ===")

	keywords := []string{
		"password", "senha", "view", "copy", "clipboard",
		"show", "reveal", "eye", "visibility", "unlock",
		"key", "secret", "credential-password",
	}

	lowerDash := strings.ToLower(dashHTML)
	for _, kw := range keywords {
		indices := allIndices(lowerDash, kw)
		t.Logf("Keyword '%s': %d occurrences", kw, len(indices))
		for i, idx := range indices {
			if i >= 5 {
				t.Logf("  ... and %d more", len(indices)-5)
				break
			}
			// Show surrounding context (200 chars before and after)
			start := idx - 200
			if start < 0 {
				start = 0
			}
			end := idx + len(kw) + 200
			if end > len(dashHTML) {
				end = len(dashHTML)
			}
			t.Logf("  [%d] ...%s...", i, dashHTML[start:end])
		}
	}

	// ------------------------------------------------------------------
	// Step 6: Search for buttons, links, and icons related to credentials
	// ------------------------------------------------------------------
	t.Log("=== Step 6: Search for action buttons/links ===")

	// Find all <a> tags and log those that look credential-related
	aTagRe := regexp.MustCompile(`<a\s[^>]*>`)
	aTags := aTagRe.FindAllString(dashHTML, -1)
	t.Logf("Found %d <a> tags total", len(aTags))
	for _, tag := range aTags {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, "password") ||
			strings.Contains(lower, "senha") ||
			strings.Contains(lower, "copy") ||
			strings.Contains(lower, "clipboard") ||
			strings.Contains(lower, "view") ||
			strings.Contains(lower, "credential") ||
			strings.Contains(lower, "eye") {
			t.Logf("  Interesting <a>: %s", tag)
		}
	}

	// Find all <button> tags
	btnRe := regexp.MustCompile(`<button\s[^>]*>[^<]*</button>`)
	btnMatches := btnRe.FindAllString(dashHTML, -1)
	t.Logf("Found %d <button> tags", len(btnMatches))
	for _, btn := range btnMatches {
		lower := strings.ToLower(btn)
		if strings.Contains(lower, "password") ||
			strings.Contains(lower, "senha") ||
			strings.Contains(lower, "copy") ||
			strings.Contains(lower, "view") ||
			strings.Contains(lower, "credential") {
			t.Logf("  Interesting <button>: %s", btn)
		}
	}

	// Find <i> icons (Font Awesome or similar) that might indicate view/copy actions
	iconRe := regexp.MustCompile(`<i\s+class="[^"]*(?:fa-eye|fa-copy|fa-clipboard|fa-key|fa-lock|fa-unlock|fa-password)[^"]*"[^>]*>`)
	iconMatches := iconRe.FindAllString(dashHTML, -1)
	t.Logf("Found %d password/view/copy icons", len(iconMatches))
	for _, icon := range iconMatches {
		t.Logf("  Icon: %s", icon)
	}

	// ------------------------------------------------------------------
	// Step 7: Dump all <script> blocks that mention credential or password
	// ------------------------------------------------------------------
	t.Log("=== Step 7: JavaScript containing credential/password references ===")

	scriptRe := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	scriptMatches := scriptRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d <script> blocks total", len(scriptMatches))
	for i, sm := range scriptMatches {
		scriptContent := sm[1]
		lower := strings.ToLower(scriptContent)
		if strings.Contains(lower, "password") ||
			strings.Contains(lower, "credential") ||
			strings.Contains(lower, "senha") ||
			strings.Contains(lower, "clipboard") ||
			strings.Contains(lower, "copy") {
			snippet := scriptContent
			if len(snippet) > 1000 {
				snippet = snippet[:1000] + "...(truncated)"
			}
			t.Logf("  Script[%d] (relevant): %s", i, snippet)
		}
	}

	// ------------------------------------------------------------------
	// Step 8: Try fetching the credential list page directly
	// ------------------------------------------------------------------
	t.Log("=== Step 8: Try credential list pages ===")

	listPages := []string{
		"/flow/coge/credential/list",
		"/flow/pam/credential/list",
		"/flow/coge/credential",
		"/flow/pam/credential",
		"/flow/coac/credential",
		"/flow/coge/desktop/credential",
	}

	for _, page := range listPages {
		t.Logf("--- Fetching %s ---", page)
		html, err := client.GetPage(page)
		if err != nil {
			t.Logf("  Error: %v", err)
			continue
		}
		t.Logf("  Length: %d bytes", len(html))

		// Check if it's a real page or redirect/error
		if strings.Contains(html, "data-actions") {
			t.Log("  Contains data-actions!")
			// Extract data-actions from this page too
			daMatches := dataActionsRe.FindAllStringSubmatch(html, -1)
			for j, m := range daMatches {
				t.Logf("  data-actions[%d]: %s", j, m[1])
			}
			daMatches2 := dataActionsRe2.FindAllStringSubmatch(html, -1)
			for j, m := range daMatches2 {
				decoded := strings.ReplaceAll(m[1], "&quot;", `"`)
				decoded = strings.ReplaceAll(decoded, "&amp;", "&")
				t.Logf("  data-actions[%d] (double-quoted, decoded): %s", j, decoded)
			}
		}

		// Search for password-related elements
		lowerHTML := strings.ToLower(html)
		if strings.Contains(lowerHTML, "password") || strings.Contains(lowerHTML, "senha") {
			t.Log("  Contains password/senha references!")
			// Find context around first occurrence
			pwIdx := strings.Index(lowerHTML, "password")
			if pwIdx < 0 {
				pwIdx = strings.Index(lowerHTML, "senha")
			}
			if pwIdx >= 0 {
				start := pwIdx - 300
				if start < 0 {
					start = 0
				}
				end := pwIdx + 300
				if end > len(html) {
					end = len(html)
				}
				t.Logf("  Context: %s", html[start:end])
			}
		}

		// Log first 500 chars
		snippet := html
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		t.Logf("  First 500 chars: %s", snippet)
	}

	// ------------------------------------------------------------------
	// Step 9: Try the credential list with _sr token
	// ------------------------------------------------------------------
	t.Log("=== Step 9: Try credential pages with _sr token from dashboard ===")

	// Extract _sr tokens from dashboard
	srRe := regexp.MustCompile(`_sr=([^'"&\s]+)`)
	srMatches := srRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d _sr tokens in dashboard", len(srMatches))
	seenSR := make(map[string]bool)
	var uniqueSRTokens []string
	for _, m := range srMatches {
		if !seenSR[m[1]] {
			seenSR[m[1]] = true
			uniqueSRTokens = append(uniqueSRTokens, m[1])
			t.Logf("  _sr token: %s", m[1][:min(40, len(m[1]))])
		}
	}

	// ------------------------------------------------------------------
	// Step 10: Dump all unique href values from the dashboard
	// ------------------------------------------------------------------
	t.Log("=== Step 10: All unique hrefs from dashboard ===")

	hrefRe := regexp.MustCompile(`href="([^"]+)"`)
	hrefMatches := hrefRe.FindAllStringSubmatch(dashHTML, -1)
	seenHref := make(map[string]bool)
	for _, m := range hrefMatches {
		href := m[1]
		if seenHref[href] {
			continue
		}
		seenHref[href] = true
		// Only log internal paths (not static assets)
		if strings.HasPrefix(href, "/flow") || strings.HasPrefix(href, "/iso") || strings.HasPrefix(href, "/api") {
			t.Logf("  href: %s", href)
		}
	}

	// Also log form actions
	formActionRe := regexp.MustCompile(`<form[^>]*action="([^"]+)"`)
	formActions := formActionRe.FindAllStringSubmatch(dashHTML, -1)
	t.Logf("Found %d form actions", len(formActions))
	for _, m := range formActions {
		t.Logf("  form action: %s", m[1])
	}

	t.Log("=== Exploration complete ===")
}

// allIndices returns all starting indices of substr in s.
func allIndices(s, substr string) []int {
	var indices []int
	start := 0
	for {
		idx := strings.Index(s[start:], substr)
		if idx < 0 {
			break
		}
		indices = append(indices, start+idx)
		start += idx + len(substr)
	}
	return indices
}

// TestExploreCredentialPassword is a focused test that attempts to find
// the password viewing mechanism in senhasegura.
// It tries various approaches:
// 1. Direct credential detail pages
// 2. Action links found in data-actions JSON
// 3. AJAX endpoints commonly used in senhasegura
func TestExploreCredentialPassword(t *testing.T) {
	cfg := mkCfg()

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	t.Log("Login successful")

	// Fetch dashboard
	dashHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	creds := ParseCredentials(dashHTML)
	t.Logf("Found %d credentials", len(creds))
	for i, c := range creds {
		t.Logf("  [%d] %s@%s (%s) sr=%s", i, c.Username, c.IP, c.Device, c.SRToken[:min(30, len(c.SRToken))])
	}

	if len(creds) == 0 {
		t.Fatal("No credentials found")
	}

	// ------------------------------------------------------------------
	// Try senhasegura known password view endpoints
	// ------------------------------------------------------------------
	t.Log("=== Trying password view endpoints ===")

	// senhasegura commonly uses these patterns for password viewing
	passwordEndpoints := []string{
		// Orbini-style endpoints
		"/flow/coge/action/view/password?_sr=%s",
		"/flow/coac/action/view/password?_sr=%s",
		"/flow/coge/action/password/view?_sr=%s",
		"/flow/coge/action/clipboard?_sr=%s",
		"/flow/coge/action/copy/password?_sr=%s",
		// Direct credential endpoints
		"/flow/coge/credential/password?_sr=%s",
		"/flow/coge/credential/view/password?_sr=%s",
	}

	// Try each endpoint with the first credential's SR token
	for _, ep := range passwordEndpoints {
		path := fmt.Sprintf(ep, creds[0].SRToken)
		t.Logf("--- GET %s ---", path)
		html, err := client.GetPage(path)
		if err != nil {
			t.Logf("  Error: %v", err)
			continue
		}
		snippet := html
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		t.Logf("  Length: %d, Body: %s", len(html), snippet)
	}

	// ------------------------------------------------------------------
	// Search for XHR/AJAX password endpoints in JS
	// ------------------------------------------------------------------
	t.Log("=== Searching JS for AJAX password endpoints ===")

	// Fetch common JS files that might contain password viewing logic
	jsPages := []string{
		"/flow/coge/desktop/dashboard",  // re-scan for inline JS
	}

	for _, page := range jsPages {
		html, err := client.GetPage(page)
		if err != nil {
			continue
		}

		// Search for AJAX calls or fetch() that might reveal password endpoints
		ajaxRe := regexp.MustCompile(`(?i)(?:ajax|fetch|xmlhttp|\.get|\.post)\s*\([^)]*(?:password|credential|senha)[^)]*\)`)
		ajaxMatches := ajaxRe.FindAllString(html, -1)
		t.Logf("AJAX password calls found: %d", len(ajaxMatches))
		for _, m := range ajaxMatches {
			t.Logf("  AJAX: %s", m)
		}

		// Search for URL patterns in JS
		urlPatternRe := regexp.MustCompile(`['"](/[a-zA-Z/]+(?:password|credential|senha|view|copy)[a-zA-Z/]*)['"]`)
		urlMatches := urlPatternRe.FindAllStringSubmatch(html, -1)
		t.Logf("URL patterns containing password/credential/view/copy: %d", len(urlMatches))
		seenURL := make(map[string]bool)
		for _, m := range urlMatches {
			if !seenURL[m[1]] {
				seenURL[m[1]] = true
				t.Logf("  URL pattern: %s", m[1])
			}
		}
	}

	t.Log("=== Password exploration complete ===")
}
