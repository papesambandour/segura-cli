package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	URL      string // Full senhasegura URL
	Host     string // Extracted hostname
	Tenant   string // Tenant name (auto-detected from first subdomain of URL)
	User     string // Vault username
	Password string // Vault password
	MFAToken string // TOTP secret key
}

func Load(envFile string) (*Config, error) {
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			return nil, fmt.Errorf("failed to load env file %s: %w", envFile, err)
		}
	} else {
		// Try default .env, ignore if not found
		_ = godotenv.Load()
	}

	cfg := &Config{
		URL:      os.Getenv("SEGURA_URL"),
		User:     os.Getenv("SEGURA_USER"),
		Password: os.Getenv("SEGURA_PASSWORD"),
		MFAToken: os.Getenv("SEGURA_MFA_TOKEN"),
		Tenant:   os.Getenv("SEGURA_TENANT"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid SEGURA_URL: %w", err)
	}
	cfg.Host = parsed.Hostname()

	// Tenant: fallback to first subdomain of the URL
	if cfg.Tenant == "" {
		parts := strings.SplitN(cfg.Host, ".", 2)
		if len(parts) > 0 {
			cfg.Tenant = parts[0]
		}
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.URL == "" {
		return fmt.Errorf("SEGURA_URL is required")
	}
	if c.User == "" {
		return fmt.Errorf("SEGURA_USER is required")
	}
	if c.Password == "" {
		return fmt.Errorf("SEGURA_PASSWORD is required")
	}
	if c.MFAToken == "" {
		return fmt.Errorf("SEGURA_MFA_TOKEN is required")
	}
	return nil
}
