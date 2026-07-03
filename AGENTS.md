# AGENTS.md

This file gives kodelet project-specific context for working in this repository. It is based on the repository files, README, build config, workflows, and existing code patterns.

## Project Overview

Comet is a small Go web terminal server. The `comet serve` command starts a localhost browser UI that uses Ghostty's terminal renderer in the frontend and real PTY-backed shell sessions on the Go server. The UI supports tabs, tab naming, pane splits, theme selection, and persisted layout state.

See `README.md` for user-facing usage. Keep development, build, test, and release workflow details here rather than expanding the README.

## Project Structure

- `cmd/comet/` - CLI entrypoint; `main.go` calls `internal/cli.Execute()`.
- `internal/cli/` - Cobra command definitions and CLI-level validation/output for `serve` and `version`.
- `internal/server/` - HTTP server, token auth, JSON APIs, WebSocket terminal transport, PTY session management, layout persistence, and terminal theme discovery/parsing.
- `internal/web/` - Go embed wrapper for web assets. `embed.go` contains the `go:generate` command that builds the frontend.
- `internal/web/assets/` - fallback embedded HTML plus generated `assets/dist` output after frontend build. `assets/dist` is ignored by git.
- `internal/web/frontend/` - Vite/TypeScript frontend source, tests, and npm dependency lockfile.
- `internal/version/` - build-time version variables set through `-ldflags` in `mise.toml` and GoReleaser.
- `.github/workflows/` - CI test/build workflow and tag-triggered release workflow.
- `mise.toml` - source of truth for local tool versions and common development tasks.
- `.goreleaser.yaml` - release artifact configuration for Linux/macOS binaries and Linux packages.

## Tech Stack

- Go `1.26.2` module `github.com/jingkaihe/comet`.
- Go libraries: `spf13/cobra` for CLI commands, `gorilla/websocket` for terminal WebSockets, `creack/pty` for PTY-backed shell sessions, standard `net/http`, `embed`, `slog`, and synchronization primitives.
- Frontend: TypeScript `strict` mode, Vite, Vitest, and `ghostty-web` WASM terminal rendering.
- Tooling: `mise` manages Go, Node/npm, `tsx`, `gofumpt`, `golangci-lint`, `goreleaser`, `gh`, `ripgrep`, and `fd`.
- Release: GoReleaser v2; `VERSION.txt` is the release version source of truth.

## Architecture Notes

```diagram
╭────────────╮      ╭──────────────╮      ╭────────────────────────╮
│ cmd/comet  │─────▶│ internal/cli │─────▶│ internal/server.Server │
╰────────────╯      ╰──────────────╯      ╰───────────┬────────────╯
                                                      │
                  ╭───────────────────────────────────┼──────────────────────────────────╮
                  ▼                                   ▼                                  ▼
        ╭──────────────────╮             ╭──────────────────────╮           ╭──────────────────╮
        │ embedded Vite UI │             │ JSON layout/themes   │           │ terminal WS API  │
        │ internal/web     │             │ /api/layout,/themes  │           │ /api/terminal/ws │
        ╰──────────────────╯             ╰──────────────────────╯           ╰────────┬─────────╯
                                                                                      ▼
                                                                            ╭────────────────╮
                                                                            │ SessionManager │
                                                                            │ creack/pty     │
                                                                            ╰────────────────╯
```

