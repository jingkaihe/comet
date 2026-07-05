# Comet

Comet is a small Go web terminal server. It starts a localhost browser UI with Ghostty's terminal renderer, tabs, tab naming, and pane splits backed by real PTY sessions on the Go server.

## Usage

```sh
comet serve
comet list-themes
comet serve --host localhost --port 6174
comet serve --theme Dracula
comet serve --auth-token-file ~/.config/comet/token
comet serve --skip-auth
comet serve --background
comet status
comet down
```

By default, `comet serve` binds to `localhost:6174`, generates an access token, and prints a tokenized URL. Use `--auth-token-file` to read a stable token from a file, or `--skip-auth` for local development. Use `comet serve --background` to detach the server from the current terminal; `comet status` prints the running background server and access URL, and `comet down` stops it.

## Terminal UI

- Click `+` to open a new terminal tab.
- Double-click a tab to rename it.
- Tab titles follow the terminal: programs that set the title with OSC 0/2 escape sequences (e.g. coding agents animating a spinner) update the Comet tab and browser tab title instantly; otherwise the foreground command or working directory is shown. Renamed tabs keep their custom name.
- Pick a terminal theme from the bottom bar. Comet reads local Ghostty-format themes from `~/.config/ghostty/themes/`. Use `comet list-themes` to see valid names, and `comet serve --theme <name>` to start with a default theme other than Comet Warm.
- macOS: `Cmd+T` opens a tab, `Cmd+W` closes the active Comet tab, `Cmd+D` creates a vertical split, `Cmd+Shift+D` creates a horizontal split, and `Cmd+Option+Arrow` switches panes. `Ctrl+Shift+T` and `Ctrl+Shift+W` are also supported as browser-safe fallbacks.
- Linux/Windows: `Ctrl+Shift+T` opens a tab, `Ctrl+Shift+W` closes the active Comet tab, `Ctrl+Shift+D` creates a vertical split, `Ctrl+Alt+D` creates a horizontal split, and `Ctrl+Alt+Arrow` switches panes.

To install the Ghostty themes from [`iTerm2-Color-Schemes`](https://github.com/mbadolato/iTerm2-Color-Schemes/tree/master/ghostty):

```sh
tmp=$(mktemp -d) && git clone --depth=1 https://github.com/mbadolato/iTerm2-Color-Schemes.git "$tmp/iTerm2-Color-Schemes" && mkdir -p ~/.config/ghostty/themes && cp -R "$tmp/iTerm2-Color-Schemes/ghostty/." ~/.config/ghostty/themes/ && rm -rf "$tmp"
```

## Development

Developer workflows, build/test commands, embedded frontend asset details, and release steps are documented in `AGENTS.md`.
