#!/usr/bin/env bash
# gen-assets-car.sh - compile build/appicon.icon (the Icon Composer bundle) into
# build/darwin/Assets.car with actool, the macOS 26 icon format. Invoked by
# `make icons` after scripts/genicons.go; needs macOS 26+ and Xcode 26+ tools.
#
# build/darwin/Taskfile.yml copies Assets.car into the .app only when the file
# exists, and Info.plist keeps CFBundleIconFile=icons, so a checkout without
# Assets.car falls back to icons.icns.

set -euo pipefail

cd "$(dirname "$0")/.."

[ "$(uname -s)" = Darwin ] || { echo "gen-assets-car: macOS only" >&2; exit 1; }

out="$(mktemp -d "${TMPDIR:-/tmp}/gen-assets-car.XXXXXX")"
trap 'rm -rf "$out"' EXIT

# Same arguments Wails' own icon task passes (wails3 generate icons).
/usr/bin/actool build/appicon.icon \
  --compile "$out" \
  --notices --warnings --errors \
  --output-partial-info-plist "$out/partial.plist" \
  --app-icon appicon \
  --enable-on-demand-resources NO \
  --development-region en \
  --target-device mac \
  --minimum-deployment-target 26.0 \
  --platform macosx >"$out/actool.plist"

[ -f "$out/Assets.car" ] || { echo "gen-assets-car: actool produced no Assets.car" >&2; cat "$out/actool.plist" >&2; exit 1; }
cp "$out/Assets.car" build/darwin/Assets.car
# actool also emits an icns of the layered icon; the committed icons.icns is
# the one drawn by genicons.go, so the actool one is discarded.
echo "wrote build/darwin/Assets.car ($(wc -c <build/darwin/Assets.car | tr -d ' ') bytes)"
