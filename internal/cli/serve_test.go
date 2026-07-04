package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{name: "theme", config: ServeConfig{Host: "localhost", Port: 6174, Theme: "Dracula"}},
		{name: "blank theme", config: ServeConfig{Host: "localhost", Port: 6174, Theme: "   "}, wantErr: "theme cannot be empty"},
		{name: "theme with whitespace", config: ServeConfig{Host: "localhost", Port: 6174, Theme: " Dracula"}, wantErr: "theme cannot contain"},
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

func TestBackgroundServeArgs(t *testing.T) {
	t.Parallel()

	args := backgroundServeArgs(&ServeConfig{
		Host:       "localhost",
		Port:       6174,
		AuthToken:  "secret",
		Theme:      "Dracula",
		InstanceID: "instance",
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{"serve", "--host localhost", "--port 6174", "--background-child", "--instance-id instance", "--auth-token secret", "--theme Dracula"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("backgroundServeArgs() = %q, want substring %q", joined, want)
		}
	}

	args = backgroundServeArgs(&ServeConfig{Host: "localhost", Port: 6174, SkipAuth: true, InstanceID: "instance"})
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--skip-auth") || strings.Contains(joined, "--auth-token") {
		t.Fatalf("backgroundServeArgs(skip auth) = %q", joined)
	}
}

func TestBackgroundStateRoundTrip(t *testing.T) {
	withBackgroundCacheDir(t, t.TempDir())
	startedAt := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	state := backgroundState{Server: &backgroundServer{
		ID:        "id-1",
		PID:       1234,
		Host:      "localhost",
		Port:      6174,
		AuthToken: "secret",
		Theme:     "Dracula",
		LogPath:   filepath.Join(t.TempDir(), "comet.log"),
		StartedAt: startedAt,
	}}

	if err := saveBackgroundState(state); err != nil {
		t.Fatalf("saveBackgroundState() error = %v", err)
	}
	loaded, err := loadBackgroundState()
	if err != nil {
		t.Fatalf("loadBackgroundState() error = %v", err)
	}
	if loaded.Server == nil || loaded.Server.ID != "id-1" || loaded.Server.AuthToken != "secret" || !loaded.Server.StartedAt.Equal(startedAt) {
		t.Fatalf("loaded state = %#v", loaded)
	}

	if err := saveBackgroundState(backgroundState{}); err != nil {
		t.Fatalf("saveBackgroundState(empty) error = %v", err)
	}
	path, err := backgroundStatePath()
	if err != nil {
		t.Fatalf("backgroundStatePath() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file stat error = %v, want not exist", err)
	}
}

func TestBackgroundStatusWithNoServers(t *testing.T) {
	withBackgroundCacheDir(t, t.TempDir())
	var out bytes.Buffer
	if err := runStatusCommand(context.Background(), &out); err != nil {
		t.Fatalf("runStatusCommand() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "No background Comet server running") {
		t.Fatalf("status output = %q", got)
	}
}

func TestBackgroundStatusPrunesStaleServers(t *testing.T) {
	withBackgroundCacheDir(t, t.TempDir())
	state := backgroundState{Server: &backgroundServer{
		ID:        "stale",
		PID:       999999,
		Host:      "localhost",
		Port:      1,
		StartedAt: time.Now(),
	}}
	if err := saveBackgroundState(state); err != nil {
		t.Fatalf("saveBackgroundState() error = %v", err)
	}

	var out bytes.Buffer
	if err := runStatusCommand(context.Background(), &out); err != nil {
		t.Fatalf("runStatusCommand() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "No background Comet server running") {
		t.Fatalf("status output = %q", got)
	}
	loaded, err := loadBackgroundState()
	if err != nil {
		t.Fatalf("loadBackgroundState() error = %v", err)
	}
	if loaded.Server != nil {
		t.Fatalf("loaded state = %#v, want no servers", loaded)
	}
}

func TestBackgroundServerAccessURL(t *testing.T) {
	t.Parallel()

	record := backgroundServer{Host: "0.0.0.0", Port: 6174, AuthToken: "secret"}
	if got := record.accessURL(); got != "http://localhost:6174?token=secret" {
		t.Fatalf("accessURL() = %q", got)
	}
	record.AuthToken = ""
	if got := record.accessURL(); got != "http://localhost:6174" {
		t.Fatalf("accessURL() = %q", got)
	}
}

func TestListThemesCommand(t *testing.T) {
	t.Parallel()

	cmd := newListThemesCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "Comet Warm\n") || !strings.Contains(body, "Dracula\n") {
		t.Fatalf("list-themes output missing bundled themes: %.200q", body)
	}
}

func withBackgroundCacheDir(t *testing.T, dir string) {
	t.Helper()
	old := backgroundCacheDir
	backgroundCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { backgroundCacheDir = old })
}
