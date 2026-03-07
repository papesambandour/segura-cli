package webproxy

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"segura-cli/internal/auth"
	"segura-cli/internal/config"
)

// GuacSession holds the tokens needed to establish a Guacamole WebSocket connection.
type GuacSession struct {
	Tenant    string
	OASToken  string
	AuthToken string
	Host      string
	BaseURL   string
	Jar       http.CookieJar
}

// Client manages authenticated HTTP sessions with the senhasegura web UI.
type Client struct {
	cfg     *config.Config
	http    *http.Client
	baseURL string
}

// NewClient creates a new senhasegura web client.
func NewClient(cfg *config.Config) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	baseURL := strings.TrimRight(cfg.URL, "/")

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Jar: jar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: baseURL,
	}, nil
}

// Login authenticates to senhasegura web UI (username + password + TOTP).
// Flow: GET / → GET /flow/auth/signin → POST credentials → POST TOTP
func (c *Client) Login() error {
	// Step 1: GET main page to obtain the _sr state token
	resp, err := c.http.Get(c.baseURL + "/")
	if err != nil {
		return fmt.Errorf("failed to access senhasegura: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	srRe := regexp.MustCompile(`_sr=([^'"&\s]+)`)
	srMatch := srRe.FindStringSubmatch(string(body))
	if len(srMatch) < 2 {
		return fmt.Errorf("failed to extract authentication token from login page")
	}
	srToken := srMatch[1]

	// Step 2: GET the signin form
	signinURL := c.baseURL + "/flow/auth/signin?_sr=" + srToken
	resp, err = c.http.Get(signinURL)
	if err != nil {
		return fmt.Errorf("failed to access signin page: %w", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	bodyStr := string(body)

	// Follow any redirects
	for resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "http") {
			loc = c.baseURL + loc
		}
		resp, err = c.http.Get(loc)
		if err != nil {
			return fmt.Errorf("failed to follow redirect: %w", err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr = string(body)
	}

	// Extract CSRF token
	csrfName, csrfValue := extractOrbiniCSRF(bodyStr)

	// Step 3: POST username + password
	form := url.Values{}
	form.Set("username", c.cfg.User)
	form.Set("password", c.cfg.Password)
	if csrfName != "" {
		form.Set(csrfName, csrfValue)
	}

	postURL := signinURL
	if resp.Request != nil && resp.Request.URL != nil {
		postURL = resp.Request.URL.String()
	}

	resp, err = c.http.PostForm(postURL, form)
	if err != nil {
		return fmt.Errorf("failed to submit credentials: %w", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	// Check for redirect to TOTP page
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		location := resp.Header.Get("Location")
		if strings.Contains(location, "twofactor") || strings.Contains(location, "otp") || strings.Contains(location, "mfa") {
			return c.submitTOTP(location)
		}
		// Login succeeded without TOTP
		c.followRedirect(location)
		return nil
	}

	// Check if the response already contains a TOTP form
	bodyStr = string(body)
	if strings.Contains(bodyStr, "totp[]") || strings.Contains(bodyStr, "token_otp") {
		return c.submitTOTPFromPage(bodyStr, postURL)
	}

	// Still on login page means credentials were rejected
	if strings.Contains(bodyStr, "kt_sign_in_form") {
		return fmt.Errorf("login failed: invalid credentials")
	}

	return nil
}

// submitTOTP handles TOTP verification after credential login.
func (c *Client) submitTOTP(redirectURL string) error {
	if !strings.HasPrefix(redirectURL, "http") {
		redirectURL = c.baseURL + redirectURL
	}

	// GET the TOTP page
	resp, err := c.http.Get(redirectURL)
	if err != nil {
		return fmt.Errorf("failed to access TOTP page: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Follow any redirects
	finalURL := redirectURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	for resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "http") {
			loc = c.baseURL + loc
		}
		finalURL = loc
		resp, err = c.http.Get(loc)
		if err != nil {
			return fmt.Errorf("failed to follow TOTP redirect: %w", err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	return c.submitTOTPFromPage(string(body), finalURL)
}

// submitTOTPFromPage submits the TOTP code from the given page content.
func (c *Client) submitTOTPFromPage(pageBody, pageURL string) error {
	totpCode, err := auth.GenerateTOTP(c.cfg.MFAToken)
	if err != nil {
		return fmt.Errorf("failed to generate TOTP: %w", err)
	}

	csrfName, csrfValue := extractOrbiniCSRF(pageBody)

	// Build form data based on the form type
	form := url.Values{}
	if csrfName != "" {
		form.Set(csrfName, csrfValue)
	}

	// senhasegura uses totp[] fields — 6 separate single-digit inputs
	if strings.Contains(pageBody, "totp[]") {
		for _, digit := range totpCode {
			form.Add("totp[]", string(digit))
		}
	} else {
		// Fallback for other OTP form formats
		form.Set("token_otp", totpCode)
	}

	// Determine POST URL
	formAction := extractFormAction(pageBody)
	postURL := pageURL
	if formAction != "" {
		if strings.HasPrefix(formAction, "http") {
			postURL = formAction
		} else {
			postURL = c.baseURL + formAction
		}
	}

	resp, err := c.http.PostForm(postURL, form)
	if err != nil {
		return fmt.Errorf("failed to submit TOTP: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Follow redirect chain after TOTP
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		location := resp.Header.Get("Location")
		if strings.Contains(location, "error") {
			return fmt.Errorf("TOTP verification failed (redirected to error page)")
		}
		c.followRedirectChain(location)
		return nil
	}

	// Check if we ended up on an explicit error page (e.g., /error/500.html)
	bodyStr := string(body)
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL := resp.Request.URL.String()
		if strings.Contains(finalURL, "/error/") {
			return fmt.Errorf("TOTP verification failed (error page: %s)", finalURL)
		}
	}
	// If still on the TOTP form, it means the code was rejected
	if strings.Contains(bodyStr, "totp[]") || strings.Contains(bodyStr, "token_otp") {
		return fmt.Errorf("TOTP verification failed: invalid code")
	}

	return nil
}

// followRedirect follows a single redirect.
func (c *Client) followRedirect(location string) {
	if !strings.HasPrefix(location, "http") {
		location = c.baseURL + location
	}
	resp, err := c.http.Get(location)
	if err != nil {
		return
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
}

// followRedirectChain follows a chain of redirects.
func (c *Client) followRedirectChain(location string) {
	for i := 0; i < 10; i++ {
		if !strings.HasPrefix(location, "http") {
			location = c.baseURL + location
		}
		resp, err := c.http.Get(location)
		if err != nil {
			return
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
			return
		}
		location = resp.Header.Get("Location")
		if location == "" {
			return
		}
	}
}

// GetPage fetches a page from the senhasegura web UI.
func (c *Client) GetPage(path string) (string, error) {
	pageURL := c.baseURL + path
	resp, err := c.http.Get(pageURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", path, err)
	}
	defer resp.Body.Close()

	// Follow redirects for GetPage
	for resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "http") {
			loc = c.baseURL + loc
		}
		resp, err = c.http.Get(loc)
		if err != nil {
			return "", fmt.Errorf("failed to follow redirect: %w", err)
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

// GetProxyURL initiates a proxy session and returns the Guacamole URL.
func (c *Client) GetProxyURL(srToken string) (string, error) {
	sessionURL := c.baseURL + "/flow/coac/action/session/default?_sr=" + srToken

	// Follow the redirect chain to get the final Guacamole URL
	resp, err := c.http.Get(sessionURL)
	if err != nil {
		return "", fmt.Errorf("failed to initiate proxy session: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Follow all redirects to get to the proxy page
	for i := 0; i < 10; i++ {
		if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
			break
		}
		location := resp.Header.Get("Location")
		if location == "" {
			break
		}
		if !strings.HasPrefix(location, "http") {
			location = c.baseURL + location
		}

		// If we hit the proxy URL, return it
		if strings.Contains(location, "/proxy/#/client/") {
			return location, nil
		}

		resp, err = c.http.Get(location)
		if err != nil {
			return "", fmt.Errorf("failed to follow redirect: %w", err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	// Check final URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL := resp.Request.URL.String()
		if strings.Contains(finalURL, "/proxy/#/client/") {
			return finalURL, nil
		}
	}

	return "", fmt.Errorf("failed to obtain proxy URL (status %d)", resp.StatusCode)
}

// BaseURL returns the base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// ExchangeTokens takes a proxy URL and exchanges the OAS token for a GuacSession.
func (c *Client) ExchangeTokens(proxyURL string) (*GuacSession, error) {
	parts := strings.Split(proxyURL, "/client/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid proxy URL format")
	}
	fullPath := strings.TrimRight(parts[1], "/,")
	tokenParts := strings.SplitN(fullPath, "/", 2)
	if len(tokenParts) < 2 {
		return nil, fmt.Errorf("invalid proxy URL token format")
	}
	tenant := tokenParts[0]
	oasToken := strings.TrimRight(tokenParts[1], ",")

	resp, err := c.http.PostForm(c.baseURL+"/proxy/api/tokens", url.Values{"token": {oasToken}})
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var tr map[string]interface{}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	authToken, ok := tr["authToken"].(string)
	if !ok {
		return nil, fmt.Errorf("no auth token in response")
	}

	return &GuacSession{
		Tenant:    tenant,
		OASToken:  oasToken,
		AuthToken: authToken,
		Host:      c.cfg.Host,
		BaseURL:   c.baseURL,
		Jar:       c.http.Jar,
	}, nil
}

// HTTPClient returns the underlying HTTP client (for cookie sharing).
func (c *Client) HTTPClient() *http.Client {
	return c.http
}

// Host returns the configured hostname.
func (c *Client) Host() string {
	return c.cfg.Host
}

// extractOrbiniCSRF extracts the Orbini CSRF token from HTML.
func extractOrbiniCSRF(html string) (name, value string) {
	re := regexp.MustCompile(`name="(orbiniCsrf[^"]*)"[^>]*value="([^"]*)"`)
	m := re.FindStringSubmatch(html)
	if len(m) >= 3 {
		return m[1], m[2]
	}
	return "", ""
}
