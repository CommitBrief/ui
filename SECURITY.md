# Security Policy

## Supported Versions

CommitBrief UI has not published a release yet and is at 0.y.z. Only the latest release will receive security fixes.

| Version | Supported |
|---|---|
| Latest release (none published yet) | Yes |
| Older versions | No |

## Reporting a Vulnerability

Do not report security vulnerabilities through public issues, pull requests or discussions.

Report vulnerabilities privately through GitHub Private Vulnerability Reporting:
[open a private security advisory](https://github.com/CommitBrief/ui/security/advisories/new).

If you cannot use GitHub, send an email to `info@muhammetsafak.com` with the subject line `[SECURITY] ui`.

Please include:

- the affected version or versions,
- steps to reproduce the issue,
- the impact of the vulnerability,
- a suggested fix, if you have one.

Vulnerabilities in the `commitbrief` command-line tool itself belong in the [CLI repository](https://github.com/CommitBrief/commitbrief/security/advisories/new).

## Response Process

- You will receive a response within 7 days of your report.
- The report is assessed and you are informed of the result.
- After a fix is released, a GitHub Security Advisory is published.
- If you wish, you are credited for the report in the advisory.

## Scope

The in-app updater downloads a release from GitHub Releases and installs it only if its SHA-256 matches the entry in the release's `checksums.txt`. That check protects against a corrupt or truncated download. `checksums.txt` is not signed and comes from the same release as the files it lists, so anyone who can write to the release can distribute code to every installation that updates. The operating-system signatures (Developer ID and notarization on macOS, Authenticode on Windows, PGP on Linux packages) are checked by the operating system at the first install, not by the updater. This is a known, accepted risk, described in [docs/RELEASING.md](docs/RELEASING.md), and is not treated as a vulnerability on its own. Protecting write access to the repository and its releases is the mitigation.

The following are out of scope for this policy:

- Vulnerabilities in third-party dependencies (report them to their maintainers)
- Vulnerabilities in the `commitbrief` command-line tool, the GitHub CLI (`gh`) or the AI providers they call (report them to those projects)
