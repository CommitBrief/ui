#!/usr/bin/env bash
# license-check.sh - audit Go dependency licenses for GPL-3.0 compatibility.
# CommitBrief UI is GPL-3.0-or-later and may only bundle compatible code.

set -euo pipefail

cd "$(dirname "$0")/.."

# Licenses considered compatible with GPL-3.0-or-later for static linking.
# Adjust deliberately; rejecting unknown is the safe default.
ALLOWED='Apache-2.0,BSD-2-Clause,BSD-3-Clause,GPL-3.0,GPL-3.0-or-later,ISC,LGPL-3.0,LGPL-3.0-or-later,MIT,MPL-2.0,Unlicense'

if ! command -v go-licenses >/dev/null 2>&1; then
  echo "license-check: go-licenses not found; install it with 'make tools'" >&2
  exit 1
fi

go-licenses check . ./cmd/... ./internal/... \
  --allowed_licenses="$ALLOWED" \
  --ignore github.com/CommitBrief/ui

echo "license-check: ok"
