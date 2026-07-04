package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/comet/internal/server"
	"github.com/spf13/cobra"
)

const (
	backgroundStateFileName = "background.json"
	backgroundStartTimeout  = 10 * time.Second
	backgroundStopTimeout   = 5 * time.Second
)

// backgroundCacheDir is a small test hook for isolating background state files.
var backgroundCacheDir = os.UserCacheDir

type backgroundState struct {
	Server *backgroundServer `json:"server,omitempty"`
}

type backgroundServer struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	AuthToken string    `json:"authToken,omitempty"`
	Theme     string    `json:"theme,omitempty"`
	LogPath   string    `json:"logPath"`
	StartedAt time.Time `json:"startedAt"`
}

type backgroundHealth struct {
	Status     string `json:"status"`
	InstanceID string `json:"instanceId"`
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show background Comet status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusCommand(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func newDownCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the background Comet server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDownCommand(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func runServeBackground(ctx context.Context, config *ServeConfig, out io.Writer) error {
	authToken, err := resolveServeAuthToken(config)
	if err != nil {
		return err
	}
	if !config.SkipAuth && authToken == "" {
		authToken, err = server.NewToken()
		if err != nil {
			return fmt.Errorf("failed to generate auth token: %w", err)
		}
	}

	instanceID, err := server.NewToken()
	if err != nil {
		return fmt.Errorf("failed to generate background instance ID: %w", err)
	}

	state, err := loadBackgroundState()
	if err != nil {
		return err
	}
	liveServer, err := inspectBackgroundServer(ctx, state.Server)
	if err != nil {
		return err
	}
	if liveServer != nil {
		return fmt.Errorf("background Comet server already running on %s", liveServer.baseURL())
	}

	logPath, err := backgroundLogPath(instanceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create background log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open background log file: %w", err)
	}
	defer logFile.Close()

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find comet executable: %w", err)
	}

	childConfig := *config
	childConfig.AuthToken = ""
	childConfig.AuthTokenFile = ""
	childConfig.InstanceID = instanceID
	if authToken != "" {
		tokenFile, cleanup, err := writeBackgroundAuthTokenFile(instanceID, authToken)
		if err != nil {
			return err
		}
		defer cleanup()
		childConfig.AuthTokenFile = tokenFile
	}

	cmd := exec.Command(executable, backgroundServeArgs(&childConfig)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background server: %w", err)
	}

	record := backgroundServer{
		ID:        instanceID,
		PID:       cmd.Process.Pid,
		Host:      config.Host,
		Port:      config.Port,
		AuthToken: authToken,
		Theme:     config.Theme,
		LogPath:   logPath,
		StartedAt: time.Now(),
	}

	if err := waitForBackgroundReady(ctx, record, backgroundStartTimeout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("background server did not start: %w (see log: %s)", err, logPath)
	}
	if err := cmd.Process.Release(); err != nil {
		_ = stopBackgroundServer(ctx, record)
		return fmt.Errorf("release background process: %w", err)
	}

	if err := saveBackgroundState(backgroundState{Server: &record}); err != nil {
		_ = stopBackgroundServer(ctx, record)
		return err
	}

	fmt.Fprintf(out, "Comet web terminal started in the background on %s\n", record.baseURL())
	fmt.Fprintf(out, "PID: %d\n", record.PID)
	if authToken != "" {
		fmt.Fprintf(out, "Authentication token: %s\n", authToken)
		fmt.Fprintf(out, "Open this URL: %s\n", record.accessURL())
	} else {
		fmt.Fprintln(out, "Authentication disabled (--skip-auth)")
	}
	fmt.Fprintf(out, "Logs: %s\n", logPath)
	fmt.Fprintln(out, "Run `comet status` to view the background server and `comet down` to stop it")
	return nil
}

func runStatusCommand(ctx context.Context, out io.Writer) error {
	state, err := loadBackgroundState()
	if err != nil {
		return err
	}
	running, err := inspectBackgroundServer(ctx, state.Server)
	if err != nil {
		return err
	}
	if running == nil && state.Server != nil {
		if err := saveBackgroundState(backgroundState{}); err != nil {
			return err
		}
	}

	if running == nil {
		fmt.Fprintln(out, "No background Comet server running")
		return nil
	}

	fmt.Fprintln(out, "Background Comet server:")
	fmt.Fprintf(out, "- pid %d at %s\n", running.PID, running.baseURL())
	fmt.Fprintf(out, "  url: %s\n", running.accessURL())
	fmt.Fprintf(out, "  started: %s\n", running.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(out, "  log: %s\n", running.LogPath)
	return nil
}

func runDownCommand(ctx context.Context, out io.Writer) error {
	state, err := loadBackgroundState()
	if err != nil {
		return err
	}
	running, err := inspectBackgroundServer(ctx, state.Server)
	if err != nil {
		return err
	}

	if running == nil {
		if state.Server != nil {
			if err := saveBackgroundState(backgroundState{}); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "No background Comet server running")
		return nil
	}

	fmt.Fprintf(out, "Stopping Comet background server pid %d at %s\n", running.PID, running.baseURL())
	if err := stopBackgroundServer(ctx, *running); err != nil {
		return fmt.Errorf("pid %d: %w", running.PID, err)
	}
	fmt.Fprintf(out, "Stopped Comet background server pid %d\n", running.PID)

	remaining, err := inspectBackgroundServer(ctx, running)
	if err != nil {
		return err
	}
	if err := saveBackgroundState(backgroundState{Server: remaining}); err != nil {
		return err
	}

	fmt.Fprintln(out, "Stopped background Comet server")
	return nil
}

func backgroundServeArgs(config *ServeConfig) []string {
	args := []string{
		"serve",
		"--host", config.Host,
		"--port", strconv.Itoa(config.Port),
		"--background-child",
		"--instance-id", config.InstanceID,
	}
	if config.AuthTokenFile != "" {
		args = append(args, "--auth-token-file", config.AuthTokenFile)
	}
	if config.SkipAuth {
		args = append(args, "--skip-auth")
	}
	if config.Theme != "" {
		args = append(args, "--theme", config.Theme)
	}
	return args
}

func writeBackgroundAuthTokenFile(instanceID string, authToken string) (string, func(), error) {
	dir, err := backgroundAuthTokenDir()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create background auth token directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("set background auth token directory permissions: %w", err)
	}

	tokenPath := filepath.Join(dir, instanceID+".token")
	tokenFile, err := os.OpenFile(tokenPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("create background auth token file: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(tokenPath)
	}

	if _, err := io.WriteString(tokenFile, authToken+"\n"); err != nil {
		_ = tokenFile.Close()
		cleanup()
		return "", nil, fmt.Errorf("write background auth token: %w", err)
	}
	if err := tokenFile.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close background auth token file: %w", err)
	}

	return tokenPath, cleanup, nil
}

func backgroundAuthTokenDir() (string, error) {
	dir, err := backgroundDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth"), nil
}

func inspectBackgroundServer(ctx context.Context, record *backgroundServer) (*backgroundServer, error) {
	if record == nil || record.ID == "" || record.PID <= 0 || record.Host == "" || record.Port <= 0 {
		return nil, nil
	}

	ok, err := backgroundHealthMatches(ctx, *record)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	running := *record
	return &running, nil
}

func backgroundHealthMatches(ctx context.Context, record backgroundServer) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, record.healthURL(), nil)
	if err != nil {
		return false, err
	}
	if record.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+record.AuthToken)
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return false, nil
	}

	var health backgroundHealth
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return false, nil
	}
	return health.Status == "ok" && health.InstanceID == record.ID, nil
}

