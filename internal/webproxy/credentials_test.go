package webproxy

import (
	"testing"

	"github.com/joho/godotenv"

	"segura-cli/internal/config"
)

// mkCfg loads .env (repo root) and returns the config, or nil on failure.
// Shared helper for the live integration tests in this package.
func mkCfg() *config.Config {
	for _, p := range []string{"../../.env", "../.env", ".env"} {
		if err := godotenv.Load(p); err == nil {
			break
		}
	}
	cfg, err := config.Load("")
	if err != nil {
		return nil
	}
	return cfg
}

// TestFetchAllCredentials verifies that FetchAllCredentials walks every dashboard
// page (senhasegura paginates via ?page=N) instead of stopping at the first page.
// Live test — skips unless a working senhasegura .env is available.
//
// Run: go test -v -count=1 -run TestFetchAllCredentials ./internal/webproxy/
func TestFetchAllCredentials(t *testing.T) {
	cfg := mkCfg()
	if cfg == nil {
		t.Skip("no senhasegura .env available")
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Baseline: a single (first) page.
	firstPage := ParseCredentials(mustGet(t, client, "/flow/coge/desktop/dashboard?page=1"))
	all, err := client.FetchAllCredentials()
	if err != nil {
		t.Fatalf("FetchAllCredentials: %v", err)
	}

	t.Logf("page 1 = %d credentials, all pages = %d credentials", len(firstPage), len(all))
	if len(all) < len(firstPage) {
		t.Fatalf("FetchAllCredentials returned fewer (%d) than page 1 (%d)", len(all), len(firstPage))
	}

	// Ensure no duplicate identities slipped through.
	seen := map[string]bool{}
	for _, c := range all {
		key := c.Username + "@" + c.IP + "|" + c.Device
		if seen[key] {
			t.Errorf("duplicate credential in result: %s", key)
		}
		seen[key] = true
	}
}

func mustGet(t *testing.T, c *Client, path string) string {
	t.Helper()
	html, err := c.GetPage(path)
	if err != nil {
		t.Fatalf("GetPage %s: %v", path, err)
	}
	return html
}
