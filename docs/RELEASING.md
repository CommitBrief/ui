# Releasing CommitBrief UI

Releases are built, signed (macOS and Linux) and published by
`.github/workflows/release.yml` when a `v*.*.*` tag is pushed. This page is for the
maintainer: what to prepare once, how to cut a test release, how to rotate
certificates, and how to take a release back.

Two things protect a release, and they do different jobs:

| Protection | Made with | Protects | Checked by |
|---|---|---|---|
| Operating system signing: Developer ID and notarization (macOS), PGP on the deb and rpm (Linux) | Certificates and keys you obtain from Apple and your own PGP key | The first install on macOS (Gatekeeper) and the package tools on Linux | The operating system and the package tools |
| SHA-256 in `checksums.txt` | The publish job of the workflow | Every in-app update against a corrupt or truncated download | The application, before anything is unpacked |

Windows packages are not signed (see below).

`checksums.txt` is not signed: it comes from the same release as the files it
lists, so it does not protect against someone who can write to the release (a
risk accepted on purpose, UI-0007). The operating system signing above is checked
by the OS at the first install and launch only; the in-app updater does not verify
it, so on the update path the SHA-256 is the only check.

The workflow refuses to publish a release whose macOS or Linux signing is
incomplete. A manual run of the workflow (Actions, Release, Run workflow) is a dry
run: it builds and packages, never signs the macOS and Linux packages (even when
the secrets exist, so code from an arbitrary branch is never signed with the real
certificates), marks those artifacts `-UNSIGNED` and never publishes.

## Windows is published unsigned

The Windows installer (`-setup.exe`) and the executable inside the `.zip` carry no
Authenticode signature (a maintainer decision, UI-0009). The workflow has no
Windows signing step and needs no Windows secret, and the publish job does not
wait for a Windows signature. On the first run of the installer, Windows
SmartScreen shows an "unknown publisher" warning; the user continues with
"More info", then "Run anyway". The in-app updater is not affected: it checks the
SHA-256 in `checksums.txt`, not a signature. If Windows signing is added later,
the first candidate is the SignPath Foundation (free code signing for open source
projects); that change adds the signing steps and secrets back to the `windows`
job and the Windows check back to the publish gate.

## Prerequisite: the repository must be public

The application looks for updates in the GitHub Releases of `CommitBrief/ui`
**without credentials**: it carries no token, and must not (a token inside a
distributed application is a leak). GitHub answers an anonymous request for a
private repository with `404`, so while the repository is private every update
check fails with "The release feed is not available" (`update_feed_unavailable`)
and no update, good or bad, can be observed. **Make the repository public
(Settings, General, Danger Zone, Change visibility) before the first test
release.** If it is ever made private again, in-app updates stop working until it
is public again; they never fall back to "up to date" silently.

## What the maintainer provides (once)

The GitHub **environment** `release` (Settings, Environments) exists; its
"Deployment branches and tags" rule allows only the tag pattern `v*.*.*`, the same
pattern that triggers the workflow. If it has to be recreated, restore that rule
first. Store the secrets below as **environment secrets of `release`**, not as
repository secrets:
repository secrets can be read by any workflow on any branch, and the signing
keys are what make a download look like it came from this project. The workflow
jobs enter the `release` environment only on tag runs; a manual dry run runs
outside it and has no secrets. The code uses only these names.

| Secret | Content |
|---|---|
| `APPLE_DEVELOPER_ID_P12` | Base64 of the "Developer ID Application" certificate exported as `.p12` (`base64 -i cert.p12`) |
| `APPLE_DEVELOPER_ID_PASSWORD` | The password of that `.p12` |
| `APPLE_NOTARY_KEY` | The App Store Connect API key, the text of the `.p8` file |
| `APPLE_NOTARY_KEY_ID` | The key ID of that API key |
| `APPLE_NOTARY_ISSUER` | The issuer ID of your App Store Connect team |
| `LINUX_PGP_KEY` | The ASCII-armored private PGP key (`gpg --armor --export-secret-keys KEYID`) |
| `LINUX_PGP_PASSPHRASE` | Its passphrase |

