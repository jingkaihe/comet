package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ghosttyThemeDiscoveryTimeout = 2 * time.Second

type TerminalThemeColors struct {
	Foreground          string `json:"foreground"`
	Background          string `json:"background"`
	Cursor              string `json:"cursor"`
	CursorAccent        string `json:"cursorAccent"`
	SelectionBackground string `json:"selectionBackground"`
	SelectionForeground string `json:"selectionForeground"`
	Black               string `json:"black"`
	Red                 string `json:"red"`
	Green               string `json:"green"`
	Yellow              string `json:"yellow"`
	Blue                string `json:"blue"`
	Magenta             string `json:"magenta"`
	Cyan                string `json:"cyan"`
	White               string `json:"white"`
	BrightBlack         string `json:"brightBlack"`
	BrightRed           string `json:"brightRed"`
	BrightGreen         string `json:"brightGreen"`
	BrightYellow        string `json:"brightYellow"`
	BrightBlue          string `json:"brightBlue"`
	BrightMagenta       string `json:"brightMagenta"`
	BrightCyan          string `json:"brightCyan"`
	BrightWhite         string `json:"brightWhite"`
}

type TerminalTheme struct {
	Name   string              `json:"name"`
	Source string              `json:"source"`
	Colors TerminalThemeColors `json:"colors"`
}

func (s *Server) handleThemes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.terminalThemes())
}

func (s *Server) terminalThemes() []TerminalTheme {
	s.themesOnce.Do(func() {
		s.themes = collectTerminalThemes()
	})
	return append([]TerminalTheme(nil), s.themes...)
}

func AvailableTerminalThemes() []TerminalTheme {
	return collectTerminalThemes()
}

func collectTerminalThemes() []TerminalTheme {
	themesByName := map[string]TerminalTheme{}
	for _, theme := range bundledTerminalThemes() {
		themesByName[themeKey(theme.Name)] = theme
	}

	for _, path := range discoverGhosttyThemeFiles() {
		theme, ok := readGhosttyThemeFile(path, "ghostty")
		if ok {
			themesByName[themeKey(theme.Name)] = theme
		}
	}

	themes := make([]TerminalTheme, 0, len(themesByName))
	for _, theme := range themesByName {
		themes = append(themes, theme)
	}
	sort.Slice(themes, func(i, j int) bool {
		if themes[i].Name == defaultTerminalThemeName {
			return true
		}
		if themes[j].Name == defaultTerminalThemeName {
			return false
		}
		if themes[i].Source != themes[j].Source {
			return themes[i].Source == "ghostty"
		}
		return strings.ToLower(themes[i].Name) < strings.ToLower(themes[j].Name)
	})
	return themes
}

