#!/usr/bin/env bash
# release-notes.sh - print one version's section of CHANGELOG.md.
#
#   scripts/release-notes.sh 0.1.0                 section of ./CHANGELOG.md (repo root)
#   scripts/release-notes.sh 0.0.1-test.1 FILE     section of FILE (tests)
#   scripts/release-notes.sh --selftest            check the extraction on a scratch file
#
# The release workflow publishes these notes instead of GitHub's generated ones.
# It is fail-closed: a missing CHANGELOG.md, a stable version without a section,
# or a section with no content exits non-zero with a clear message, so a release
# is never published with empty or wrong notes.
#
# One exception, decided by the maintainer: a pre-release version (it has a "-",
# e.g. 0.0.1-test.1, 0.1.0-rc.1) with no section of its own gets a fixed note,
# "Test release, see [Unreleased](...)", so test tags need no changelog entry.
# A pre-release that does have a section gets that section like any other.
#
# Format (Keep a Changelog): a section starts at "## [VERSION]" (optionally
# followed by " - YYYY-MM-DD") and runs to the next "## " heading. The version
# is compared as a plain string, so "1.2" never matches "1.2.3" and the dots in
# "0.0.1-test.1" are not patterns. Trailing link reference definitions
# ("[1.2.3]: https://...", the footer of the last section) and surrounding blank
# lines are dropped.

set -euo pipefail

extract() { # version file
  local version="$1" file="$2" rc=0
  if [ ! -f "$file" ]; then
    echo "release-notes: $file not found" >&2
    return 1
  fi
  awk -v want="$version" '
    { sub(/\r$/, "") }
    /^## / {
      if (in_section) { exit }
      if (match($0, /^## \[[^]]+\]/) && substr($0, 5, RLENGTH - 5) == want) {
        in_section = 1
        found = 1
      }
      next
    }
    in_section { lines[++n] = $0 }
    END {
      if (!found) { exit 3 }
      s = 1
      while (s <= n && lines[s] ~ /^[[:space:]]*$/) { s++ }
      e = n
      while (e >= s && (lines[e] ~ /^[[:space:]]*$/ || lines[e] ~ /^\[[^]]+\]:[[:space:]]/)) { e-- }
      if (e < s) { exit 4 }
      for (i = s; i <= e; i++) { print lines[i] }
    }
  ' "$file" || rc=$?
  case "$rc" in
    0) return 0 ;;
    3) return 3 ;; # no section; the caller decides (see notes)
    4) echo "release-notes: the \"## [$version]\" section in $file is empty" >&2 ;;
    *) echo "release-notes: failed to read $file (awk exit $rc)" >&2 ;;
  esac
  return 1
}

# notes VERSION FILE: the section, or the fixed pre-release note, or failure.
notes() {
  local version="$1" file="$2" rc=0 repo="${GITHUB_REPOSITORY:-CommitBrief/ui}"
  extract "$version" "$file" || rc=$?
  case "$rc" in
    0) return 0 ;;
    3)
      case "$version" in
        *-*)
          echo "Test release, see [Unreleased](https://github.com/$repo/blob/main/CHANGELOG.md#unreleased)."
          return 0
          ;;
      esac
      echo "release-notes: $file has no \"## [$version]\" section; write the changelog entry before tagging" >&2
      return 1
      ;;
    *) return 1 ;;
  esac
}

