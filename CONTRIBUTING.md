# Contributing to CommitBrief UI

Thank you for your interest in contributing. This guide explains how to propose changes.

## Before You Start

- Small fixes (typos, broken links, obvious bugs) can be sent directly as a pull request.
- For a new feature or a change in behavior, open an issue first so the approach can be discussed before you write code.
- Opening a pull request does not guarantee that it will be accepted.
- Contributions are licensed under the project's GPL-3.0-or-later license. There is no CLA and no DCO sign-off.
- Do not report security vulnerabilities in issues or pull requests; follow the [security policy](SECURITY.md).

## Development Setup

Requirements:

- Go 1.26 or later (`go.mod`).
- Node.js 22 and npm (CI uses Node.js 22).
- `make`, plus the pinned `wails3` CLI v3.1.0 and `go-licenses` v1.6.0, which `make tools` installs into `$(go env GOPATH)/bin`. Put that directory on `PATH`.
- `golangci-lint` v2.15.0, the version CI uses.
- macOS: Xcode command line tools.
- Windows: a C toolchain (CGO) and the WebView2 runtime.
- Linux (best effort): GTK 4 and WebKitGTK 6.0. On Debian or Ubuntu 24.04 and newer: `sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`. Distributions that only ship GTK 3 and WebKitGTK 4.1 are not supported.
- Optional: `actionlint`, used by `make release-check` when installed.

Clone the repository and install the tools:

```bash
git clone https://github.com/CommitBrief/ui.git
cd ui
make tools
export PATH="$(go env GOPATH)/bin:$PATH"
```

Run the app with hot reload, or build the binary into `bin/`:

```bash
make dev
```

```bash
make build
```

`make help` lists every target. Go code embeds `frontend/dist`, so the targets that compile Go run the frontend build first.

### Project Layout

```text
cmd/commitbrief-ui/      composition root
internal/domain/         pure business model (standard library only)
internal/app/            use cases (domain and port only)
internal/port/           interfaces the core needs from the outside
internal/adapter/        Wails shell, CLI runner, GitHub, SQLite, updater, ...
internal/platform/       OS-specific helpers
frontend/                React, TypeScript and Vite
build/                   Wails build assets and per-platform packaging tasks
scripts/                 checks and tooling used by make check and the release workflow
docs/                    maintainer documentation
```

Layer boundaries are enforced by `depguard` in `.golangci.yml`; `make lint` reports violations.

## Running Tests

These are the same commands that CI runs on Linux, macOS and Windows. Run both before opening a pull request.

```bash
make check
```

```bash
make build
```

`make check` runs gofmt drift, the frontend build, `go vet`, `golangci-lint`, the layering self-test, Go tests, the license, SPDX header and i18n parity checks, frontend lint and typecheck, Wails bindings drift and the release tooling self-test. Every new Go file needs the SPDX header, and every user-visible string needs a key in both `frontend/src/i18n/en.json` and `frontend/src/i18n/tr.json`.

If `make check` reports bindings drift, regenerate and commit the bindings:

```bash
wails3 generate bindings -clean=true -ts -i . ./cmd/... ./internal/...
```

## Commit Messages

This project follows [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/):

```text
<type>(<scope>)!: <description>

<body>
```

- **Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.
- **Scope** is optional, lowercase kebab-case, and names a module or area: `feat(updater): ...`.
- **Description** is in English, imperative mood, starts with a lowercase letter, has no trailing period and is at most 72 characters.
- **Body** is optional and explains why the change was made. Wrap lines at 72 characters.
- Mark a breaking change only with `!` after the type or scope, and describe it in the body.
- Do not add footers or trailers (`BREAKING CHANGE:`, `Refs:`, `Closes #12`, `Co-Authored-By:`, `Signed-off-by:` or any tool attribution). Link issues in the pull request description instead.
- Keep one logical change per commit.

Example:

```text
fix(repos): reject relative paths when adding a repository
```

## Pull Requests

1. Create a branch from `main` named `<type>/<short-kebab-description>`, for example `feat/token-refresh` or `fix/123-null-slug-404`.
2. Write the pull request title in Conventional Commits format. Pull requests are squash merged, so the title becomes the commit message on `main`.
3. If the change affects users, add an entry under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md).
4. Fill in the pull request template and link the related issue with `Closes #<number>`.
5. Make sure CI passes. A maintainer reviews the pull request and may ask for changes before merging.

## Code of Conduct

This project is governed by the [Code of Conduct](https://github.com/CommitBrief/.github/blob/HEAD/CODE_OF_CONDUCT.md). By participating, you agree to uphold it.