Windows needs no secret (see "Windows is published unsigned").

## Cut a test release

The acceptance run needs two test releases (signed on macOS and Linux) and a
third whose `checksums.txt` is wrong. Tags are a decision of the maintainer; the
workflow only reacts to them.

```sh
git tag v0.0.1-test.1 && git push origin v0.0.1-test.1
```

Before the first one: the repository is public (above), the `release` environment
and its secrets exist.

The workflow then runs: `meta` (checks the tag and that the commit is on `main`),
the three platform builds in parallel, and `publish` (writes `checksums.txt`,
creates the GitHub release with `prerelease: true` because the tag has a
pre-release part).
The release notes come from the matching `## [version]` section of `CHANGELOG.md`;
a pre-release tag such as `v0.0.1-test.1` needs no section of its own and gets the
fixed note "Test release, see [Unreleased]" instead, while a stable tag without a
section stops the workflow before anything is built.

Check the result on each system:

- macOS: `spctl -a -vv "/Applications/CommitBrief UI.app"` says
  `accepted` and `source=Notarized Developer ID`.
- Windows: `Get-AuthenticodeSignature "$env:LOCALAPPDATA\Programs\CommitBrief UI\commitbrief-ui.exe"`
  has `Status : NotSigned` (Windows is published unsigned on purpose), and the
  installer shows the SmartScreen "unknown publisher" warning.
- Linux: `rpm --import commitbrief-ui-signing-key.asc && rpm -K commitbrief-ui_*.rpm`
  says the signatures are OK.

Then publish `v0.0.1-test.2`, install `test.1`, switch the update channel to
"Pre-release" on the Updates screen, and let it update to `test.2`. For the
negative test, make a third release and damage its `checksums.txt` (edit the hash
of the artefact for your system on the release) and confirm the app shows
"The download does not match its checksum" and keeps running `test.2`.

What gets updated in place:

| System | Installed as | Updated by |
|---|---|---|
| macOS | `.app` from the `.dmg` or `.zip` | the `.zip` (signed, notarized `.app`) |
| Windows | per-user NSIS installer | the `.zip` holding the (unsigned) `.exe` |
| Linux | AppImage | the AppImage |
| Linux | binary from the `.tar.gz` in a directory you can write | the `.tar.gz` |
| Linux | `.deb` / `.rpm` | not self-updated; the package manager updates it |

## Rotate an operating-system certificate

Replace the matching secrets. Developer ID certificates and PGP keys
expire (usually yearly); renew before the date and cut a release afterwards, since
an expired certificate does not invalidate signatures made while it was valid
(the timestamp covers that) but the next release cannot be signed.

## Take a release back

A release that must not be installed any more:

1. Mark it a draft or delete it on GitHub. The updater lists published releases
   only, so it stops offering it at once.
2. Releases already installed keep running; users do not get a downgrade.
3. Publish a fixed version with a higher number. The updater never installs a
   version that is not higher than the running one, so a rollback is always a new,
   higher release.

A broken update on a user's machine is undone by the update helper itself: it
keeps a backup of the old application, and restores it if the swap or the first
launch of the new version fails.

## Pinned inputs

- Third-party GitHub Actions are pinned to a commit SHA (the version is in the
  comment). Bump them on purpose.
- Wails is pinned to v3.1.0 (`make tools`); the updater API the app uses is that
  version's `pkg/updater`.
- The AppImage is built with `linuxdeploy` release `1-alpha-20251107-1`, checked
  against a SHA-256 in the workflow before it runs.
- On Windows the workflow installs NSIS `3.11.0` and GNU make `4.4.1` from
  Chocolatey at those exact versions (NSIS builds the installer), and fails if
  `makensis` does not report `v3.11`. Bump them on purpose, together with the
  version check.
