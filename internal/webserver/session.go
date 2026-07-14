package webserver

import (
	"fmt"
	"sync"
	"time"

	"segura-cli/internal/config"
	"segura-cli/internal/credcache"
	"segura-cli/internal/webproxy"
)

// SessionManager caches an authenticated senhasegura client with expiration,
// plus a cache-first credential list so the dashboard loads instantly.
type SessionManager struct {
	cfg        *config.Config
	mu         sync.Mutex
	client     *webproxy.Client
	expiration time.Time
	ttl        time.Duration
	creds      *credcache.Cache[webproxy.Credential]
}

// NewSessionManager creates a new session manager.
func NewSessionManager(cfg *config.Config) *SessionManager {
	sm := &SessionManager{
		cfg: cfg,
		ttl: 15 * time.Minute,
	}
	// In-memory, stale-while-revalidate: serve fresh for 1 min, serve stale (with
	// a single background refresh) up to 10 min, then block on a live fetch.
	sm.creds = credcache.New(sm.fetchCredentials, time.Minute, 10*time.Minute, "")
	return sm
}

// fetchCredentials does a live credential fetch through the cached client.
func (sm *SessionManager) fetchCredentials() ([]webproxy.Credential, error) {
	client, err := sm.GetClient()
	if err != nil {
		return nil, err
	}
	return client.FetchAllCredentials()
}

// GetCredentials returns the credential list cache-first (instant when warm,
// background-refreshed when stale).
func (sm *SessionManager) GetCredentials() ([]webproxy.Credential, error) {
	creds, _, err := sm.creds.Get()
	return creds, err
}

// RefreshCredentials forces a live re-fetch and updates the cache.
func (sm *SessionManager) RefreshCredentials() ([]webproxy.Credential, error) {
	return sm.creds.Refresh()
}

// WarmCredentials kicks a background fetch so the first dashboard load is instant.
func (sm *SessionManager) WarmCredentials() { sm.creds.Warm() }

// GetClient returns an authenticated client, re-authenticating if expired.
func (sm *SessionManager) GetClient() (*webproxy.Client, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.client != nil && time.Now().Before(sm.expiration) {
		return sm.client, nil
	}

	client, err := webproxy.NewClient(sm.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Login(); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	sm.client = client
	sm.expiration = time.Now().Add(sm.ttl)
	return sm.client, nil
}
