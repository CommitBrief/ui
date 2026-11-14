// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The frontend words every repo error code itself and turns an unknown code into
// "internal" (frontend/src/api/repoErrors.ts). If a code is added to or removed
// from the closed set in internal/domain/repo without the frontend list moving
// with it, the user would see the generic message and CI would stay green. This
// compares the two lists, read straight from the sources.
func TestFrontendRepoErrorCodesMatchTheDomain(t *testing.T) {
	goSrc, err := os.ReadFile("../../domain/repo/repo.go")
	if err != nil {
		t.Fatal(err)
	}
	tsSrc, err := os.ReadFile("../../../frontend/src/api/repoErrors.ts")
	if err != nil {
		t.Fatal(err)
	}

	goCodes := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*Code[A-Za-z]+\s+Code\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(goSrc), -1) {
		goCodes[m[1]] = true
	}

	block := regexp.MustCompile(`(?s)repoErrorCodes\s*=\s*\[(.*?)\]\s*as const`).FindStringSubmatch(string(tsSrc))
	if block == nil {
		t.Fatal("repoErrorCodes list not found in repoErrors.ts; update this test")
	}
	tsCodes := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(block[1], -1) {
		tsCodes[m[1]] = true
	}

	if len(goCodes) < 10 {
		t.Fatalf("found only %d Go codes; the pattern no longer matches repo.go", len(goCodes))
	}
	if diff := symmetricDiff(goCodes, tsCodes); len(diff) > 0 {
		t.Errorf("repo error codes differ between internal/domain/repo and frontend/src/api/repoErrors.ts (- only in Go, + only in TS):\n%s", strings.Join(diff, "\n"))
	}
}

func symmetricDiff(goCodes, tsCodes map[string]bool) []string {
	var out []string
	for c := range goCodes {
		if !tsCodes[c] {
			out = append(out, "- "+c)
		}
	}
	for c := range tsCodes {
		if !goCodes[c] {
			out = append(out, "+ "+c)
		}
	}
	sort.Strings(out)
	return out
}
