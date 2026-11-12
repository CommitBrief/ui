# Releasing CommitBrief UI

Releases are built, signed and published by `.github/workflows/release.yml` when a
`v*` tag is pushed. This page is for the maintainer: what to prepare once, how to
cut a test release, how to rotate keys, and how to take a release back.

Two separate signatures protect a release, and they do different jobs:

| Signature | Made with | Protects | Checked by |
|---|---|---|---|
| Operating system signing: Developer ID and notarization (macOS), Authenticode (Windows), PGP on the deb and rpm (Linux) | Certificates and keys you obtain from Apple, a Windows code-signing authority and your own PGP key | The first install: Gatekeeper and SmartScreen accept the download | The operating system and the package tools |
| Update signature: Ed25519 over `checksums.txt` | A key pair you generate yourself, independent of any certificate | Every in-app update | The application, with the public key compiled in |

The workflow refuses to publish a release that lacks either one. A manual run of
the workflow (Actions, Release, Run workflow) is a dry run: it builds and packages,
never signs (even when the secrets exist, so code from an arbitrary branch is never
signed with the real certificates), marks its artifacts `-UNSIGNED` and never
publishes.

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

Create a GitHub **environment** named `release` (Settings, Environments) and, in
its "Deployment branches and tags" rule, allow only the tag pattern `v*`. Store the
secrets below as **environment secrets of `release`**, not as repository secrets:
repository secrets can be read by any workflow on any branch, and the update
private key is the one key that can push an update to every user. The workflow
jobs enter the `release` environment only on tag runs; a manual dry run runs
outside it and has no secrets. The code uses only these names.

| Secret | Content |
|---|---|
| `APPLE_DEVELOPER_ID_P12` | Base64 of the "Developer ID Application" certificate exported as `.p12` (`base64 -i cert.p12`) |
| `APPLE_DEVELOPER_ID_PASSWORD` | The password of that `.p12` |
| `APPLE_NOTARY_KEY` | The App Store Connect API key, the text of the `.p8` file |
| `APPLE_NOTARY_KEY_ID` | The key ID of that API key |
| `APPLE_NOTARY_ISSUER` | The issuer ID of your App Store Connect team |
| `WINDOWS_SIGNING_PFX` | Base64 of the Authenticode certificate as `.pfx` |
| `WINDOWS_SIGNING_PASSWORD` | The password of that `.pfx` |
| `WINDOWS_SIGNING_TIMESTAMP_URL` | Optional. Defaults to `http://timestamp.digicert.com` |
| `LINUX_PGP_KEY` | The ASCII-armored private PGP key (`gpg --armor --export-secret-keys KEYID`) |
| `LINUX_PGP_PASSPHRASE` | Its passphrase |
| `UPDATE_ED25519_PRIVATE_KEY` | The update private key, see below |

The Windows steps assume a certificate that can be exported to a `.pfx`. A
certificate that lives on a hardware token or in a cloud signing service (an EV
certificate, Azure Trusted Signing) needs the "Sign the executable" and "Sign the
installer" steps changed to that service; the secret names above would change with
it.

### Create the update key pair

On your own machine, once:

```sh
go run scripts/sign-checksums.go keygen -private-out update-private.key
```

It prints the public key line and writes the private key to the file (mode 0600,
never overwritten). Then:

1. Store the file's content as the `UPDATE_ED25519_PRIVATE_KEY` secret and delete
   the file. Keep one offline copy somewhere safe: losing it forces a rotation.
2. Paste the printed public key line into
   `internal/adapter/updater/release_key.pub` (below the comments) and commit.
   Until this is done every build refuses all updates, and the release workflow
   fails in its first job.

The private key never goes into the repository, a log or an argument list. The
tool reads it from the environment only.

## Cut a test release

The acceptance run needs two signed test releases and a third with a broken
signature. Tags are a decision of the maintainer; the workflow only reacts to them.

```sh
git tag v0.0.1-test.1 && git push origin v0.0.1-test.1
```

Before the first one: the repository is public (above), the `release` environment
and its secrets exist, and `release_key.pub` holds the update public key.

The workflow then runs: `meta` (checks the tag, that the commit is on `main`, and
that the update key pair matches), the three platform builds in parallel, and
`publish` (writes `checksums.txt`, signs it, creates the GitHub release with
`prerelease: true` because the tag has a pre-release part).

Check the result on each system:

- macOS: `spctl -a -vv "/Applications/CommitBrief UI.app"` says
  `accepted` and `source=Notarized Developer ID`.
- Windows: `Get-AuthenticodeSignature "$env:LOCALAPPDATA\Programs\CommitBrief UI\commitbrief-ui.exe"`
  has `Status : Valid`.
- Linux: `rpm --import commitbrief-ui-signing-key.asc && rpm -K commitbrief-ui_*.rpm`
  says the signatures are OK.

Then publish `v0.0.1-test.2`, install `test.1`, switch the update channel to
"Pre-release" on the Updates screen, and let it update to `test.2`. For the
negative test, make a third release whose `checksums.txt.sig` is damaged (edit the
asset on the release, or sign with another key) and confirm the app shows
"The release signature is not valid" and keeps running `test.2`.

What gets updated in place:

| System | Installed as | Updated by |
|---|---|---|
| macOS | `.app` from the `.dmg` or `.zip` | the `.zip` (signed, notarized `.app`) |
| Windows | per-user NSIS installer | the `.zip` holding the signed `.exe` |
| Linux | AppImage | the AppImage |
| Linux | binary from the `.tar.gz` in a directory you can write | the `.tar.gz` |
| Linux | `.deb` / `.rpm` | not self-updated; the package manager updates it |

## Rotate the update key

The compiled-in public key is the only trust anchor, so a new key has to reach
users through a release that the old key signed. `release_key.pub` may list more
than one key (up to four, one per line); a signature by any of them is accepted.

1. Generate the new pair (`keygen`, above). Leave the secret as it is.
2. Add the new public key as a second line in `release_key.pub`, keeping the old
   one, commit, and cut release `N` as usual. It is signed with the old private key
   (the secret), which the workflow's key check accepts because the old key is
   still in the file. Everyone who updates to `N` now trusts both keys.
3. Replace the `UPDATE_ED25519_PRIVATE_KEY` secret with the new private key.
4. Cut release `N+1`, signed with the new key (the workflow accepts it because the
   new key is in the file). Delete the old key line from `release_key.pub` in the
   commit of `N+1` or later, once the old key is no longer needed to bridge users.
   A copy older than `N` does not know the new key and rejects `N+1` with
   `signature_invalid`; it cannot reach `N` from inside the application either,
   because the updater only ever offers the highest release. Those users reinstall
   from the release page by hand. If you must bridge them in-app, keep `N` the
   latest release for as long as the bridge is needed and rotate afterwards.

If the private key leaks, the old key line must come out of `release_key.pub`: while
it stays, every build that carries it accepts whatever the attacker signs. So:
generate a new pair, replace the secret, **delete the leaked public key line** (do
not keep it as a second key), cut a new release signed with the new key, delete
suspect releases, and say in the release notes that users on older copies must
reinstall by hand from the release page. Copies that only know the leaked key
cannot be protected in-app; an attacker holding the key can still sign an update
for them until they reinstall.

## Rotate an operating-system certificate

Replace the matching secrets. Developer ID, Authenticode and PGP certificates
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
  Chocolatey at those exact versions (NSIS builds the installer that is then
  signed), and fails if `makensis` does not report `v3.11`. Bump them on purpose,
  together with the version check.
