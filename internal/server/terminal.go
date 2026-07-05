package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	terminalWriteWait  = 10 * time.Second
	terminalPongWait   = 30 * time.Second
	terminalPingPeriod = 20 * time.Second
	terminalReadLimit  = 64 * 1024
)

type terminalMessage struct {
	Type              string `json:"type"`
	ID                string `json:"id,omitempty"`
	Data              string `json:"data,omitempty"`
	Rows              int    `json:"rows,omitempty"`
	Cols              int    `json:"cols,omitempty"`
	Code              int    `json:"code,omitempty"`
	CWD               string `json:"cwd,omitempty"`
	DisplayCWD        string `json:"displayCwd,omitempty"`
	ForegroundCommand string `json:"foregroundCommand,omitempty"`
	DisplayTitle      string `json:"displayTitle,omitempty"`
	Name              string `json:"name,omitempty"`
	PID               int    `json:"pid,omitempty"`
	Text              string `json:"text,omitempty"`
	Host              string `json:"host,omitempty"`
	User              string `json:"user,omitempty"`
}

type terminalSocketRead struct {
	MessageType int
	Payload     []byte
	Err         error
}

type terminalSessionRequest struct {
	IDs []string `json:"ids"`
}

type terminalStatusResponse struct {
	Running map[string]bool `json:"running"`
}

type terminalTerminateResponse struct {
	Terminated []string `json:"terminated"`
}

type websocketWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func terminalStatusMessage(status processStatus) terminalMessage {
	return terminalMessage{
		Type:              "status",
		CWD:               status.CWD,
		DisplayCWD:        status.DisplayCWD,
		ForegroundCommand: status.ForegroundCommand,
		DisplayTitle:      status.DisplayTitle,
	}
}

func (w *websocketWriter) Write(messageType int, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.conn.SetWriteDeadline(time.Now().Add(terminalWriteWait)); err != nil {
		return err
	}
	return w.conn.WriteMessage(messageType, payload)
}

func (w *websocketWriter) writeJSON(message terminalMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return w.Write(websocket.TextMessage, payload)
}

func (s *Server) handleTerminalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request terminalSessionRequest
	if err := decodeTerminalSessionRequest(r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(terminalStatusResponse{Running: s.manager.RunningProcesses(request.IDs)})
}

func (s *Server) handleTerminalTerminate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.terminalOriginAllowed(r) {
		http.Error(w, "cross-origin terminal termination denied", http.StatusForbidden)
		return
	}
	if !requestContentTypeIsJSON(r) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	var request terminalSessionRequest
	if err := decodeTerminalSessionRequest(r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(terminalTerminateResponse{Terminated: s.manager.Terminate(request.IDs)})
}

func decodeTerminalSessionRequest(r *http.Request, request *terminalSessionRequest) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		return errors.New("invalid terminal session request")
	}

	request.IDs = normalizedTerminalIDs(request.IDs)
	if len(request.IDs) == 0 {
		return errors.New("terminal session ids cannot be empty")
	}

	return nil
}

