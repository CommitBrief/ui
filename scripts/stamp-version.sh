#!/usr/bin/env bash
# stamp-version.sh - write a release version into the packaging metadata.
#
#   scripts/stamp-version.sh 0.1.0            stamp the files under the repo root
#   scripts/stamp-version.sh 0.1.0-rc.1 DIR   stamp the files under DIR (tests)
#   scripts/stamp-version.sh --selftest       stamp a scratch copy and check it
#
# The application's own version is not stamped here: it is injected into the
# binary at link time (APP_VERSION, see build/*/Taskfile.yml). This script sets
# the metadata the operating systems and package managers read:
#   build/darwin/Info.plist       CFBundleVersion, CFBundleShortVersionString
#   build/windows/info.json       file and product version (numeric core only)
#   build/linux/nfpm/nfpm.yaml    package version (full semantic version)
#   build/config.yml              info.version
# Windows and macOS accept dotted numbers only, so a pre-release such as
# 0.1.0-rc.1 is written as 0.1.0 there.

set -euo pipefail

selftest() {
  local root scratch
  root="$(cd "$(dirname "$0")/.." && pwd)"
  scratch="$(mktemp -d)"
  trap 'rm -rf "$scratch"' RETURN
  mkdir -p "$scratch/build/darwin" "$scratch/build/windows" "$scratch/build/linux/nfpm"
  cp "$root/build/config.yml" "$scratch/build/config.yml"
  cp "$root/build/darwin/Info.plist" "$scratch/build/darwin/Info.plist"
  cp "$root/build/windows/info.json" "$scratch/build/windows/info.json"
  cp "$root/build/linux/nfpm/nfpm.yaml" "$scratch/build/linux/nfpm/nfpm.yaml"

  bash "$0" 7.8.9-rc.1 "$scratch" >/dev/null

  local fail=0
  check() { # file pattern description
    if ! grep -Eq "$2" "$scratch/$1"; then
      echo "stamp-version selftest: $1: $3" >&2
      fail=1
    fi
  }
  check build/darwin/Info.plist '<key>CFBundleVersion</key>' "CFBundleVersion key lost"
  [ "$(grep -A1 'CFBundleVersion' "$scratch/build/darwin/Info.plist" | grep -c '<string>7\.8\.9</string>')" = 1 ] \
    || { echo "stamp-version selftest: CFBundleVersion is not 7.8.9" >&2; fail=1; }
  [ "$(grep -A1 'CFBundleShortVersionString' "$scratch/build/darwin/Info.plist" | grep -c '<string>7\.8\.9</string>')" = 1 ] \
    || { echo "stamp-version selftest: CFBundleShortVersionString is not 7.8.9" >&2; fail=1; }
  check build/windows/info.json '"file_version": "7\.8\.9"' "file_version"
  check build/windows/info.json '"product_version": "7\.8\.9"' "product_version"
  check build/windows/info.json '"FileVersion": "7\.8\.9"' "FileVersion"
  check build/windows/info.json '"ProductVersion": "7\.8\.9"' "ProductVersion"
  check build/linux/nfpm/nfpm.yaml '^version: "7\.8\.9-rc\.1"$' "nfpm version"
  check build/config.yml '^  version: "7\.8\.9"' "config version"
  # A version that is not a semantic version is refused.
  if bash "$0" "1.2" "$scratch" >/dev/null 2>&1; then
    echo "stamp-version selftest: an invalid version was accepted" >&2
    fail=1
  fi
  [ "$fail" = 0 ] || exit 1
  echo "ok:   stamp-version selftest"
}

if [ "${1:-}" = "--selftest" ]; then
  selftest
  exit 0
fi

version="${1:-}"
root="${2:-$(cd "$(dirname "$0")/.." && pwd)}"

# Semantic version: MAJOR.MINOR.PATCH with an optional -prerelease (build
# metadata is not used in tags).
if ! [[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "stamp-version: '$version' is not a semantic version (MAJOR.MINOR.PATCH[-prerelease])" >&2
  exit 2
fi
core="${version%%-*}"

# sed -i differs between GNU and BSD; a backup suffix works on both.
edit() { # file sed-expression...
  local f="$1"
  shift
  [ -f "$f" ] || { echo "stamp-version: $f not found" >&2; exit 1; }
  sed -i.bak -E "$@" "$f"
  rm -f "$f.bak"
}

edit "$root/build/darwin/Info.plist" \
  -e "/<key>CFBundleVersion<\/key>/{n;s|<string>[^<]*</string>|<string>$core</string>|;}" \
  -e "/<key>CFBundleShortVersionString<\/key>/{n;s|<string>[^<]*</string>|<string>$core</string>|;}"

edit "$root/build/windows/info.json" \
  -e "s|(\"file_version\": \")[^\"]*\"|\1$core\"|" \
  -e "s|(\"product_version\": \")[^\"]*\"|\1$core\"|" \
  -e "s|(\"FileVersion\": \")[^\"]*\"|\1$core\"|" \
  -e "s|(\"ProductVersion\": \")[^\"]*\"|\1$core\"|"

edit "$root/build/linux/nfpm/nfpm.yaml" -e "s|^version: \"[^\"]*\"|version: \"$version\"|"

edit "$root/build/config.yml" -e "s|^(  version: \")[^\"]*\"|\1$core\"|"

echo "stamped $version (numeric core $core)"
