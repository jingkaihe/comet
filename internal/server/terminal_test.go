package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
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

func TestTerminalStatusAndTerminateEndpoints(t *testing.T) {
	const fakePID = 99999999

	oldProcessHasDescendant := processHasDescendant
	processHasDescendant = func(pid int) bool { return pid == fakePID }
	t.Cleanup(func() { processHasDescendant = oldProcessHasDescendant })

	session := newFakeLiveSession(t, fakePID)
	s := &Server{manager: &SessionManager{sessions: map[string]*Session{"pane-1": session}}}

	statusRecorder := httptest.NewRecorder()
	s.handleTerminalStatus(statusRecorder, httptest.NewRequest(http.MethodPost, "/api/terminal/status", strings.NewReader(`{"ids":[" pane-1 ","pane-2","pane-1"]}`)))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%q, want %d", statusRecorder.Code, statusRecorder.Body.String(), http.StatusOK)
	}

	var status terminalStatusResponse
	if err := json.NewDecoder(statusRecorder.Body).Decode(&status); err != nil {
		t.Fatalf("Decode(status) error = %v", err)
	}
	if !status.Running["pane-1"] || status.Running["pane-2"] {
		t.Fatalf("running = %#v, want pane-1 running and pane-2 stopped", status.Running)
	}

	terminateRecorder := httptest.NewRecorder()
	s.handleTerminalTerminate(terminateRecorder, httptest.NewRequest(http.MethodPost, "/api/terminal/terminate", strings.NewReader(`{"ids":["pane-1","pane-1","pane-2"]}`)))
	if terminateRecorder.Code != http.StatusOK {
		t.Fatalf("terminate code = %d body=%q, want %d", terminateRecorder.Code, terminateRecorder.Body.String(), http.StatusOK)
	}

	var terminated terminalTerminateResponse
	if err := json.NewDecoder(terminateRecorder.Body).Decode(&terminated); err != nil {
		t.Fatalf("Decode(terminated) error = %v", err)
	}
	if len(terminated.Terminated) != 1 || terminated.Terminated[0] != "pane-1" {
		t.Fatalf("terminated = %#v, want [pane-1]", terminated.Terminated)
	}
	if running := s.manager.RunningProcesses([]string{"pane-1"}); running["pane-1"] {
		t.Fatalf("running after terminate = %#v, want pane-1 stopped", running)
	}
}

func TestTerminalStatusTreatsIdleShellAsNotRunning(t *testing.T) {
	const fakePID = 99999999

	oldProcessHasDescendant := processHasDescendant
	processHasDescendant = func(int) bool { return false }
	t.Cleanup(func() { processHasDescendant = oldProcessHasDescendant })

	session := newFakeLiveSession(t, fakePID)
	s := &Server{manager: &SessionManager{sessions: map[string]*Session{"pane-1": session}}}

	statusRecorder := httptest.NewRecorder()
	s.handleTerminalStatus(statusRecorder, httptest.NewRequest(http.MethodPost, "/api/terminal/status", strings.NewReader(`{"ids":["pane-1"]}`)))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%q, want %d", statusRecorder.Code, statusRecorder.Body.String(), http.StatusOK)
	}

	var status terminalStatusResponse
	if err := json.NewDecoder(statusRecorder.Body).Decode(&status); err != nil {
		t.Fatalf("Decode(status) error = %v", err)
	}
	if status.Running["pane-1"] {
		t.Fatalf("running = %#v, want idle shell stopped", status.Running)
	}
}

func TestTerminalSessionRequestRejectsEmptyIDs(t *testing.T) {
	t.Parallel()

	s := &Server{manager: &SessionManager{sessions: map[string]*Session{}}}
	recorder := httptest.NewRecorder()
	s.handleTerminalStatus(recorder, httptest.NewRequest(http.MethodPost, "/api/terminal/status", strings.NewReader(`{"ids":[" "]}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusBadRequest)
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

func newFakeLiveSession(t *testing.T, pid int) *Session {
	t.Helper()

	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() {
		_ = readFile.Close()
		_ = writeFile.Close()
	})

	return &Session{
		cancel: func() {},
		cmd:    &exec.Cmd{Process: &os.Process{Pid: pid}},
		ptmx:   readFile,
		done:   make(chan struct{}),
	}
}
