package auth

import (
	"fmt"
	"time"

	"github.com/pquerna/otp/totp"
)

// GenerateTOTP generates a 6-digit TOTP code from the given base32-encoded secret.
func GenerateTOTP(secret string) (string, error) {
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		return "", fmt.Errorf("failed to generate TOTP code: %w", err)
	}
	return code, nil
}