func requestContentTypeIsJSON(r *http.Request) bool {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return false
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func normalizedTerminalIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id := strings.TrimSpace(query.Get("id"))
	if id == "" {
		http.Error(w, "missing terminal id", http.StatusBadRequest)
		return
	}

	rows := boundedRows(parseTerminalDimension(query.Get("rows")))
	cols := boundedCols(parseTerminalDimension(query.Get("cols")))

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.terminalOriginAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	session, err := s.manager.GetOrCreate(r.Context(), id, query.Get("cwd"), rows, cols)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","text":"failed to create terminal"}`))
		return
	}

	writer := &websocketWriter{conn: conn}
	conn.SetReadLimit(terminalReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	})

	attachment, replay, err := session.Attach()
	if err != nil {
		return
	}
	defer session.Detach(attachment)

	if err := session.Resize(rows, cols); err != nil && !errors.Is(err, errSessionClosed) {
		return
	}
	if err := writer.writeJSON(session.ReadyMessage(r.Context())); err != nil {
		return
	}
	if len(replay) > 0 {
		if err := writer.Write(websocket.BinaryMessage, replay); err != nil {
			return
		}
	}
	if err := writer.writeJSON(terminalMessage{Type: "replay-complete"}); err != nil {
		return
	}

	go func() {
		ticker := time.NewTicker(terminalPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writer.Write(websocket.PingMessage, nil); err != nil {
					attachment.notify(err)
					return
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(terminalStatusProbeInterval)
		defer ticker.Stop()

		lastStatus := processStatus{}
		for {
			var status processStatus
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status = session.ProcessStatus(ctx)
			case <-attachment.titleCh:
				// push OSC titles immediately without re-probing the process tree
				status = lastStatus
				status.DisplayTitle = composeDisplayTitle(session.OSCTitle(), status.ForegroundCommand, status.DisplayCWD)
			}
			if status == lastStatus {
				continue
			}
			lastStatus = status
			if err := writer.writeJSON(terminalStatusMessage(status)); err != nil {
				attachment.notify(err)
				return
			}
		}
	}()

	readCh := make(chan terminalSocketRead, 1)
	go readTerminalWebsocket(ctx, conn, readCh)

	for {
		select {
		case output := <-attachment.outputCh:
			if err := writer.Write(websocket.BinaryMessage, output); err != nil {
				return
			}
		case code := <-attachment.exitCh:
			_ = writer.writeJSON(terminalMessage{Type: "exit", Code: code})
			return
		case asyncErr := <-attachment.errCh:
			if asyncErr != nil && !errors.Is(asyncErr, errClientSlow) {
				return
			}
		case socketRead := <-readCh:
			if socketRead.Err != nil {
				return
			}

			switch socketRead.MessageType {
			case websocket.BinaryMessage:
				if err := session.WriteInput(socketRead.Payload); err != nil {
					return
				}
			case websocket.TextMessage:
				var message terminalMessage
				if err := json.Unmarshal(socketRead.Payload, &message); err != nil {
					continue
				}

				switch message.Type {
				case "input":
					if message.Data == "" {
						continue
					}
					if err := session.WriteInput([]byte(message.Data)); err != nil {
						return
					}
				case "resize":
					if err := session.Resize(message.Rows, message.Cols); err != nil && !errors.Is(err, errSessionClosed) {
						return
					}
				case "signal":
					if sig, ok := parseTerminalSignal(message.Name); ok {
						_ = session.Signal(sig)
					}
				}
			}
		}
	}
}

func readTerminalWebsocket(ctx context.Context, conn *websocket.Conn, readCh chan<- terminalSocketRead) {
	for {
		messageType, payload, readErr := conn.ReadMessage()
		select {
		case readCh <- terminalSocketRead{MessageType: messageType, Payload: payload, Err: readErr}:
		case <-ctx.Done():
			return
		}
		if readErr != nil {
			return
		}
	}
}

func parseTerminalDimension(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}

func parseTerminalSignal(name string) (syscall.Signal, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "INT", "SIGINT":
		return syscall.SIGINT, true
	case "TERM", "SIGTERM":
		return syscall.SIGTERM, true
	case "HUP", "SIGHUP":
		return syscall.SIGHUP, true
	case "QUIT", "SIGQUIT":
		return syscall.SIGQUIT, true
	default:
		return 0, false
	}
}

func (s *Server) terminalOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}

	originHost := normalizedHostPort(originURL.Host)
	requestHost := normalizedHostPort(r.Host)
	return originHost != "" && originHost == requestHost
}

func normalizedHostPort(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(trimmed)
	if err == nil {
		return net.JoinHostPort(normalizedHostname(host), port)
	}

	if ip := net.ParseIP(strings.Trim(trimmed, "[]")); ip != nil {
		return strings.ToLower(ip.String())
	}

	return normalizedHostname(trimmed)
}

func normalizedHostname(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "[]"))
}