func discoverGhosttyThemeFiles() []string {
	seen := map[string]struct{}{}
	paths := []string{}
	add := func(path string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		paths = append(paths, abs)
	}

	for _, path := range ghosttyListThemePaths() {
		add(path)
	}
	for _, dir := range ghosttyThemeDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				add(filepath.Join(dir, entry.Name()))
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func ghosttyListThemePaths() []string {
	ctx, cancel := context.WithTimeout(context.Background(), ghosttyThemeDiscoveryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ghostty", "+list-themes", "--plain", "--path")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil
	}

	paths := []string{}
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		if path := parseGhosttyListThemePath(scanner.Text()); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func parseGhosttyListThemePath(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if fileExists(trimmed) {
		return trimmed
	}
	if tab := strings.LastIndex(trimmed, "\t"); tab >= 0 {
		if candidate := strings.TrimSpace(trimmed[tab+1:]); fileExists(candidate) {
			return candidate
		}
	}
	if slash := strings.Index(trimmed, string(filepath.Separator)); slash >= 0 {
		if candidate := strings.TrimSpace(trimmed[slash:]); fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ghosttyThemeDirs() []string {
	dirs := []string{}
	if xdgConfig := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgConfig != "" {
		dirs = append(dirs, filepath.Join(xdgConfig, "ghostty", "themes"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "ghostty", "themes"))
	}

	if resourcesDir := strings.TrimSpace(os.Getenv("GHOSTTY_RESOURCES_DIR")); resourcesDir != "" {
		dirs = append(dirs,
			filepath.Join(resourcesDir, "ghostty", "themes"),
			filepath.Join(resourcesDir, "themes"),
		)
	}

	dirs = append(dirs,
		"/usr/share/ghostty/themes",
		"/usr/local/share/ghostty/themes",
		"/opt/homebrew/share/ghostty/themes",
		"/Applications/Ghostty.app/Contents/Resources/ghostty/themes",
	)
	return dirs
}

func readGhosttyThemeFile(path string, source string) (TerminalTheme, bool) {
	file, err := os.Open(path)
	if err != nil {
		return TerminalTheme{}, false
	}
	defer file.Close()

	name := strings.TrimSpace(filepath.Base(path))
	return parseGhosttyTheme(name, source, file)
}

func parseGhosttyTheme(name string, source string, reader io.Reader) (TerminalTheme, bool) {
	palette := [16]string{}
	values := map[string]string{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "palette" {
			indexRaw, colorRaw, ok := strings.Cut(value, "=")
			if !ok {
				continue
			}
			index, err := strconv.Atoi(strings.TrimSpace(indexRaw))
			if err != nil || index < 0 || index >= len(palette) {
				continue
			}
			if color, ok := normalizeHexColor(colorRaw); ok {
				palette[index] = color
			}
			continue
		}
		if color, ok := normalizeHexColor(value); ok {
			values[key] = color
		}
	}

	if strings.TrimSpace(name) == "" || values["background"] == "" || values["foreground"] == "" {
		return TerminalTheme{}, false
	}
	for _, color := range palette {
		if color == "" {
			return TerminalTheme{}, false
		}
	}

	return newTerminalTheme(
		name,
		source,
		palette,
		values["background"],
		values["foreground"],
		firstNonEmpty(values["cursor-color"], values["cursor"], values["foreground"]),
		firstNonEmpty(values["cursor-text"], values["background"]),
		firstNonEmpty(values["selection-background"], palette[8], values["foreground"]),
		firstNonEmpty(values["selection-foreground"], values["background"]),
	), true
}

func normalizeHexColor(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.Trim(value, "\"'"))
	trimmed = strings.TrimPrefix(trimmed, "#")
	if len(trimmed) == 3 {
		trimmed = string([]byte{trimmed[0], trimmed[0], trimmed[1], trimmed[1], trimmed[2], trimmed[2]})
	}
	if len(trimmed) != 6 {
		return "", false
	}
	for _, r := range trimmed {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return "", false
		}
	}
	return "#" + strings.ToLower(trimmed), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func themeKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

const defaultTerminalThemeName = "Comet Warm"

func bundledTerminalThemes() []TerminalTheme {
	return []TerminalTheme{
		newTerminalTheme(defaultTerminalThemeName, "bundled", [16]string{
			"#171512", "#df7c5e", "#8ea267", "#cfb37a", "#7eabd8", "#b795b9", "#87b7b1", "#efe6d7",
			"#635b4f", "#f29b80", "#a6bf79", "#e7c98d", "#99c0e6", "#cfadd0", "#a5d0ca", "#fffaf1",
		}, "#18140f", "#f4eee3", "#d97757", "#171512", "#624733", "#fffaf1"),
		newTerminalTheme("Catppuccin Mocha", "bundled", [16]string{
			"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
			"#585b70", "#f7aec2", "#c2ecbf", "#fcd682", "#aeccfc", "#f398da", "#b1eae1", "#a6adc8",
		}, "#1e1e2e", "#cdd6f4", "#f5e0dc", "#1e1e2e", "#f5e0dc", "#1e1e2e"),
		newTerminalTheme("Catppuccin Latte", "bundled", [16]string{
			"#bcc0cc", "#d20f39", "#40a02b", "#df8e1d", "#1e66f5", "#ea76cb", "#179299", "#5c5f77",
			"#acb0be", "#e7103f", "#46b02f", "#e49931", "#3878f6", "#ef95d7", "#19a1a8", "#6c6f85",
		}, "#eff1f5", "#4c4f69", "#dc8a78", "#eff1f5", "#dc8a78", "#eff1f5"),
		newTerminalTheme("Dracula", "bundled", [16]string{
			"#21222c", "#ff5555", "#50fa7b", "#f1fa8c", "#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
			"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5", "#d6acff", "#ff92df", "#a4ffff", "#ffffff",
		}, "#282a36", "#f8f8f2", "#f8f8f2", "#282a36", "#44475a", "#ffffff"),
		newTerminalTheme("Gruvbox Dark", "bundled", [16]string{
			"#282828", "#cc241d", "#98971a", "#d79921", "#458588", "#b16286", "#689d6a", "#a89984",
			"#928374", "#fb4934", "#b8bb26", "#fabd2f", "#83a598", "#d3869b", "#8ec07c", "#ebdbb2",
		}, "#282828", "#ebdbb2", "#ebdbb2", "#282828", "#665c54", "#ebdbb2"),
		newTerminalTheme("Nord", "bundled", [16]string{
			"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
			"#596377", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
		}, "#2e3440", "#d8dee9", "#eceff4", "#282828", "#eceff4", "#4c566a"),
		newTerminalTheme("TokyoNight Night", "bundled", [16]string{
			"#15161e", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#a9b1d6",
			"#414868", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#c0caf5",
		}, "#1a1b26", "#c0caf5", "#c0caf5", "#1a1b26", "#283457", "#c0caf5"),
		newTerminalTheme("iTerm2 Solarized Dark", "bundled", [16]string{
			"#073642", "#dc322f", "#859900", "#b58900", "#268bd2", "#d33682", "#2aa198", "#eee8d5",
			"#335e69", "#cb4b16", "#586e75", "#657b83", "#839496", "#6c71c4", "#93a1a1", "#fdf6e3",
		}, "#002b36", "#839496", "#839496", "#073642", "#073642", "#93a1a1"),
	}
}

func newTerminalTheme(name string, source string, palette [16]string, background string, foreground string, cursor string, cursorAccent string, selectionBackground string, selectionForeground string) TerminalTheme {
	return TerminalTheme{
		Name:   name,
		Source: source,
		Colors: TerminalThemeColors{
			Foreground:          foreground,
			Background:          background,
			Cursor:              cursor,
			CursorAccent:        cursorAccent,
			SelectionBackground: selectionBackground,
			SelectionForeground: selectionForeground,
			Black:               palette[0],
			Red:                 palette[1],
			Green:               palette[2],
			Yellow:              palette[3],
			Blue:                palette[4],
			Magenta:             palette[5],
			Cyan:                palette[6],
			White:               palette[7],
			BrightBlack:         palette[8],
			BrightRed:           palette[9],
			BrightGreen:         palette[10],
			BrightYellow:        palette[11],
			BrightBlue:          palette[12],
			BrightMagenta:       palette[13],
			BrightCyan:          palette[14],
			BrightWhite:         palette[15],
		},
	}
}
