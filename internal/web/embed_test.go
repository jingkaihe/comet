package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestDistFSUsesGeneratedFrontendWhenAvailable(t *testing.T) {
	t.Parallel()

	distFS, err := DistFS()
	if err != nil {
		t.Fatalf("DistFS() error = %v", err)
	}

	content, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	if !strings.Contains(string(content), `<div id="app"></div>`) {
		t.Fatalf("index.html does not look like generated app: %.120q", string(content))
	}
}

func TestDistFSEmbedsViteUnderscoreAssets(t *testing.T) {
	t.Parallel()

	distFS, err := DistFS()
	if err != nil {
		t.Fatalf("DistFS() error = %v", err)
	}

	entries, err := fs.ReadDir(distFS, "assets")
	if err != nil {
		t.Fatalf("ReadDir(assets) error = %v", err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "__vite-browser-external") {
			return
		}
	}

	t.Fatalf("generated Vite underscore asset was not embedded; entries=%v", entries)
}
