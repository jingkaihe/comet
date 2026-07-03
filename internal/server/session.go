package server

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

const (
	terminalReplayBufferLimit      = 1024 * 1024
	terminalAttachmentBufferLength = 128
	defaultTerminalRows            = 28
	defaultTerminalCols            = 100
	maxTerminalRows                = 400
	maxTerminalCols                = 400
)

var (
	errSessionClosed = errors.New("terminal session is closed")
	errClientSlow    = errors.New("terminal websocket client is not consuming output")
)

type SessionManager struct {
	ctx       context.Context
	mu        sync.Mutex
	sessions  map[string]*Session
	closed    bool
	closeOnce sync.Once
}

type Session struct {
	id        string
	cwd       string
	shellName string

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	ptmx   *os.File

	mu          sync.Mutex
	ptyMu       sync.Mutex
	attachments map[*Attachment]struct{}
	buffer      []byte
	exited      bool
	exitCode    int

	done       chan struct{}
	doneOnce   sync.Once
	finishOnce sync.Once
	onExit     func()
}

type Attachment struct {
	session  *Session
	outputCh chan []byte
	exitCh   chan int
	errCh    chan error
}

func NewSessionManager(ctx context.Context) *SessionManager {
	if ctx == nil {
		ctx = context.Background()
	}

	manager := &SessionManager{
		ctx:      ctx,
		sessions: make(map[string]*Session),
	}

	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			manager.Close()
		}()
	}

	return manager
}

func (m *SessionManager) GetOrCreate(ctx context.Context, id, cwd string, rows, cols int) (*Session, error) {
	if id == "" {
		return nil, errors.New("session id cannot be empty")
	}

	resolvedCWD, err := resolveCWD(cwd)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, errSessionClosed
	}

	if session := m.sessions[id]; session != nil {
		if session.isAlive() {
			return session, nil
		}
		delete(m.sessions, id)
	}

	shell, shellName := resolveShell()
	session, err := newSession(m.ctx, id, resolvedCWD, shell, shellName, rows, cols)
	if err != nil {
		return nil, err
	}
	session.onExit = func() {
		m.remove(id, session)
	}
	m.sessions[id] = session
	session.start(ctx)
	return session, nil
}

func (m *SessionManager) remove(id string, session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessions[id] == session {
		delete(m.sessions, id)
	}
}

func (m *SessionManager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		sessions := make([]*Session, 0, len(m.sessions))
		for _, session := range m.sessions {
			sessions = append(sessions, session)
		}
		m.sessions = make(map[string]*Session)
		m.mu.Unlock()

		for _, session := range sessions {
			session.terminate()
		}
	})
}

func newSession(ctx context.Context, id, cwd, shell, shellName string, rows, cols int) (*Session, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(sessionCtx, shell)
	cmd.Dir = cwd
	cmd.Env = terminalEnv(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(boundedRows(rows)), Cols: uint16(boundedCols(cols))})
	if err != nil {
		cancel()
		return nil, err
	}

	return &Session{
		id:          id,
		cwd:         cwd,
		shellName:   shellName,
		ctx:         sessionCtx,
		cancel:      cancel,
		cmd:         cmd,
		ptmx:        ptmx,
		attachments: make(map[*Attachment]struct{}),
		done:        make(chan struct{}),
	}, nil
}

func (s *Session) start(ctx context.Context) {
	go s.readPTY()
	go s.wait(ctx)
}

func (s *Session) ReadyMessage() terminalMessage {
	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}

	return terminalMessage{
		Type: "ready",
		ID:   s.id,
		CWD:  s.cwd,
		Name: s.shellName,
		PID:  pid,
		Host: hostname(),
		User: username(),
	}
}

func (s *Session) Attach() (*Attachment, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.exited {
		return nil, nil, errSessionClosed
	}

	attachment := &Attachment{
		session:  s,
		outputCh: make(chan []byte, terminalAttachmentBufferLength),
		exitCh:   make(chan int, 1),
		errCh:    make(chan error, 1),
	}
	s.attachments[attachment] = struct{}{}
	replay := append([]byte(nil), s.buffer...)

	return attachment, replay, nil
}

func (s *Session) Detach(attachment *Attachment) {
	if attachment == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attachments, attachment)
}

