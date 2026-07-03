package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

type Config struct {
	Host      string
	Port      int
	AuthToken string
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host cannot be empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return ValidateAuthToken(c.AuthToken)
}

func NewAuthToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func ValidateAuthToken(authToken string) error {
	trimmed := strings.TrimSpace(authToken)
	if authToken == "" {
		return nil
	}
	if trimmed == "" {
		return errors.New("auth-token cannot be empty")
	}
	if trimmed != authToken {
		return errors.New("auth-token cannot contain leading or trailing whitespace")
	}

	for _, r := range authToken {
		if !isAuthTokenRune(r) {
			return errors.New("auth-token can only contain letters, numbers, and URL-safe punctuation (-._~)")
		}
	}

	return nil
}

func isAuthTokenRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '.' || r == '_' || r == '~'
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