selftest() {
  local fail=0 out
  # The fixed pre-release note embeds the repository; pin it so the selftest
  # passes in forks and wherever GITHUB_REPOSITORY is set to something else.
  export GITHUB_REPOSITORY=CommitBrief/ui
  # Global on purpose: the EXIT trap runs after this function has returned, and
  # it must also fire when the selftest ends with "exit 1".
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/release-notes.XXXXXX")"
  trap 'rm -rf "$scratch"' EXIT
  printf '%s\r\n' '# Changelog' '' '## [Unreleased]' '' '- not yet' '' \
    '## [1.2.3] - 2026-11-13' '' '### Added' '' '- crlf entry' '' >"$scratch/crlf.md"
  cat >"$scratch/CHANGELOG.md" <<'EOF'
# Changelog

## [Unreleased]

- not yet

## [1.2.3] - 2026-11-13

### Added

- the feature

### Fixed

- the bug

## [1.2] - 2026-01-01

- short version

## [0.0.1-test.1] - 2026-11-10

- prerelease entry

## [0.0.1] - 2026-11-01

### Added

- first release

## [0.0.0]

## [0.0.2-empty]

## [0.0.0-rc.1]

- last section

[Unreleased]: https://example.invalid/compare/v1.2.3...HEAD
[1.2.3]: https://example.invalid/compare/v1.2...v1.2.3
EOF

  expect_ok() { # description version file expected-first-line expected-line-count
    if ! out="$(notes "$2" "$3" 2>/dev/null)"; then
      echo "release-notes selftest: $1: extraction failed" >&2; fail=1; return
    fi
    if [ "$(printf '%s\n' "$out" | head -n 1)" != "$4" ] || [ "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" != "$5" ]; then
      echo "release-notes selftest: $1: unexpected output: $out" >&2; fail=1
    fi
  }
  expect_fail() { # description version file
    if notes "$2" "$3" >/dev/null 2>&1; then
      echo "release-notes selftest: $1: expected failure, got success" >&2; fail=1
    fi
  }

  expect_ok 'section with date and subsections' 1.2.3 "$scratch/CHANGELOG.md" '### Added' 7
  expect_ok 'does not bleed into the next section' 1.2 "$scratch/CHANGELOG.md" '- short version' 1
  expect_ok 'prerelease version, dots are literal' 0.0.1-test.1 "$scratch/CHANGELOG.md" '- prerelease entry' 1
  expect_ok 'release after a prerelease' 0.0.1 "$scratch/CHANGELOG.md" '### Added' 3
  expect_ok 'last section drops link definitions' 0.0.0-rc.1 "$scratch/CHANGELOG.md" '- last section' 1
  expect_ok 'CRLF file' 1.2.3 "$scratch/crlf.md" '### Added' 3
  expect_fail 'stable version without a section' 9.9.9 "$scratch/CHANGELOG.md"
  expect_fail 'prefix of an existing version' 1 "$scratch/CHANGELOG.md"
  expect_fail 'dot is not a wildcard' 1x2x3 "$scratch/CHANGELOG.md"
  expect_fail 'empty section' 0.0.0 "$scratch/CHANGELOG.md"
  expect_fail 'empty pre-release section is not replaced by the fixed note' 0.0.2-empty "$scratch/CHANGELOG.md"
  expect_fail 'missing file' 1.2.3 "$scratch/none.md"
  expect_fail 'missing file, pre-release' 1.2.3-rc.1 "$scratch/none.md"
  for pre in 0.0.9-test.1 1.0.0-rc.1 2.0.0-beta.1; do
    expect_ok "pre-release $pre without a section gets the fixed note" "$pre" "$scratch/CHANGELOG.md" \
      'Test release, see [Unreleased](https://github.com/CommitBrief/ui/blob/main/CHANGELOG.md#unreleased).' 1
  done
  expect_fail 'stable 0.0.9 gets no fixed note' 0.0.9 "$scratch/CHANGELOG.md"

  if [ "$fail" -ne 0 ]; then
    exit 1
  fi
  echo "ok:   release-notes selftest"
}

if [ "${1:-}" = "--selftest" ]; then
  selftest
  exit 0
fi

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: $0 VERSION [CHANGELOG]  |  $0 --selftest" >&2
  exit 2
fi

version="${1#v}"
default_file="$(cd "$(dirname "$0")/.." && pwd)/CHANGELOG.md"
notes "$version" "${2:-$default_file}"