func waitForBackgroundReady(ctx context.Context, record backgroundServer, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		ok, err := backgroundHealthMatches(ctx, record)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s", record.healthURL())
		case <-ticker.C:
		}
	}
}

func stopBackgroundServer(ctx context.Context, record backgroundServer) error {
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if err := waitForBackgroundStopped(ctx, record, backgroundStopTimeout); err == nil {
		return nil
	}

	if err := process.Kill(); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return waitForBackgroundStopped(ctx, record, 2*time.Second)
}

func waitForBackgroundStopped(ctx context.Context, record backgroundServer, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		ok, err := backgroundHealthMatches(ctx, record)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s to stop", record.baseURL())
		case <-ticker.C:
		}
	}
}

func loadBackgroundState() (backgroundState, error) {
	path, err := backgroundStatePath()
	if err != nil {
		return backgroundState{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return backgroundState{}, nil
	}
	if err != nil {
		return backgroundState{}, fmt.Errorf("read background state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return backgroundState{}, nil
	}

	var state backgroundState
	if err := json.Unmarshal(data, &state); err != nil {
		return backgroundState{}, fmt.Errorf("parse background state: %w", err)
	}
	return state, nil
}

func saveBackgroundState(state backgroundState) error {
	path, err := backgroundStatePath()
	if err != nil {
		return err
	}
	if state.Server == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove background state: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create background state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode background state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".background-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary background state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary background state permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary background state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary background state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save background state: %w", err)
	}
	return nil
}

func backgroundStatePath() (string, error) {
	dir, err := backgroundDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, backgroundStateFileName), nil
}

func backgroundLogPath(instanceID string) (string, error) {
	dir, err := backgroundDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs", instanceID+".log"), nil
}

func backgroundDataDir() (string, error) {
	cacheDir, err := backgroundCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "comet"), nil
}

func (s backgroundServer) baseURL() string {
	return serveBaseURL(s.Host, s.Port)
}

func (s backgroundServer) accessURL() string {
	if s.AuthToken == "" {
		return s.baseURL()
	}
	return serveURLWithToken(s.baseURL(), s.AuthToken)
}

func (s backgroundServer) healthURL() string {
	return s.baseURL() + "/api/healthz"
}
