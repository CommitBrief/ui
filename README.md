# CommitBrief UI

A desktop app that runs CommitBrief code reviews on local repositories and pull requests, on demand or on a schedule.

[![CI](https://github.com/CommitBrief/ui/actions/workflows/ci.yml/badge.svg)](https://github.com/CommitBrief/ui/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/CommitBrief/ui)](LICENSE)
[![Designed & Maintained with Tan](https://muhammetsafak.com/badges/designed-maintained-with-tan.svg)](https://muhammetsafak.com/tan/)

CommitBrief UI is a companion for the [CommitBrief](https://github.com/CommitBrief/commitbrief) command-line tool. It does not review code itself: it runs the `commitbrief` binary as a subprocess. It is being built to manage repositories, review jobs, a review-requested bot and updates around it; see [Project Status](#project-status) for what exists today. It runs on macOS and Windows, and on Linux on a best-effort basis.

## Quick Start

There is no release yet, so the app is built from source. You need:

- Go 1.26 or later and Node.js 22 (the version CI uses).
- The [`commitbrief` CLI](https://github.com/CommitBrief/commitbrief) v1.21.2 or later. Posting review comments to GitHub and storing API keys from the UI will need a newer CLI release.
- The [GitHub CLI](https://cli.github.com/) (`gh`), logged in, for pull request reviews and the bot.
- `make` (on Windows, for example `choco install make`).
- A native toolchain: Xcode command line tools on macOS; a C toolchain (CGO) and the WebView2 runtime on Windows; GTK 4 and WebKitGTK 6.0 on Linux (see [CONTRIBUTING.md](CONTRIBUTING.md#development-setup)).

```bash
git clone https://github.com/CommitBrief/ui.git
cd ui
make tools
export PATH="$(go env GOPATH)/bin:$PATH"
make build
```

`make tools` installs the pinned `wails3` CLI into your Go bin directory. On Windows, use the equivalent PATH setting for your shell and run the binary as `bin\commitbrief-ui.exe`.

Start the app:

```bash
./bin/commitbrief-ui
```

A window opens and a tray icon with **Show** and **Quit** entries appears. Closing the window does not quit the app, so the scheduler keeps running; use **Quit** in the tray menu to exit.

## Usage

The application is in early development, and most screens are not built yet (see [Project Status](#project-status)). What works today:

- **Updates screen:** shows the running version, checks GitHub Releases for a newer one and installs it. The Stable channel follows releases only; the Pre-release channel also follows pre-releases.
- **Tray and window:** one instance runs at a time; starting a second one focuses the existing window.

The CLI is located through the path set in Settings, the `PATH` of your login shell and the app, and common install locations.

## How It Works

The core of the app is written in Go and talks to the outside world through narrow interfaces. Reviews run by invoking `commitbrief` with its `--json` output; GitHub pull requests are found and read through `gh`. State (repositories, jobs, runs, schedules) is kept in a local SQLite database. The interface is a React and TypeScript frontend rendered in a Wails v3 webview, with a Content Security Policy and a restricted runtime surface.

## Upgrading

Once releases are published, the Updates screen installs a newer release in place, except for `.deb` and `.rpm` installs, which the package manager updates. An update is installed only if its SHA-256 matches the entry in the release's `checksums.txt`. That check protects against a corrupt or truncated download. It does not protect against someone who can write to the release, and the app does not verify the operating-system signature when it updates itself. The accepted risk is described in [SECURITY.md](SECURITY.md) and [docs/RELEASING.md](docs/RELEASING.md).

## Documentation

- [Releasing](docs/RELEASING.md): the maintainer's runbook for secrets, test releases and taking a release back.
- [Contributing](CONTRIBUTING.md): requirements, commands and the repository layout.

## Project Status

**Experimental.** Version 0.1.0 has not been released and there are no installers. The services behind repositories, review jobs, schedules, the review-requested bot, settings and updates exist in the code, but the Repos, Jobs, Schedules and Settings screens are placeholders until their designs are approved; only the Updates screen is implemented. Expect breaking changes until a stable release.

## Contributing

Contributions are welcome. Read the [contributing guide](CONTRIBUTING.md) before opening an issue or pull request.

## Security

Do not report security vulnerabilities through public issues; follow the [security policy](SECURITY.md) instead.

## License

Licensed under the [GPL-3.0-or-later](LICENSE) license.
