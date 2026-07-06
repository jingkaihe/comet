package server

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	terminalReplayBufferLimit      = 1024 * 1024
	terminalAttachmentBufferLength = 128
	terminalTerm                   = "xterm-ghostty"
	fallbackTerminalTerm           = "xterm-256color"
	terminalProgram                = "comet"
	terminalTerminfoCacheVersion   = "xterm-ghostty-v1"
	terminalTerminfoInstallTimeout = 3 * time.Second
	defaultTerminalRows            = 28
	defaultTerminalCols            = 100
	maxTerminalRows                = 400
	maxTerminalCols                = 400
	processProbeTimeout            = 500 * time.Millisecond
	terminalStatusProbeInterval    = time.Second
	terminalTitleMaxLength         = 80
)

var (
	errSessionClosed = errors.New("terminal session is closed")
	errClientSlow    = errors.New("terminal websocket client is not consuming output")
)

var processHasDescendant = defaultProcessHasDescendant

var processSnapshot = defaultProcessSnapshot

var terminalCacheDir = os.UserCacheDir

//go:embed terminfo/xterm-ghostty.terminfo
var xtermGhosttyTerminfo string

var terminalProfile = defaultTerminalProfile

var terminfoInstall struct {
	once sync.Once
	dir  string
	err  error
}

type terminalProfileConfig struct {
	term        string
	terminfoDir string
}

type processStatus struct {
	CWD               string
	DisplayCWD        string
	ForegroundCommand string
	DisplayTitle      string
}

type processSnapshotResult struct {
	cwd               string
	foregroundCommand string
}

type SessionManager struct {
	ctx       context.Context
	mu        sync.Mutex
	sessions  map[string]*Session
	closed    bool
	closeOnce sync.Once
}

type Session struct {
	id        string
	shellName string

	cancel context.CancelFunc
	cmd    *exec.Cmd
	ptmx   *os.File

	mu           sync.Mutex
	ptyMu        sync.Mutex
	attachments  map[*Attachment]struct{}
	buffer       []byte
	exited       bool
	exitCode     int
	lastKnownCWD string
	oscTitle     string

	// titleParser is only touched by the readPTY goroutine.
	titleParser oscTitleParser

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
	titleCh  chan struct{}
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
	session.start()
	return session, nil
}

func (m *SessionManager) remove(id string, session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessions[id] == session {
		delete(m.sessions, id)
	}
}

func (m *SessionManager) RunningProcesses(ids []string) map[string]bool {
	statuses := make(map[string]bool, len(ids))
	sessions := make(map[string]*Session, len(ids))

	m.mu.Lock()
	for _, id := range ids {
		if _, ok := statuses[id]; ok {
			continue
		}
		statuses[id] = false

		session := m.sessions[id]
		if session == nil {
			continue
		}

		if !session.isAlive() {
			delete(m.sessions, id)
			continue
		}

		sessions[id] = session
	}
	m.mu.Unlock()

	for id, session := range sessions {
		statuses[id] = session.hasChildProcesses()
	}

	return statuses
}

func (m *SessionManager) Terminate(ids []string) []string {
	terminatedIDs := make([]string, 0, len(ids))
	sessions := make([]*Session, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))

	m.mu.Lock()
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		session := m.sessions[id]
		if session == nil {
			continue
		}

		delete(m.sessions, id)
		if session.isAlive() {
			terminatedIDs = append(terminatedIDs, id)
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()

	for _, session := range sessions {
		session.terminate()
	}

	return terminatedIDs
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
		id:           id,
		shellName:    shellName,
		cancel:       cancel,
		cmd:          cmd,
		ptmx:         ptmx,
		attachments:  make(map[*Attachment]struct{}),
		lastKnownCWD: cwd,
		done:         make(chan struct{}),
	}, nil
}

func (s *Session) start() {
	go s.readPTY()
	go s.wait()
}

func (s *Session) ReadyMessage(ctx context.Context) terminalMessage {
	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	status := s.ProcessStatus(ctx)

	return terminalMessage{
		Type:              "ready",
		ID:                s.id,
		CWD:               status.CWD,
		DisplayCWD:        status.DisplayCWD,
		ForegroundCommand: status.ForegroundCommand,
		DisplayTitle:      status.DisplayTitle,
		Name:              s.shellName,
		PID:               pid,
		Host:              hostname(),
		User:              username(),
	}
}

