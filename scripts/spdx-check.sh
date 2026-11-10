#!/usr/bin/env bash
# spdx-check.sh - fail if any Go source file lacks the per-file SPDX header.
# Invoked by `make spdx-check` (part of `make check`) and by CI.
#
# Scope: Go sources only. Shell, YAML and Markdown ride the repo LICENSE file.
# The header may sit on line 1 or just below a build-constraint block, so the
# first few lines are scanned rather than requiring line 1 exactly.

set -euo pipefail

cd "$(dirname "$0")/.."

readonly HEADER='SPDX-License-Identifier: GPL-3.0-or-later'
readonly SCAN_LINES=5

# `--cached --others --exclude-standard` lists tracked files AND brand-new
# untracked (non-ignored) ones, so a freshly added file is caught before it is
# committed. `while read` instead of `mapfile`: macOS still ships bash 3.2.
files=()
while IFS= read -r f; do
  [ -n "$f" ] && files+=("$f")
done < <(
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git ls-files --cached --others --exclude-standard '*.go'
  else
    find . -name '*.go' -not -path './frontend/node_modules/*' -not -path './bin/*'
  fi
)

missing=()
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  if ! head -n "$SCAN_LINES" "$f" | grep -q "$HEADER"; then
    missing+=("$f")
  fi
done

if [ "${#missing[@]}" -gt 0 ]; then
  printf 'fail: %d Go file(s) missing the SPDX header (%s):\n' \
    "${#missing[@]}" "$HEADER" >&2
  for f in "${missing[@]}"; do
    printf '  %s\n' "$f" >&2
  done
  printf '\nAdd this as the first line of each file:\n  // %s\n' "$HEADER" >&2
  exit 1
fi

printf 'ok:   all %d Go source files carry the SPDX header\n' "${#files[@]}"
