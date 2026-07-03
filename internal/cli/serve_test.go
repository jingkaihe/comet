package cli

import (
	"strings"
	"testing"
)

func TestValidateServeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  ServeConfig
		wantErr string
	}{
		{name: "valid", config: ServeConfig{Host: "localhost", Port: 6174}},
		{name: "empty host", config: ServeConfig{Port: 6174}, wantErr: "host cannot be empty"},
		{name: "bad host", config: ServeConfig{Host: "bad host", Port: 6174}, wantErr: "invalid host"},
		{name: "bad port", config: ServeConfig{Host: "localhost", Port: 80808}, wantErr: "port must be between"},
		{name: "skip with token", config: ServeConfig{Host: "localhost", Port: 6174, SkipAuth: true, AuthToken: "secret"}, wantErr: "--auth-token cannot be used"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateServeConfig(&tt.config)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateServeConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateServeConfig() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestServeURLHelpers(t *testing.T) {
	t.Parallel()

	if got := serveBaseURL("0.0.0.0", 6174); got != "http://localhost:6174" {
		t.Fatalf("serveBaseURL() = %q", got)
	}
	if got := serveBaseURL("::1", 6174); got != "http://[::1]:6174" {
		t.Fatalf("serveBaseURL() = %q", got)
	}
	if got := serveURLWithToken("http://localhost:6174", "abc123"); got != "http://localhost:6174?token=abc123" {
		t.Fatalf("serveURLWithToken() = %q", got)
	}
}