func (s *Session) ProcessStatus(ctx context.Context) processStatus {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	cwd := s.lastKnownCWD
	if strings.TrimSpace(cwd) == "" && s.cmd != nil {
		cwd = s.cmd.Dir
	}
	oscTitle := s.oscTitle
	s.mu.Unlock()
	foregroundCommand := ""

	if s.isAlive() && s.cmd != nil && s.cmd.Process != nil && s.cmd.Process.Pid > 0 {
		probeCtx, cancel := context.WithTimeout(ctx, processProbeTimeout)
		snapshot := processSnapshot(probeCtx, s.cmd.Process.Pid)
		cancel()

		if snapshotCWD := strings.TrimSpace(snapshot.cwd); snapshotCWD != "" {
			cwd = snapshotCWD
			s.mu.Lock()
			s.lastKnownCWD = snapshotCWD
			s.mu.Unlock()
		}
		foregroundCommand = snapshot.foregroundCommand
	}

	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "~"
	}
	displayCWD := displayCWD(cwd)
	displayTitle := composeDisplayTitle(oscTitle, foregroundCommand, displayCWD)

	return processStatus{
		CWD:               cwd,
		DisplayCWD:        displayCWD,
		ForegroundCommand: foregroundCommand,
		DisplayTitle:      displayTitle,
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
		titleCh:  make(chan struct{}, 1),
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

func (s *Session) hasChildProcesses() bool {
	if !s.isAlive() || s.cmd == nil || s.cmd.Process == nil || s.cmd.Process.Pid <= 0 {
		return false
	}

	return processHasDescendant(s.cmd.Process.Pid)
}

func defaultProcessHasDescendant(pid int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	parent, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return false
	}

	children, err := parent.ChildrenWithContext(ctx)
	return err == nil && len(children) > 0
}

func defaultProcessSnapshot(ctx context.Context, pid int) processSnapshotResult {
	root, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return processSnapshotResult{}
	}

	result := processSnapshotResult{}
	if cwd, err := root.CwdWithContext(ctx); err == nil {
		result.cwd = cwd
	}

	foreground := foregroundDescendant(ctx, root)
	if foreground != nil {
		result.foregroundCommand = processCommand(ctx, foreground)
	}

	return result
}

func foregroundDescendant(ctx context.Context, root *process.Process) *process.Process {
	if root == nil {
		return nil
	}

	children, err := root.ChildrenWithContext(ctx)
	if err != nil || len(children) == 0 {
		return nil
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Pid < children[j].Pid })

	var foreground *process.Process
	for _, child := range children {
		isForeground, err := child.ForegroundWithContext(ctx)
		if err == nil && isForeground {
			return child
		}

		if descendant := foregroundDescendant(ctx, child); descendant != nil {
			foreground = descendant
		}
	}

	return foreground
}

func processCommand(ctx context.Context, proc *process.Process) string {
	if proc == nil {
		return ""
	}

	args, err := proc.CmdlineSliceWithContext(ctx)
	if err == nil && len(args) > 0 {
		return formatCommand(args)
	}

	if line, err := proc.CmdlineWithContext(ctx); err == nil {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateTerminalTitle(line)
		}
	}

	name, err := proc.NameWithContext(ctx)
	if err != nil {
		return ""
	}
	return truncateTerminalTitle(strings.TrimSpace(name))
}

func formatCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for index, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if index == 0 {
			arg = filepath.Base(arg)
		}
		if strings.ContainsAny(arg, " \t\n\r") {
			arg = quoteCommandArg(arg)
		}
		parts = append(parts, arg)
	}
	return truncateTerminalTitle(strings.Join(parts, " "))
}

func truncateTerminalTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) <= terminalTitleMaxLength {
		return title
	}

	runes := []rune(title)
	return string(runes[:terminalTitleMaxLength-1]) + "…"
}

func quoteCommandArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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
			if title, ok := s.titleParser.feed(chunk); ok {
				s.setOSCTitle(title)
			}
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

