package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	webassets "github.com/jingkaihe/comet/internal/web"
)

const authCookieName = "comet_auth_token"

type Server struct {
	config     *Config
	mux        *http.ServeMux
	server     *http.Server
	manager    *SessionManager
	layout     *LayoutStore
	staticFS   fs.FS
	themesOnce sync.Once
	themes     []TerminalTheme
}

func New(config *Config) (*Server, error) {
	if config == nil {
		config = &Config{Host: "localhost", Port: 6174}
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	defaultThemeName := strings.TrimSpace(config.Theme)
	var themes []TerminalTheme
	if defaultThemeName != "" {
		themes = collectTerminalThemes()
		found := false
		for _, theme := range themes {
			if theme.Name == defaultThemeName {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("theme %q not found", config.Theme)
		}
	}

	staticFS, err := webassets.DistFS()
	if err != nil {
		return nil, err
	}

	s := &Server{
		config:   config,
		mux:      http.NewServeMux(),
		manager:  NewSessionManager(context.Background()),
		layout:   NewLayoutStoreWithDefaultTheme(defaultThemeName),
		staticFS: staticFS,
	}
	if themes != nil {
		s.themesOnce.Do(func() {
			s.themes = themes
		})
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.Handle("/assets/", s.authMiddleware(http.FileServer(http.FS(s.staticFS))))
	s.mux.Handle("/api/health", s.authMiddleware(http.HandlerFunc(s.handleHealth)))
	s.mux.Handle("/api/layout", s.authMiddleware(http.HandlerFunc(s.handleLayout)))
	s.mux.Handle("/api/themes", s.authMiddleware(http.HandlerFunc(s.handleThemes)))
	s.mux.Handle("/api/terminal/status", s.authMiddleware(http.HandlerFunc(s.handleTerminalStatus)))
	s.mux.Handle("/api/terminal/terminate", s.authMiddleware(http.HandlerFunc(s.handleTerminalTerminate)))
	s.mux.Handle("/api/terminal/ws", s.authMiddleware(http.HandlerFunc(s.handleTerminalWebSocket)))
	s.mux.Handle("/", s.authMiddleware(http.HandlerFunc(s.handleIndex)))
}

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetLayout(w, r)
	case http.MethodPut:
		s.handlePutLayout(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) Start(ctx context.Context) error {
	addr := net.JoinHostPort(s.config.Host, fmt.Sprintf("%d", s.config.Port))
	s.server = &http.Server{
		Addr:              addr,
		Handler:           s.loggingMiddleware(s.mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) Close() error {
	if s.manager != nil {
		s.manager.Close()
	}
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		content, err := fs.ReadFile(s.staticFS, "index.html")
		if err != nil {
			http.Error(w, "failed to load application", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write(content)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	content, err := fs.ReadFile(s.staticFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if contentType := rootAssetContentType(name); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
}

func rootAssetContentType(name string) string {
	switch name {
	case "manifest.webmanifest":
		return "application/manifest+json"
	case "favicon.ico":
		return "image/x-icon"
	case "favicon.svg":
		return "image/svg+xml"
	default:
		return ""
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	authToken := strings.TrimSpace(s.config.AuthToken)
	if authToken == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryToken, hasQueryToken := r.URL.Query()["token"]
		if hasQueryToken {
			if len(queryToken) == 0 || !constantTimeStringEqual(queryToken[0], authToken) {
				s.writeAuthError(w, r, http.StatusUnauthorized, "invalid authentication token")
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     authCookieName,
				Value:    authToken,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   r.TLS != nil,
			})

			if r.Method == http.MethodGet && !isWebsocketUpgrade(r) && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/assets/") {
				redirectURL := *r.URL
				query := redirectURL.Query()
				query.Del("token")
				redirectURL.RawQuery = query.Encode()
				http.Redirect(w, r, redirectURL.String(), http.StatusFound)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		if requestHasAuthToken(r, authToken) {
			next.ServeHTTP(w, r)
			return
		}

		s.writeAuthError(w, r, http.StatusUnauthorized, "authentication required")
	})
}

func requestHasAuthToken(r *http.Request, authToken string) bool {
	if headerToken := authHeaderToken(r.Header.Get("Authorization")); headerToken != "" {
		return constantTimeStringEqual(headerToken, authToken)
	}

	cookie, err := r.Cookie(authCookieName)
	return err == nil && constantTimeStringEqual(cookie.Value, authToken)
}

func authHeaderToken(headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return ""
	}

	for _, prefix := range []string{"Bearer ", "Token "} {
		if len(headerValue) > len(prefix) && strings.EqualFold(headerValue[:len(prefix)], prefix) {
			return strings.TrimSpace(headerValue[len(prefix):])
		}
	}

	return headerValue
}

func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, "%s\n\nOpen the tokenized URL printed by `comet serve`, or restart with --skip-auth.\n", message)
}

func isWebsocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", rw.statusCode, "duration", time.Since(start), "remote", r.RemoteAddr)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}

	return hijacker.Hijack()
}

func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}

	return pusher.Push(target, opts)
}

func (w *responseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}

	return io.Copy(w.ResponseWriter, reader)
}