- `internal/server.New` validates config, loads embedded web assets through `webassets.DistFS()`, creates a `SessionManager` and `LayoutStore`, and registers routes.
- Auth is route-wide middleware. An empty auth token means auth is disabled; otherwise query token, auth cookie, or `Authorization` header can satisfy auth. Token comparisons use constant-time comparison.
- Static frontend files are served from the embedded filesystem. `DistFS()` prefers `internal/web/assets/dist/index.html` when generated, otherwise falls back to `internal/web/assets/index.html` with a generation reminder.
- Terminal panes map to server-side PTY sessions by pane ID. A session can have attachments, keeps a bounded replay buffer, supports input/resize/signal messages, and sends binary PTY output plus JSON control messages.
- WebSocket origin checking allows same-origin browser connections and rejects cross-origin origins.
- The frontend is a framework-free TypeScript app. `CometApp` owns tabs, active pane state, theme selection, persisted layout sync, and keyboard shortcuts. `TerminalPane` owns Ghostty terminal instances, websocket lifecycle, resize fitting, replay suppression, and mouse wheel reporting.
- Layout state is normalized on the server: invalid/duplicate tabs or panes are dropped, pane IDs are re-derived from the layout tree, active pane/tab fallbacks are chosen, stale writes with lower versions are ignored.
- Themes come from bundled themes plus local Ghostty-format theme files discovered from Ghostty commands and common config/resource directories.

## Development Workflows and Commands

Prefer `mise run ...` commands because `mise.toml` pins the expected tool versions.

### Install Dependencies

```sh
mise run install
```

This downloads Go modules and runs `npm install` in `internal/web/frontend`.

### Build

```sh
mise run build
```

`build` depends on `code-generation`, which runs `go generate ./internal/web`. The generate step executes `cd frontend && npm ci && npm run build`, producing Vite output under `internal/web/assets/dist` for embedding. The final binary is `./bin/comet` with version values injected by `-ldflags`.

Use this only when frontend assets should be regenerated:

```sh
go generate ./internal/web
```

For a faster Go-only rebuild that does not regenerate frontend assets:

```sh
mise run build-dev
```

### Run Locally

```sh
mise run serve-dev   # builds, then runs ./bin/comet serve --skip-auth
mise run serve       # builds, then runs ./bin/comet serve
```

The frontend dev server script also exists in `internal/web/frontend/package.json`:

```sh
(cd internal/web/frontend && npm run dev)
```

It runs Vite on `127.0.0.1:6175`. The Go server still owns the backend APIs and terminal WebSocket.

### Test

```sh
mise run test
```

This runs `mise run frontend-test`, regenerates embedded frontend assets, then runs `go test ./...`.

Narrow checks:

```sh
go test ./...
go test ./internal/server
go test ./internal/cli
go test ./internal/web
(cd internal/web/frontend && npm test)
(cd internal/web/frontend && npm run typecheck)
mise run frontend-test
```

Notes:

- Go tests use standard `testing`; most tests call `t.Parallel()`.
- WebSocket PTY integration test `TestTerminalWebSocketUpgradesThroughLoggingMiddleware` is skipped in `go test -short` because it spawns a shell-backed PTY.
- Frontend tests use Vitest and live next to source as `*.test.ts`.
- `internal/web/embed_test.go` expects generated frontend assets to be present. If it fails looking for generated Vite assets, run `go generate ./internal/web` or `mise run test`.

### Format and Lint

```sh
mise run format   # gofumpt -w .
mise run lint     # go generate ./internal/web, go vet ./..., golangci-lint run
```

There is no configured frontend formatter/linter beyond TypeScript strict checks and Vitest in the checked-in files.

### Release/Snapshot

```sh
mise run release-snapshot
mise run release
mise run push-tag
```

`VERSION.txt` is the source of truth for release versions. To publish a release, update `VERSION.txt`, commit the change, and run `mise run push-tag`. That task creates or reuses `v$(cat VERSION.txt)`, pushes it to `origin`, and triggers the tag-based GitHub Actions release workflow for `github.com/jingkaihe/comet`.

`release` depends on `test` and then runs a GoReleaser snapshot. `release-snapshot` runs `goreleaser release --snapshot --clean` directly.

## Dependencies and Generated Files