func (s *Session) wait() {
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

// setOSCTitle latches the OSC 0/2 title and wakes attached clients so
// title changes push without waiting for the next status probe tick.
func (s *Session) setOSCTitle(title string) {
	title = truncateTerminalTitle(title)

	s.mu.Lock()
	if s.exited || s.oscTitle == title {
		s.mu.Unlock()
		return
	}
	s.oscTitle = title
	attachments := s.attachmentsSnapshotLocked()
	s.mu.Unlock()

	for _, attachment := range attachments {
		attachment.notifyTitle()
	}
}

func (s *Session) OSCTitle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.oscTitle
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

// notifyTitle coalesces; the reader picks up the latest title on drain.
func (a *Attachment) notifyTitle() {
	select {
	case a.titleCh <- struct{}{}:
	default:
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

func displayCWD(cwd string) string {
	path := strings.TrimSpace(cwd)
	if path == "" {
		return "~"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	home = strings.TrimSpace(home)
	if home == "" || home == string(os.PathSeparator) {
		return path
	}

	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}

	return path
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
	profile := terminalProfile()
	baseEnv := os.Environ()
	env := make([]string, 0, len(baseEnv)+5)
	for _, entry := range baseEnv {
		key, _, ok := strings.Cut(entry, "=")
		if ok && shouldDropTerminalEnv(key) {
			continue
		}
		env = append(env, entry)
	}

	env = append(env,
		"TERM="+profile.term,
		"TERM_PROGRAM="+terminalProgram,
		"COLORTERM=truecolor",
		"SHELL="+shell,
	)
	if profile.terminfoDir != "" {
		env = append(env, "TERMINFO_DIRS="+profile.terminfoDir+string(os.PathListSeparator))
	}

	return env
}

func shouldDropTerminalEnv(key string) bool {
	switch key {
	case "TERM", "TERMINFO", "TERMINFO_DIRS", "TERM_PROGRAM", "TERM_PROGRAM_VERSION",
		"COLORTERM", "LC_TERMINAL", "LC_TERMINAL_VERSION", "TERM_SESSION_ID",
		"ITERM_SESSION_ID", "SHELL":
		return true
	}

	return strings.HasPrefix(key, "GHOSTTY_") || strings.HasPrefix(key, "KITTY_") || strings.HasPrefix(key, "WEZTERM_")
}

func defaultTerminalProfile() terminalProfileConfig {
	dir, err := ensureXtermGhosttyTerminfo()
	if err != nil {
		return terminalProfileConfig{term: fallbackTerminalTerm}
	}

	return terminalProfileConfig{term: terminalTerm, terminfoDir: dir}
}

func ensureXtermGhosttyTerminfo() (string, error) {
	terminfoInstall.once.Do(func() {
		terminfoInstall.dir, terminfoInstall.err = installXtermGhosttyTerminfo()
	})
	return terminfoInstall.dir, terminfoInstall.err
}

func installXtermGhosttyTerminfo() (string, error) {
	cacheDir, err := terminalCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}

	terminfoDir := filepath.Join(cacheDir, "comet", "terminfo", terminalTerminfoCacheVersion)
	if xtermGhosttyTerminfoInstalled(terminfoDir) {
		return terminfoDir, nil
	}

	if err := os.MkdirAll(terminfoDir, 0o700); err != nil {
		return "", fmt.Errorf("create terminfo cache: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), terminalTerminfoInstallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tic", "-x", "-o", terminfoDir, "-")
	cmd.Stdin = strings.NewReader(xtermGhosttyTerminfo)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", fmt.Errorf("install xterm-ghostty terminfo: %w: %s", err, message)
		}
		return "", fmt.Errorf("install xterm-ghostty terminfo: %w", err)
	}

	if !xtermGhosttyTerminfoInstalled(terminfoDir) {
		return "", errors.New("install xterm-ghostty terminfo: compiled entry was not created")
	}

	return terminfoDir, nil
}

func xtermGhosttyTerminfoInstalled(dir string) bool {
	if dir == "" {
		return false
	}

	paths := []string{
		filepath.Join(dir, "x", terminalTerm),
		filepath.Join(dir, fmt.Sprintf("%02x", terminalTerm[0]), terminalTerm),
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
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
