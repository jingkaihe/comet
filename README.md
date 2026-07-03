# Comet

Comet is a small Go web terminal server. It starts a localhost browser UI with
Ghostty's terminal renderer, tabs, tab naming, and pane splits backed by real
PTY sessions on the Go server.

## Usage

```sh
comet serve
comet serve --host localhost --port 6174
comet serve --skip-auth
```

By default, `comet serve` binds to `localhost:6174`, generates an access token,
and prints a tokenized URL. Use `--skip-auth` for local development.

## Terminal UI

- Click `+` to open a new terminal tab.
- Double-click a tab to rename it.
- Pick a terminal theme from the top bar. Comet reads local Ghostty-format themes from `~/.config/ghostty/themes/`.
- macOS: `Cmd+T` opens a tab, `Cmd+W` closes the active Comet tab, `Cmd+D` creates a vertical split, and `Cmd+Shift+D` creates a horizontal split. `Ctrl+Shift+T` and `Ctrl+Shift+W` are also supported as browser-safe fallbacks.
- Linux/Windows: `Ctrl+Shift+T` opens a tab, `Ctrl+Shift+W` closes the active Comet tab, `Ctrl+Shift+D` creates a vertical split, and `Ctrl+Alt+D` creates a horizontal split.

To install the Ghostty themes from
[`iTerm2-Color-Schemes`](https://github.com/mbadolato/iTerm2-Color-Schemes/tree/master/ghostty):

```sh
tmp=$(mktemp -d) && git clone --depth=1 https://github.com/mbadolato/iTerm2-Color-Schemes.git "$tmp/iTerm2-Color-Schemes" && mkdir -p ~/.config/ghostty/themes && cp -R "$tmp/iTerm2-Color-Schemes/ghostty/." ~/.config/ghostty/themes/ && rm -rf "$tmp"
```

## Development

```sh
mise run install             # install Go/npm dependencies
mise run test                # frontend tests/typecheck + embedded assets + Go tests
mise run build               # build ./bin/comet with embedded web UI
mise run serve-dev           # run ./bin/comet serve --skip-auth

# or run the underlying commands directly:
go generate ./internal/web   # npm ci + vite build frontend into embedded assets
go test ./...
go build ./cmd/comet

(cd internal/web/frontend && npm test)
```

The Go binary embeds generated assets under `internal/web/assets/dist`, which is
produced by the Vite frontend build and includes the `ghostty-web` WebAssembly
assets. That generated directory is intentionally ignored by git.

## Releases

`VERSION.txt` is the source of truth for release versions. To publish a release,
update `VERSION.txt`, commit the change, and run:

```sh
mise run push-tag
```

That creates/pushes `v$(cat VERSION.txt)` and triggers the GitHub Actions release
workflow for `github.com/jingkaihe/comet`.