- Go dependencies are managed with `go.mod` and `go.sum`.
- Frontend dependencies are managed in `internal/web/frontend/package.json` and `package-lock.json`.
- CI and `go generate ./internal/web` use `npm ci`; `mise run install` uses `npm install`.
- Do not commit generated/ignored outputs such as `bin/`, `internal/web/assets/dist/`, `internal/web/frontend/node_modules/`, frontend coverage, or Vite cache.
- If frontend dependency versions change, keep `package-lock.json` consistent.

## Coding Style and Conventions

### Go

- Keep packages aligned with existing boundaries: CLI concerns in `internal/cli`, server/API/session/theme/layout concerns in `internal/server`, embedded asset concerns in `internal/web`.
- Use `gofumpt` formatting via `mise run format`.
- Error messages are lower-case and direct. Wrap underlying errors with context using `fmt.Errorf("...: %w", err)` when propagating across layers.
- Validation helpers return errors instead of exiting. Only the top-level CLI execution path prints the error and calls `os.Exit(1)`.
- HTTP handlers set content type for JSON responses and use `http.Error` for simple error responses.
- Concurrency state uses `sync.Mutex`, `sync.RWMutex`, and `sync.Once` explicitly. Preserve locking/snapshot patterns around sessions, attachments, layout store state, and theme caching.
- When returning stored mutable state, clone slices/trees before returning. Existing layout store code avoids exposing internal mutable state.
- For security-sensitive token comparison, preserve `constantTimeStringEqual` behavior rather than normal string equality.
- Logging currently uses `log/slog` only in HTTP request logging middleware with structured key/value fields.
- Test style: table-driven tests where useful, `t.Run` subtests, `t.Parallel()` for independent tests, `httptest` for handlers, and substring checks for expected error text.

### TypeScript/Frontend

- Source is strict TypeScript ES modules under `internal/web/frontend/src`.
- Imports include explicit relative module paths without `.js` extensions, matching current Vite/TS config.
- Use small pure exported helpers in `model.ts`/`terminal-pane.ts` for logic that can be unit tested without browser setup.
- Runtime type guards validate server or persisted data before use (`isTerminalTheme`, `isLayoutNode`, `isTerminalServerEvent`). Preserve this pattern for data from JSON, local/persisted state, or WebSocket messages.
- DOM is built imperatively in `CometApp` and `TerminalPane`; there is no frontend framework.
- WebSocket control messages are JSON; PTY output is binary `ArrayBuffer`/`Uint8Array` data.
- Keep terminal input suppressed until replay completion logic releases it; this avoids replayed output racing with user input.
- CSS is a single stylesheet using custom properties and class-based state such as `.is-active` and `.has-error`.
- Tests use Vitest `describe`/`it`/`expect`; browser-dependent internals are tested with lightweight object stubs when needed.

## Naming Patterns

- Go exported types/functions use standard PascalCase; unexported helpers use camelCase.
- Go test names are `Test...` and often name the function or behavior under test.
- JSON field names intentionally use lower camelCase for frontend compatibility (`activePaneId`, `customTitle`, etc.). Keep Go struct tags and TypeScript interfaces synchronized.
- Frontend IDs use prefixes such as `tab-` and `pane-`; `seedCountersFromTabs` relies on numeric suffixes.
- Terminal message `type` values are string literals such as `ready`, `replay-complete`, `exit`, `info`, `error`, `input`, `resize`, and `signal`. Update Go and TypeScript together when changing this protocol.

## Integration Guidelines

- No `.cursor/rules/`, `.cursorrules`, or `.github/copilot-instructions.md` files were present when this file was created.
- GitHub Actions test workflow runs frontend npm install/test, `go generate ./internal/web`, Go tests, Go build, and a GoReleaser snapshot. Keep local verification aligned with that sequence when changing build, frontend, embedded assets, or release behavior.
- The release workflow is tag-triggered for `v*` tags and uses GoReleaser. `VERSION.txt` is loaded into the environment and is the documented source of truth for release versions.
- Be careful with auth, terminal sessions, process signaling, PTY lifecycle, and WebSocket origin checks; these are user-facing and security-sensitive surfaces.
