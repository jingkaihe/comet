package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTerminalDimensionsAreBounded(t *testing.T) {
	t.Parallel()

	if got := boundedRows(0); got != defaultTerminalRows {
		t.Fatalf("boundedRows(0) = %d, want %d", got, defaultTerminalRows)
	}
	if got := boundedRows(maxTerminalRows + 1); got != maxTerminalRows {
		t.Fatalf("boundedRows(max+1) = %d, want %d", got, maxTerminalRows)
	}
	if got := boundedCols(0); got != defaultTerminalCols {
		t.Fatalf("boundedCols(0) = %d, want %d", got, defaultTerminalCols)
	}
	if got := boundedCols(maxTerminalCols + 1); got != maxTerminalCols {
		t.Fatalf("boundedCols(max+1) = %d, want %d", got, maxTerminalCols)
	}
}

func TestParseTerminalSignal(t *testing.T) {
	t.Parallel()

	if _, ok := parseTerminalSignal("SIGINT"); !ok {
		t.Fatal("SIGINT should parse")
	}
	if _, ok := parseTerminalSignal("wat"); ok {
		t.Fatal("unknown signal should not parse")
	}
}

func TestTerminalOriginAllowed(t *testing.T) {
	t.Parallel()

	s := &Server{}
	request := httptestRequestWithOrigin("http://localhost:6174", "localhost:6174")
	if !s.terminalOriginAllowed(request) {
		t.Fatal("same-origin websocket should be allowed")
	}

	request = httptestRequestWithOrigin("http://evil.example", "localhost:6174")
	if s.terminalOriginAllowed(request) {
		t.Fatal("cross-origin websocket should be rejected")
	}
}

func TestTerminalWebSocketUpgradesThroughLoggingMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell-backed pty")
	}

	t.Setenv("SHELL", "/bin/sh")

	server, err := New(&Config{Host: "localhost", Port: 6174})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer server.Close()

	testServer := httptest.NewServer(server.loggingMiddleware(server.mux))
	defer testServer.Close()

	wsURL := "ws" + testServer.URL[len("http"):] + "/api/terminal/ws?id=test-pane&rows=5&cols=20"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("Dial() error = %v status=%d", err, status)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("messageType = %d, want TextMessage", messageType)
	}

	var message terminalMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v payload=%q", err, string(payload))
	}
	if message.Type != "ready" || message.ID != "test-pane" || message.Name != "sh" {
		t.Fatalf("ready message = %+v", message)
	}
}

func httptestRequestWithOrigin(origin string, host string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/terminal/ws", nil)
	request.Header.Set("Origin", origin)
	request.Host = host
	return request
}
