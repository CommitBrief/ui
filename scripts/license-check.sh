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

# Narrow exceptions, each verified by hand against the module's LICENSE file:
#   modernc.org/mathutil (v1.7.1, pulled in by modernc.org/sqlite): the LICENSE
#   is the three-clause BSD text ("Neither the names of the authors nor the
#   names of the contributors ...", copyright The mathutil Authors), which is
#   GPL-compatible. go-licenses' classifier does not recognise this wording
#   ("cannot find a known open source license"), so the module is skipped here
#   rather than the allow list being widened. Re-verify when the version moves.
# The exception was verified for exactly this version. If the module moves,
# the license text has to be read again before the pin below is updated.
MATHUTIL_VERIFIED=v1.7.1
if ! grep -qE "^[[:space:]]*modernc\.org/mathutil ${MATHUTIL_VERIFIED} " go.mod; then
  echo "license-check: modernc.org/mathutil is no longer ${MATHUTIL_VERIFIED} in go.mod;" >&2
  echo "  re-read its LICENSE (it must be GPL-compatible), then update MATHUTIL_VERIFIED in scripts/license-check.sh" >&2
  exit 1
fi

go-licenses check . ./cmd/... ./internal/... \
  --allowed_licenses="$ALLOWED" \
  --ignore github.com/CommitBrief/ui \
  --ignore modernc.org/mathutil

echo "license-check: ok"
