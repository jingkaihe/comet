package web

import (
	"embed"
	"io/fs"
)

//go:generate bash -c "cd frontend && npm ci && npm run build"
//go:embed all:assets
var embeddedAssets embed.FS

func DistFS() (fs.FS, error) {
	if _, err := embeddedAssets.Open("assets/dist/index.html"); err == nil {
		return fs.Sub(embeddedAssets, "assets/dist")
	}

	return fs.Sub(embeddedAssets, "assets")
}
