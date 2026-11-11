// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"fmt"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/review"
)

// op names the kind of CLI call, so classification can pick a better fallback
// than "unknown" where the call itself implies the failure class.
type op string

const (
	opVersion      op = "version"
	opReview       op = "review"
	opConfig       op = "config"
	opProviders    op = "providers"
	opProviderTest op = "providers_test"
	opUpgrade      op = "upgrade"
)

// failure is everything classification looks at.
type failure struct {
	op       op
	exitCode int
	stderr   string
	// report is the stdout document when it decoded, else nil.
	report *review.Report
}

// classify reduces a failed CLI run (non-zero exit) to one closed code. The
// order is the contract (UI-0004 section 5):
//
//  1. the JSON document: remote.status, then meta.degrade_reason;
//  2. the exit code: the CLI has only 0 and 1, so anything else (a signal, a
//     runtime panic code) carries no class and ends up as unknown;
//  3. as a last resort the en + tr stderr patterns.
//
// Step 2 of the analysis (preflights the UI runs itself) happens before the CLI
// is started, in the engine, so it never reaches this function.
func classify(f failure) *errcode.Error {
	tail := tailString(f.stderr, 4096)

	if r := f.report; r != nil {
		if r.Remote != nil {
			switch r.Remote.Status {
			case review.RemoteTooVolatile:
				return errcode.New(errcode.TooVolatile, "the PR head moved twice during the review").WithDetail(tail)
			case review.RemoteDegraded:
				return errcode.New(errcode.Degraded, "the provider answer could not be parsed into findings").WithDetail(tail)
			}
		}
		if r.Degraded() {
			return errcode.New(errcode.Degraded, "the provider answer degraded: "+r.Meta.DegradeReason).WithDetail(tail)
		}
	}

	if f.exitCode != 1 {
		return errcode.New(errcode.Unknown, fmt.Sprintf("the CLI exited with code %d", f.exitCode)).WithDetail(tail)
	}

	if code, ok := classifyStderr(f.stderr); ok {
		return errcode.New(code, "classified from the CLI's stderr").WithDetail(tail)
	}
	if f.op == opProviderTest {
		return errcode.New(errcode.ProviderError, "the provider connection test failed").WithDetail(tail)
	}
	return errcode.New(errcode.Unknown, "the CLI failed with an unrecognized message").WithDetail(tail)
}

// stderrRule maps message fragments to a code. Fragments are lower-case and
// match a lower-cased stderr, so Turkish and English catalog text both work.
// They come from the CLI's i18n catalogs (messages.en.yml / messages.tr.yml);
// keep a fragment short and distinctive so a reworded message still matches.
type stderrRule struct {
	code      errcode.Code
	fragments []string
}

// Order matters: the first rule with a matching fragment wins.
var stderrRules = []stderrRule{
	{errcode.GHNotFound, []string{
		"'gh' cli is required but not on path", // remote.gh_missing (en)
		"'gh' cli gerekli ama path'te yok",     // remote.gh_missing (tr)
	}},
	{errcode.ProviderNotAPI, []string{
		"can't produce structured findings", // remote.plain_text_provider (en)
		"yapılandırılmış bulgu üretemez",    // remote.plain_text_provider (tr)
		"do not return structured findings", // sarif.plain_text_provider (en)
	}},
	{errcode.SelfPR, []string{
		"you are the author of this pr", // remote.self_pr_blocked (en)
		"yazarı sizsiniz",               // remote.self_pr_blocked (tr)
	}},
	{errcode.TooVolatile, []string{
		"pr head changed twice", // remote.too_volatile (en)
		"iki kez değişti",       // remote.too_volatile (tr)
	}},
	{errcode.SecretDetected, []string{
		"pre-send secret scanner", // guard.secrets.aborted_user (en + tr)
		"--allow-secrets",         // guard.secrets.aborted_non_interactive (en + tr)
	}},
	{errcode.CostAborted, []string{
		"cost preflight",  // cost.aborted_user (en + tr)
		"--no-cost-check", // cost.aborted_non_interactive (en + tr)
		"token preflight", // guard.tokens.aborted_user: same family, a preflight stop
		"token_preflight", // guard.tokens.aborted_non_interactive
	}},
	{errcode.NotARepo, []string{
		"not inside a git repository",         // common.error.not_a_repo, remote.repo_required (en)
		"bir git deposunun içinde değilsiniz", // common.error.not_a_repo (tr)
		"bir git deposu içinde değilsiniz",    // remote.repo_required (tr)
	}},
	{errcode.Timeout, []string{
		"timed out after",      // timeout.exceeded (en)
		"zaman aşımına uğradı", // timeout.exceeded (tr)
	}},
}

// errorPrefixes are the CLI's localized common.error_prefix values.
var errorPrefixes = []string{"Error:", "Hata:"}

// errorSegment returns the text from the last "Error:" / "Hata:" line to the
// end of stderr: the CLI's actual failure, including any continuation lines
// (gh's own wording is relayed after the prefix). Anything before it is
// progress output and informational notes, which can contain the very
// fragments the rules look for ("token preflight", "API key" in the notes
// guard.tokens.window_unknown_note and provider.insecure_http_note) without
// being the failure.
func errorSegment(stderr string) (string, bool) {
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		for _, p := range errorPrefixes {
			if strings.HasPrefix(t, p) {
				return strings.Join(lines[i:], "\n"), true
			}
		}
	}
	return "", false
}

// classifyStderr classifies the failure message. When stderr has an "Error:"
// line only that line onward is looked at; if it matches nothing the result is
// "no match" (unknown), never a guess from the notes above it. Without any
// prefixed line the whole tail is scanned.
func classifyStderr(stderr string) (errcode.Code, bool) {
	if seg, ok := errorSegment(stderr); ok {
		return matchStderr(seg)
	}
	return matchStderr(stderr)
}

// matchStderr runs the en + tr catalog patterns, then the gh auth heuristic,
// then the provider heuristic.
func matchStderr(stderr string) (errcode.Code, bool) {
	s := strings.ToLower(stderr)
	for _, rule := range stderrRules {
		for _, frag := range rule.fragments {
			if strings.Contains(s, frag) {
				return rule.code, true
			}
		}
	}
	// `gh` failures are relayed as "gh <args>: <gh's stderr>". gh has no
	// localized catalog we can match, so recognise its own auth wording.
	if strings.Contains(s, "gh auth login") ||
		(strings.Contains(s, "gh ") && containsAny(s, "bad credentials", "http 401", "not logged in", "authentication required")) {
		return errcode.GHNotAuthenticated, true
	}
	// Provider failures carry no catalog marker; these are the stable phrases
	// providers and HTTP layers use.
	if containsAny(s,
		"api key", "rate limit", "rate_limit", "unauthorized", "overloaded",
		"quota", "invalid_request", "authentication_error", "status code",
		"connection refused", "no such host", "test failed", "testi başarısız",
	) {
		return errcode.ProviderError, true
	}
	return "", false
}

func containsAny(s string, fragments ...string) bool {
	for _, f := range fragments {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}

// tailString returns at most n bytes from the end of s, on a rune boundary.
func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !isRuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
