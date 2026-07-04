package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAuthMiddlewareAllowsSkipAuth(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{}}
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
}

func TestAuthMiddlewareRequiresToken(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{AuthToken: "secret"}}
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(recorder.Body.String(), "authentication required") {
		t.Fatalf("body = %q, want authentication error", recorder.Body.String())
	}
}

func TestAuthMiddlewareAcceptsQueryTokenAndSetsCookie(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{AuthToken: "secret"}}
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/healthz?token=secret", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != authCookieName || cookies[0].Value != "secret" {
		t.Fatalf("cookies = %#v, want auth cookie", cookies)
	}
}

func TestAuthMiddlewareAcceptsBearerToken(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{AuthToken: "secret"}}
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	request.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestHealthResponseIncludesInstanceID(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{InstanceID: "instance-1"}}
	recorder := httptest.NewRecorder()
	s.handleHealth(recorder, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body["status"] != "ok" || body["instanceId"] != "instance-1" {
		t.Fatalf("health body = %#v", body)
	}
}

func TestServerRoutesHealthz(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{Host: "localhost", Port: 6174})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	healthzRecorder := httptest.NewRecorder()
	s.mux.ServeHTTP(healthzRecorder, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if healthzRecorder.Code != http.StatusOK {
		t.Fatalf("/api/healthz status = %d, want %d", healthzRecorder.Code, http.StatusOK)
	}

	healthRecorder := httptest.NewRecorder()
	s.mux.ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if healthRecorder.Code != http.StatusNotFound {
		t.Fatalf("/api/health status = %d, want %d", healthRecorder.Code, http.StatusNotFound)
	}
}

func TestLayoutEndpointRoundTrip(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{}, layout: NewLayoutStore()}
	payload := `{"tabs":[{"id":"tab-1","title":"one","customTitle":true,"layout":{"type":"pane","id":"pane-1"},"panes":["ignored"],"activePaneId":"pane-1"}],"activeTabId":"tab-1","theme":"Dracula","version":3}`
	putRecorder := httptest.NewRecorder()
	s.handleLayout(putRecorder, httptest.NewRequest(http.MethodPut, "/api/layout", strings.NewReader(payload)))
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", putRecorder.Code, putRecorder.Body.String())
	}

	getRecorder := httptest.NewRecorder()
	s.handleLayout(getRecorder, httptest.NewRequest(http.MethodGet, "/api/layout", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getRecorder.Code)
	}
	if !strings.Contains(getRecorder.Body.String(), `"activeTabId":"tab-1"`) || !strings.Contains(getRecorder.Body.String(), `"panes":["pane-1"]`) || !strings.Contains(getRecorder.Body.String(), `"theme":"Dracula"`) || !strings.Contains(getRecorder.Body.String(), `"initialized":true`) || !strings.Contains(getRecorder.Body.String(), `"version":3`) {
		t.Fatalf("GET body = %q", getRecorder.Body.String())
	}
}

func TestServerServesEmbeddedIndex(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{Host: "localhost", Port: 6174})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	s.handleIndex(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "Comet") {
		t.Fatalf("index body does not look like Comet app: %.80q", recorder.Body.String())
	}
}

func TestServerServesRootAssets(t *testing.T) {
	t.Parallel()

	s := &Server{
		config: &Config{},
		mux:    http.NewServeMux(),
		staticFS: fstest.MapFS{
			"index.html": {
				Data: []byte("<!doctype html><title>Comet</title>"),
			},
			"manifest.webmanifest": {
				Data: []byte(`{"name": "Comet"}`),
			},
			"favicon.svg": {
				Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
			},
			"favicon.ico": {
				Data: []byte{0x00, 0x00, 0x01, 0x00},
			},
		},
	}
	s.routes()

	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{path: "/manifest.webmanifest", contentType: "application/manifest+json", body: `"name": "Comet"`},
		{path: "/favicon.svg", contentType: "image/svg+xml", body: "<svg"},
		{path: "/favicon.ico", contentType: "image/x-icon", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			s.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, tt.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", contentType, tt.contentType)
			}
			if tt.body != "" && !strings.Contains(recorder.Body.String(), tt.body) {
				t.Fatalf("body = %.120q, want substring %q", recorder.Body.String(), tt.body)
			}
		})
	}
}

func TestServerDoesNotServeNestedUnknownStaticPathsFromIndex(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{Host: "localhost", Port: 6174})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	s.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing/favicon.svg", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestServerUsesThemeAsInitialLayoutDefault(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{Host: "localhost", Port: 6174, Theme: "Dracula"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	recorder := httptest.NewRecorder()
	s.handleLayout(recorder, httptest.NewRequest(http.MethodGet, "/api/layout", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%q", recorder.Code, recorder.Body.String())
	}

	var state LayoutState
	if err := json.NewDecoder(recorder.Body).Decode(&state); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if state.Theme != "Dracula" {
		t.Fatalf("theme = %q, want Dracula", state.Theme)
	}
	if state.Initialized {
		t.Fatal("initial default theme should not mark layout initialized")
	}
}

func TestServerThemeMatchingIsCaseSensitive(t *testing.T) {
	t.Parallel()

	_, err := New(&Config{Host: "localhost", Port: 6174, Theme: "dracula"})
	if err == nil || !strings.Contains(err.Error(), `theme "dracula" not found`) {
		t.Fatalf("New() error = %v, want case-sensitive missing theme error", err)
	}
}

func TestServerRejectsUnknownDefaultTheme(t *testing.T) {
	t.Parallel()

	_, err := New(&Config{Host: "localhost", Port: 6174, Theme: "Missing Theme"})
	if err == nil || !strings.Contains(err.Error(), `theme "Missing Theme" not found`) {
		t.Fatalf("New() error = %v, want missing theme error", err)
	}
}

func TestResponseWriterPreservesHijacker(t *testing.T) {
	t.Parallel()

	inner := &hijackableResponseWriter{}
	rw := &responseWriter{ResponseWriter: inner, statusCode: http.StatusOK}
	_, _, err := rw.Hijack()
	if !errors.Is(err, errHijackedForTest) {
		t.Fatalf("Hijack() error = %v, want %v", err, errHijackedForTest)
	}
	if !inner.hijacked {
		t.Fatal("inner hijacker was not called")
	}
}

var errHijackedForTest = errors.New("hijacked for test")

type hijackableResponseWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (w *hijackableResponseWriter) Header() http.Header { return http.Header{} }

func (w *hijackableResponseWriter) Write(payload []byte) (int, error) { return len(payload), nil }

func (w *hijackableResponseWriter) WriteHeader(int) {}

func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, errHijackedForTest
}
