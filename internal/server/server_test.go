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
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))

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
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health?token=secret", nil))

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
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
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
