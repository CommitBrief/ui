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
| Linux (best effort) | GTK 4 and WebKitGTK 6.0 (`libgtk-4-dev`, `libwebkitgtk-6.0-dev`), `build-essential`, `pkg-config` |

**Linux is best effort.** CommitBrief UI uses the Wails v3 default on Linux, which
is GTK 4 with WebKitGTK 6.0. That needs a recent distribution (Ubuntu 24.04 or
newer, or equivalent); older distributions that only ship GTK 3 /
WebKitGTK 4.1 are not supported. macOS and Windows are the primary targets.

On Debian/Ubuntu: `sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`.

## Develop

```sh
make tools       # install wails3 (pinned) and go-licenses into $(go env GOPATH)/bin
make dev         # run the app with hot reload
make build       # produce bin/commitbrief-ui
make check       # every gate CI runs: gofmt, vet, golangci-lint, tests, license, SPDX, i18n parity, frontend lint/typecheck, bindings drift, release tooling self-test
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

## Releases

Tagged releases are built, signed (macOS Developer ID and notarization, Windows
Authenticode, Linux PGP) and published by `.github/workflows/release.yml`; the app
updates itself from them, installing an update only if it matches the SHA-256 in
the release's `checksums.txt` (that guards against a corrupt download, not against
someone who can write to the release; the operating-system signature is checked at
the first install, not when the app updates itself). The maintainer's runbook (secrets, test
releases, taking a release back) is [docs/RELEASING.md](docs/RELEASING.md).

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
