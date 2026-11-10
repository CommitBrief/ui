# CommitBrief UI

Cross-platform desktop companion for the [CommitBrief](https://github.com/CommitBrief/commitbrief)
CLI. It drives the `commitbrief` binary as a subprocess: manage repositories, run
reviews, and schedule a background bot from the system tray.

> **Status: early development.** This repository currently contains the project
> skeleton: an empty window, a system tray (Show / Quit) and single-instance
> handling. There is nothing to install yet.

## Requirements

| | |
|---|---|
| Go | 1.26 or later |
| Node.js | 22 or later (frontend build) |
| Wails CLI | v3.1.0 (`make tools` installs the pinned version) |
| macOS | Xcode command line tools |
| Windows | WebView2 runtime, a C toolchain (CGO) |
| Linux | GTK 4 and WebKitGTK 6.0 development packages, `build-essential`, `pkg-config` |

On Debian/Ubuntu: `sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`.

## Develop

```sh
make tools       # install wails3 (pinned) and go-licenses into $(go env GOPATH)/bin
make dev         # run the app with hot reload
make build       # produce bin/commitbrief-ui
make check       # every gate CI runs: gofmt, vet, golangci-lint, tests, license, SPDX, i18n parity, frontend lint/typecheck, bindings drift
```

`make help` lists every target. Go code embeds `frontend/dist`, so the targets
that compile Go run the frontend build first.

## Layout

```
cmd/commitbrief-ui/      composition root
internal/domain/         pure business model (standard library only)
internal/app/            use cases (domain + port only)
internal/port/           interfaces the core needs from the outside
internal/adapter/        Wails shell, CLI runner, GitHub, SQLite, ...
internal/platform/       OS-specific helpers
frontend/                React + TypeScript + Vite
```

Layer boundaries are enforced by `depguard` in `.golangci.yml`; see
`make lint`.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
