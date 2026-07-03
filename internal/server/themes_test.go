package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGhosttyTheme(t *testing.T) {
	t.Parallel()

	content := `palette = 0=#111111
palette = 1=#222222
palette = 2=#333333
palette = 3=#444444
palette = 4=#555555
palette = 5=#666666
palette = 6=#777777
palette = 7=#888888
palette = 8=#999999
palette = 9=#aaaaaa
palette = 10=#bbbbbb
palette = 11=#cccccc
palette = 12=#dddddd
palette = 13=#eeeeee
palette = 14=#abcdef
palette = 15=#fedcba
background = #101010
foreground = #f0f0f0
cursor-color = #123456
cursor-text = #654321
selection-background = #202020
selection-foreground = #eeeeee
`

	theme, ok := parseGhosttyTheme("Example", "test", strings.NewReader(content))
	if !ok {
		t.Fatal("parseGhosttyTheme() rejected valid theme")
	}
	if theme.Name != "Example" || theme.Source != "test" {
		t.Fatalf("theme metadata = %#v", theme)
	}
	if theme.Colors.Background != "#101010" || theme.Colors.Foreground != "#f0f0f0" || theme.Colors.BrightCyan != "#abcdef" {
		t.Fatalf("colors = %#v", theme.Colors)
	}
}

func TestParseGhosttyThemeRejectsIncompleteTheme(t *testing.T) {
	t.Parallel()

	if _, ok := parseGhosttyTheme("Broken", "test", strings.NewReader("background = #000000\nforeground = #ffffff")); ok {
		t.Fatal("parseGhosttyTheme() accepted incomplete palette")
	}
}

func TestThemesEndpointIncludesBundledThemes(t *testing.T) {
	t.Parallel()

	s := &Server{config: &Config{}}
	recorder := httptest.NewRecorder()
	s.handleThemes(recorder, httptest.NewRequest("GET", "/api/themes", nil))

	if recorder.Code != 200 {
		t.Fatalf("status = %d body=%q", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"name":"Comet Warm"`) || !strings.Contains(body, `"name":"Catppuccin Mocha"`) {
		t.Fatalf("themes body missing bundled themes: %.200q", body)
	}
}
