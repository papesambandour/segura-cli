package webserver

import (
	"fmt"
	"sync"
	"time"

	"segura-cli/internal/config"
	"segura-cli/internal/webproxy"
)

// SessionManager caches an authenticated senhasegura client with expiration.
type SessionManager struct {
	cfg        *config.Config
	mu         sync.Mutex
	client     *webproxy.Client
	expiration time.Time
	ttl        time.Duration
}

// NewSessionManager creates a new session manager.
func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		cfg: cfg,
		ttl: 15 * time.Minute,
	}
}

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