func (s *Session) isAlive() bool {
	select {
	case <-s.done:
		return false
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

func (s *Session) WriteInput(payload []byte) error {
	if !s.isAlive() {
		return errSessionClosed
	}

	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()

	_, err := s.ptmx.Write(payload)
	return err
}

func (s *Session) Resize(rows, cols int) error {
	if !s.isAlive() {
		return errSessionClosed
	}

	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()

	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: uint16(boundedRows(rows)), Cols: uint16(boundedCols(cols))})
}

func (s *Session) Signal(sig syscall.Signal) error {
	if !s.isAlive() {
		return errSessionClosed
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	return syscall.Kill(-s.cmd.Process.Pid, sig)
}

func (s *Session) readPTY() {
	buf := make([]byte, 4096)
	for {
		n, readErr := s.ptmx.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.appendOutput(chunk)
		}
		if readErr != nil {
			if s.isAlive() && !errors.Is(readErr, io.ErrClosedPipe) {
				s.terminate()
			}
			return
		}
	}
}

func (s *Session) wait(context.Context) {
	waitErr := s.cmd.Wait()
	code := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		}
	}

	s.finish(code)
}

func (s *Session) appendOutput(chunk []byte) {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return
	}
	s.appendReplayBufferLocked(chunk)
	attachments := s.attachmentsSnapshotLocked()
	s.mu.Unlock()

	for _, attachment := range attachments {
		attachment.sendOutput(chunk)
	}
}

func (s *Session) appendReplayBufferLocked(chunk []byte) {
	if len(chunk) >= terminalReplayBufferLimit {
		s.buffer = append(s.buffer[:0], chunk[len(chunk)-terminalReplayBufferLimit:]...)
		return
	}

	overflow := len(s.buffer) + len(chunk) - terminalReplayBufferLimit
	if overflow > 0 {
		s.buffer = append(s.buffer[:0], s.buffer[overflow:]...)
	}
	s.buffer = append(s.buffer, chunk...)
}

func (s *Session) attachmentsSnapshotLocked() []*Attachment {
	attachments := make([]*Attachment, 0, len(s.attachments))
	for attachment := range s.attachments {
		attachments = append(attachments, attachment)
	}
	return attachments
}

func (s *Session) finish(code int) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.exited = true
		s.exitCode = code
		attachments := s.attachmentsSnapshotLocked()
		s.mu.Unlock()

		for _, attachment := range attachments {
			attachment.sendExit(code)
		}

		s.cancel()
		s.closePTY()
		if s.onExit != nil {
			s.onExit()
		}
		s.doneOnce.Do(func() { close(s.done) })
	})
}

func (s *Session) terminate() {
	s.cancel()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
	}
	s.closePTY()
}

func (s *Session) closePTY() {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	_ = s.ptmx.Close()
}

func (a *Attachment) sendOutput(chunk []byte) {
	select {
	case a.outputCh <- chunk:
	default:
		a.notify(errClientSlow)
	}
}

func (a *Attachment) sendExit(code int) {
	select {
	case a.exitCh <- code:
	default:
		a.notify(errSessionClosed)
	}
}

func (a *Attachment) notify(err error) {
	select {
	case a.errCh <- err:
	default:
	}
}

func resolveCWD(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return os.Getwd()
	}

	return filepath.Abs(raw)
}

func resolveShell() (string, string) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/bash"
	}

	name := filepath.Base(shell)
	if name == "." || name == string(os.PathSeparator) || name == "" {
		name = shell
	}

	return shell, name
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return host
}

func username() string {
	for _, key := range []string{"USER", "LOGNAME", "USERNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "user"
}

func terminalEnv(shell string) []string {
	env := os.Environ()
	hasTerm := false
	hasShell := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=") {
			hasTerm = true
		}
		if strings.HasPrefix(entry, "SHELL=") {
			hasShell = true
		}
	}

	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasShell {
		env = append(env, "SHELL="+shell)
	}

	return env
}

func boundedRows(value int) int {
	if value <= 0 {
		return defaultTerminalRows
	}
	if value > maxTerminalRows {
		return maxTerminalRows
	}
	return value
}

func boundedCols(value int) int {
	if value <= 0 {
		return defaultTerminalCols
	}
	if value > maxTerminalCols {
		return maxTerminalCols
	}
	return value
}
