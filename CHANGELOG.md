# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Desktop shell for macOS, Windows and Linux (best effort) with a system tray (Show and Quit), single-instance handling and a scheduler that keeps running when the window is closed.
- Backend service for repository management, backed by a local SQLite database (screen not yet available).
- Detection of the `commitbrief` CLI (Settings path, login shell `PATH`, common install locations) and per-version capability checks, so features the installed CLI cannot support are disabled instead of failing.
- Backend service for CLI configuration, the CLI path and GitHub CLI (`gh`) status (screen not yet available). API keys are written through standard input and never appear on a command line.
- Backend service for manual review jobs on local changes and pull requests, with live progress and run logs (screen not yet available).
- Backend scheduler and review-requested bot that finds pull requests through `gh` and reviews them on a schedule (screen not yet available).
- In-app updates from GitHub Releases with Stable and Pre-release channels. An update is installed only if its SHA-256 matches `checksums.txt`.
- Release workflow producing signed macOS and Linux packages and unsigned Windows packages (Windows SmartScreen shows an "unknown publisher" warning on the first run).
- English and Turkish interface, design tokens, base components and the application shell.

### Security

- The webview enforces a strict Content Security Policy, and only a restricted set of Wails runtime calls is reachable from it.
- Credential-shaped values are masked in logs.

[Unreleased]: https://github.com/CommitBrief/ui/commits/main
