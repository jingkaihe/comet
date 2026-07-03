package server

import (
	"strings"
	"testing"
)

func TestValidateAuthToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   string
		wantErr string
	}{
		{name: "empty allowed", token: ""},
		{name: "url safe", token: "abc-123._~XYZ"},
		{name: "blank rejected", token: "   ", wantErr: "auth-token cannot be empty"},
		{name: "leading whitespace", token: " token", wantErr: "leading or trailing whitespace"},
		{name: "invalid punctuation", token: "abc/123", wantErr: "URL-safe punctuation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAuthToken(tt.token)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAuthToken() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateAuthToken() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "valid", config: Config{Host: "localhost", Port: 6174}},
		{name: "empty host", config: Config{Port: 6174}, wantErr: "host cannot be empty"},
		{name: "low port", config: Config{Host: "localhost"}, wantErr: "port must be between"},
		{name: "high port", config: Config{Host: "localhost", Port: 70000}, wantErr: "port must be between"},
		{name: "bad token", config: Config{Host: "localhost", Port: 6174, AuthToken: "bad token"}, wantErr: "URL-safe punctuation"},
		{name: "theme", config: Config{Host: "localhost", Port: 6174, Theme: "Dracula"}},
		{name: "blank theme", config: Config{Host: "localhost", Port: 6174, Theme: "   "}, wantErr: "theme cannot be empty"},
		{name: "theme with whitespace", config: Config{Host: "localhost", Port: 6174, Theme: " Dracula"}, wantErr: "theme cannot contain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
