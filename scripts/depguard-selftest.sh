#!/usr/bin/env bash
# depguard-selftest.sh - prove that every layering rule in .golangci.yml still
# fires. It drops one violating file per rule into the tree, runs
# golangci-lint once, checks that each violation is reported by the expected
# rule (and that the allowed adapter is not), then removes the files.
#
# Depguard matches globs against absolute paths, so a rule that is written
# wrongly can switch itself off without any error. This script is what turns
# "the config looks right" into "the config demonstrably works".

set -euo pipefail

cd "$(dirname "$0")/.."

# bash 3.2 (macOS) has no associative arrays; keep parallel arrays.
files=()
expect=()
created_dirs=()

cleanup() {
  for f in "${files[@]:-}"; do
    [ -n "$f" ] && rm -f "$f"
  done
  for d in "${created_dirs[@]:-}"; do
    [ -n "$d" ] && rmdir "$d" 2>/dev/null || true
  done
}
trap cleanup EXIT

# add <path> <expected-rule-or-"-"> <package> <body>
add() {
  local path="$1" rule="$2" pkg="$3" body="$4" dir
  dir="$(dirname "$path")"
  # Remember every directory this call creates (deepest first) for cleanup.
  local d="$dir"
  while [ ! -d "$d" ]; do
    created_dirs+=("$d")
    d="$(dirname "$d")"
  done
  mkdir -p "$dir"
  printf '// SPDX-License-Identifier: GPL-3.0-or-later\n\npackage %s\n\n%s\n' "$pkg" "$body" > "$path"
  files+=("$path")
  expect+=("$rule")
}

WAILS='import _ "github.com/wailsapp/wails/v3/pkg/application"'
EXEC='import _ "os/exec"'
EXECABS='import _ "golang.org/x/sys/execabs"'
PROC='import "os"
var _ = os.StartProcess'

add internal/domain/zz_selftest.go                domain-stdlib-only                domain   "$WAILS"
add internal/app/zz_selftest.go                   app-domain-and-port-only          app      'import _ "github.com/CommitBrief/ui/internal/platform"'
add internal/platform/zz_selftest_wails.go        wails-only-in-shell               platform "$WAILS"
add internal/platform/zz_selftest_exec.go         exec-only-in-cli-and-gh-adapters  platform "$EXEC"
add internal/platform/zz_selftest_execabs.go      exec-only-in-cli-and-gh-adapters  platform "$EXECABS"
add internal/platform/zz_selftest_proc.go         forbidigo                         platform "$PROC"
add cmd/commitbrief-ui/zz_selftest.go             exec-only-in-cli-and-gh-adapters  main     "$EXEC"
add embed_zz_selftest.go                          wails-only-in-shell               ui       "$WAILS"
# The two adapters that may spawn processes must stay clean.
add internal/adapter/clicmd/zz_selftest.go        -                                 clicmd   "$EXEC
$PROC"
add internal/adapter/github/gh/zz_selftest.go     -                                 gh       "$EXEC
$PROC"

out="$(golangci-lint run . ./cmd/... ./internal/... 2>&1 || true)"

fail=0
for i in "${!files[@]}"; do
  f="${files[$i]}"
  rule="${expect[$i]}"
  # Only layering findings count; the generated files are not gofmt-clean.
  hits="$(printf '%s\n' "$out" | grep -F "$f:" | grep -E '\((depguard|forbidigo)\)' || true)"
  if [ "$rule" = "-" ]; then
    if [ -n "$hits" ]; then
      echo "FAIL $f: allowed adapter was reported:"; echo "$hits"; fail=1
    else
      echo "ok   $f: not reported (allowed)"
    fi
  elif printf '%s\n' "$hits" | grep -q "$rule"; then
    echo "ok   $f: reported by $rule"
  else
    echo "FAIL $f: expected a report from $rule, got:"; echo "${hits:-<nothing>}"; fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "depguard-selftest: FAILED" >&2
  exit 1
fi
echo "depguard-selftest: ok"
