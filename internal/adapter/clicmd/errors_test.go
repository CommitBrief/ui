// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/port"
)

// Real messages from the CLI's catalogs (messages.en.yml / messages.tr.yml at
// commitbrief db4a08a), as they appear on stderr behind the "Error:" prefix.
var stderrFixtures = []struct {
	name   string
	stderr string
	want   errcode.Code
}{
	{"gh missing en", "Error: remote pr: the 'gh' CLI is required but not on PATH. Install it from https://cli.github.com (brew install gh / winget install GitHub.cli / dnf install gh).", errcode.GHNotFound},
	{"gh missing tr", "Hata: remote pr: 'gh' CLI gerekli ama PATH'te yok. https://cli.github.com adresinden kurun (brew install gh / winget install GitHub.cli / dnf install gh).", errcode.GHNotFound},
	{"gh not authenticated", "Error: gh pr view 42 --repo o/r: To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.", errcode.GHNotAuthenticated},
	{"gh bad credentials", "Error: gh api repos/o/r/pulls/42: HTTP 401: Bad credentials (https://api.github.com/repos/o/r/pulls/42)", errcode.GHNotAuthenticated},
	{"not a repo en", "Error: Not inside a git repository.", errcode.NotARepo},
	{"not a repo tr", "Hata: Bir git deposunun içinde değilsiniz.", errcode.NotARepo},
	{"repo required en", "Error: remote pr: not inside a git repository and --repo was not given; pass --repo owner/repo.", errcode.NotARepo},
	{"repo required tr", "Hata: remote pr: bir git deposu içinde değilsiniz ve --repo verilmedi; --repo owner/repo geçin.", errcode.NotARepo},
	{"plain-text provider en", "Error: remote pr: CLI-tool-backed providers (claude-cli / gemini-cli / codex-cli) can't produce structured findings. Use an API provider (anthropic / openai / gemini / ollama).", errcode.ProviderNotAPI},
	{"plain-text provider tr", "Hata: remote pr: CLI-tabanlı provider'lar (claude-cli / gemini-cli / codex-cli) yapılandırılmış bulgu üretemez. Bir API provider kullanın (anthropic / openai / gemini / ollama).", errcode.ProviderNotAPI},
	{"self pr en", "Error: remote pr: you are the author of this PR; GitHub does not allow self-review.", errcode.SelfPR},
	{"self pr tr", "Hata: remote pr: bu PR'ın yazarı sizsiniz; GitHub kendi PR'ınızı incelemenize izin vermez.", errcode.SelfPR},
	{"too volatile en", "Error: remote pr: the PR head changed twice during review; aborted — try again once it settles.", errcode.TooVolatile},
	{"too volatile tr", "Hata: remote pr: review sırasında PR head'i iki kez değişti; iptal edildi — sakinleşince tekrar deneyin.", errcode.TooVolatile},
	{"secrets user en", "Error: aborted: pre-send secret scanner", errcode.SecretDetected},
	{"secrets user tr", "Hata: iptal edildi: pre-send secret scanner", errcode.SecretDetected},
	{"secrets non-interactive en", "Error: Aborted (non-interactive); pass --allow-secrets to override.", errcode.SecretDetected},
	{"secrets non-interactive tr", "Hata: İptal edildi (etkileşimsiz); zorlamak için --allow-secrets kullanın.", errcode.SecretDetected},
	{"cost user en", "Error: aborted: cost preflight", errcode.CostAborted},
	{"cost user tr", "Hata: iptal edildi: cost preflight", errcode.CostAborted},
	{"cost non-interactive en", "Error: Aborted (non-interactive); pass --no-cost-check or raise cost.warn_threshold_usd to override.", errcode.CostAborted},
	{"cost non-interactive tr", "Hata: İptal edildi (etkileşimsiz); zorlamak için --no-cost-check veya cost.warn_threshold_usd değerini yükseltin.", errcode.CostAborted},
	{"token preflight en", "Error: aborted: token preflight", errcode.CostAborted},
	{"timeout en", "Error: timed out after 10m0s — the run was stopped by --timeout / review.timeout, not by the provider. Pass a larger --timeout (e.g. --timeout 20m) to allow more time, or --timeout 0 to fall back to the provider's own limit.", errcode.Timeout},
	{"timeout tr", "Hata: 10m0s sonunda zaman aşımına uğradı — koşuyu durduran --timeout / review.timeout, provider değil.", errcode.Timeout},
	{"provider rate limit", "Error: anthropic: 429 rate limit exceeded", errcode.ProviderError},
	{"provider key", "Error: no API key configured for provider openai; run 'commitbrief setup'", errcode.ProviderError},
	{"provider test failed", "Error: openai test failed: dial tcp: lookup api.openai.com: no such host", errcode.ProviderError},
	{"gibberish", "Error: something nobody has seen before", ""},
}

// Informational notes the CLI prints before the failure. They contain fragments
// the rules look for, but are not the failure.
const (
	noteTokenWindow  = "ℹ  No context_window declared for deepseek: the token preflight has nothing to compare against and is skipped. Set providers.<name>.context_window to enable it.\n"
	noteInsecureHTTP = "⚠  ollama base_url uses plain http to a non-loopback host (10.0.0.5): the diff and any API key travel unencrypted. Use https:// unless the network is trusted.\n"
)

func TestErrorsNotesDoNotHijackTheClassification(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   errcode.Code
		match  bool
	}{
		{"token note then a provider 401", noteTokenWindow + "Error: deepseek: unauthorized (401)\n", errcode.ProviderError, true},
		{"token note then the real cost abort", noteTokenWindow + "Error: aborted: cost preflight\n", errcode.CostAborted, true},
		{"insecure-http note then an unrecognised failure", noteInsecureHTTP + "Error: something nobody has seen before\n", "", false},
		{"both notes then a timeout", noteTokenWindow + noteInsecureHTTP + "Hata: 10m0s sonunda zaman aşımına uğradı\n", errcode.Timeout, true},
		{"gh's wording after the prefix line", noteTokenWindow + "Error: gh pr view 1: To get started with GitHub CLI, please run:  gh auth login\nAlternatively, set GH_TOKEN.\n", errcode.GHNotAuthenticated, true},
		{"no prefixed line: the whole tail is scanned", "aborted: cost preflight\n", errcode.CostAborted, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyStderr(tc.stderr)
			if ok != tc.match || got != tc.want {
				t.Errorf("classified as %q (ok=%v), want %q (ok=%v)", got, ok, tc.want, tc.match)
			}
		})
	}
	// Through classify(): the unrecognised failure must end up unknown, not
	// provider_error because of the "API key" in the note.
	if got := classify(failure{exitCode: 1, stderr: noteInsecureHTTP + "Error: boom\n"}); got.Code != errcode.Unknown {
		t.Errorf("code = %q, want unknown", got.Code)
	}
}

func TestErrorsClassifyStderr(t *testing.T) {
	for _, tc := range stderrFixtures {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyStderr(tc.stderr)
			switch {
			case tc.want == "" && ok:
				t.Errorf("classified as %q, want no match", got)
			case tc.want != "" && (!ok || got != tc.want):
				t.Errorf("classified as %q (ok=%v), want %q", got, ok, tc.want)
			}
		})
	}
}

func TestErrorsClassifyOrder(t *testing.T) {
	rep := func(mutate func(*review.Report)) *review.Report {
		r := &review.Report{Findings: []review.Finding{}}
		mutate(r)
		return r
	}

	tests := []struct {
		name string
		f    failure
		want errcode.Code
	}{
		{
			name: "remote status wins over the stderr text",
			f: failure{exitCode: 1, stderr: "Error: aborted: cost preflight",
				report: rep(func(r *review.Report) { r.Remote = &review.Remote{Status: review.RemoteTooVolatile} })},
			want: errcode.TooVolatile,
		},
		{
			name: "remote degraded",
			f: failure{exitCode: 1, stderr: "Error: remote pr: the provider did not return structured findings; no review submitted.",
				report: rep(func(r *review.Report) { r.Remote = &review.Remote{Status: review.RemoteDegraded} })},
			want: errcode.Degraded,
		},
		{
			name: "degrade_reason wins over the stderr text",
			f: failure{exitCode: 1, stderr: "Error: timed out after 1m0s",
				report: rep(func(r *review.Report) { r.Meta.DegradeReason = "prose" })},
			want: errcode.Degraded,
		},
		{
			name: "a remote failed status falls through to stderr",
			f: failure{exitCode: 1, stderr: "Error: aborted: pre-send secret scanner",
				report: rep(func(r *review.Report) { r.Remote = &review.Remote{Status: review.RemoteFailed} })},
			want: errcode.SecretDetected,
		},
		{
			name: "exit code other than 1 carries no class even if stderr matches",
			f:    failure{exitCode: 2, stderr: "Error: aborted: cost preflight"},
			want: errcode.Unknown,
		},
		{
			name: "killed by a signal",
			f:    failure{exitCode: -1, stderr: "Error: aborted: cost preflight"},
			want: errcode.Unknown,
		},
		{
			name: "providers test failure without a recognisable marker",
			f:    failure{op: opProviderTest, exitCode: 1, stderr: "Error: openai: boom"},
			want: errcode.ProviderError,
		},
		{
			name: "unknown keeps the raw stderr",
			f:    failure{exitCode: 1, stderr: "Error: boom"},
			want: errcode.Unknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.f)
			if got.Code != tc.want {
				t.Errorf("code = %q, want %q", got.Code, tc.want)
			}
			if tc.want == errcode.Unknown && !strings.Contains(got.Detail, strings.TrimSpace(tc.f.stderr)) {
				t.Errorf("Detail = %q, want the raw stderr", got.Detail)
			}
		})
	}
}

func TestErrorsTailString(t *testing.T) {
	if got := tailString("  short \n", 100); got != "short" {
		t.Errorf("tailString = %q", got)
	}
	long := strings.Repeat("é", 50) // two bytes per rune
	got := tailString(long, 9)
	if len(got) > 9 || !strings.HasSuffix(long, got) {
		t.Errorf("tailString = %q (%d bytes)", got, len(got))
	}
	for _, r := range got {
		if r == '�' {
			t.Errorf("tailString cut a rune in half: %q", got)
		}
	}
}

// TestErrorsEveryCodeIsProducedByTheAdapter drives the real engine against the
// fake CLI and requires every member of the closed set to come out at least
// once. Adding a code to errcode without a scenario here fails the test.
func TestErrorsEveryCodeIsProducedByTheAdapter(t *testing.T) {
	seen := map[errcode.Code]string{}
	record := func(scenario string, err error, want errcode.Code) {
		t.Helper()
		got := errcode.CodeOf(err)
		if got != want {
			t.Errorf("%s: code = %q, want %q (err = %v)", scenario, got, want, err)
			return
		}
		seen[got] = scenario
	}
	ctx := context.Background()
	repo := repoDir(t)
	review := func(h *harness) error {
		_, err := h.engine.ReviewLocal(ctx, port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalStaged})
		return err
	}

	// Every stderr fixture, through a real process that exits 1.
	for _, tc := range stderrFixtures {
		want := tc.want
		if want == "" {
			want = errcode.Unknown
		}
		h := newHarness(t, true)
		h.set("FAKECLI_EXIT", "1", "FAKECLI_STDERR", tc.stderr)
		record("stderr: "+tc.name, review(h), want)
	}

	// gh preflight: no gh anywhere on the discovered PATH.
	{
		h := newHarness(t, false)
		_, err := h.engine.PreviewPR(ctx, port.PRRequest{Repo: "o/r", Number: 1})
		record("gh preflight", err, errcode.GHNotFound)
		if _, statErr := os.Stat(h.argvLog); !os.IsNotExist(statErr) {
			t.Error("the gh preflight must stop before the CLI starts")
		}
	}

	// Exit code other than 1.
	{
		h := newHarness(t, true)
		h.set("FAKECLI_EXIT", "7", "FAKECLI_STDERR", "Error: aborted: cost preflight")
		record("exit code 7", review(h), errcode.Unknown)
	}

	// Degraded: exit 0 with meta.degrade_reason. The report comes back with it.
	{
		h := newHarness(t, true)
		h.stdoutLiteral(`{"schema":1,"content":"raw model prose","findings":[],"summary":{},"meta":{"provider":"p","degrade_reason":"prose"}}`)
		rep, err := h.engine.ReviewLocal(ctx, port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalStaged})
		record("degraded exit 0", err, errcode.Degraded)
		if rep.Content != "raw model prose" || !rep.Degraded() {
			t.Errorf("a degraded review must still return its report: %+v", rep)
		}
	}
	// Degraded in posting mode: exit 1 plus remote.status=degraded.
	{
		h := newHarness(t, true)
		h.stdoutLiteral(`{"schema":1,"content":"raw","findings":[],"summary":{},"meta":{"degrade_reason":"schema"},"remote":{"status":"degraded","pr":{"repo":"o/r","number":1},"warnings":[]}}`)
		h.set("FAKECLI_EXIT", "1")
		_, err := h.engine.PreviewPR(ctx, port.PRRequest{Repo: "o/r", Number: 1})
		record("degraded remote exit 1", err, errcode.Degraded)
	}
	// Too volatile through the JSON document, with no useful stderr at all.
	{
		h := newHarness(t, true)
		h.stdoutLiteral(`{"schema":1,"content":"","findings":[],"summary":{},"meta":{},"remote":{"status":"too_volatile","pr":{"repo":"o/r","number":1},"warnings":[]}}`)
		h.set("FAKECLI_EXIT", "1")
		_, err := h.engine.PreviewPR(ctx, port.PRRequest{Repo: "o/r", Number: 1})
		record("too_volatile via remote.status", err, errcode.TooVolatile)
	}

	// cli_not_found: a configured path that does not exist ...
	{
		h := newHarness(t, true, func(c *Config) {
			c.OverridePath = func() string { return filepath.Join(t.TempDir(), "missing") }
		})
		record("override missing", review(h), errcode.CLINotFound)
	}
	// ... and nothing anywhere.
	{
		h := newHarness(t, true, func(c *Config) { c.OverridePath = nil })
		_, err := h.engine.Version(ctx)
		record("nothing found", err, errcode.CLINotFound)
	}
	// ... and a binary that is not commitbrief.
	{
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "gh version 2.40.0")
		_, err := h.engine.Version(ctx)
		record("not commitbrief", err, errcode.CLINotFound)
	}

	// cli_too_old.
	{
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.20.0 (commit a, built b)")
		record("too old", review(h), errcode.CLITooOld)
	}

	// canceled: the caller cancels mid-run.
	{
		h := newHarness(t, true)
		h.set("FAKECLI_SLEEP_MS", "10000")
		cctx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(300 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		_, err := h.engine.ReviewLocal(cctx, port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalStaged})
		record("canceled", err, errcode.Canceled)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("cancel took %v", d)
		}
	}

	// timeout: the runner's backstop fires because the process never finishes.
	{
		h := newHarness(t, true, func(c *Config) { c.BackstopSlack = 100 * time.Millisecond })
		h.set("FAKECLI_SLEEP_MS", "10000")
		start := time.Now()
		_, err := h.engine.ReviewLocal(ctx, port.LocalReviewRequest{
			RepoPath: repo, Mode: port.LocalStaged, Options: port.RunOptions{Timeout: 100 * time.Millisecond},
		})
		record("timeout backstop", err, errcode.Timeout)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("timeout took %v", d)
		}
	}

	// capability_missing.
	{
		h := newHarness(t, true)
		_, err := h.engine.PostPR(ctx, port.PRRequest{Repo: "o/r", Number: 1})
		record("post pr", err, errcode.CapabilityMissing)
		if !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Error("errors.Is(err, ErrCapabilityMissing) = false")
		}
	}

	for _, code := range errcode.All() {
		if _, ok := seen[code]; !ok {
			t.Errorf("no scenario produced %q", code)
		}
	}
}

func TestErrorsKeepRawStderrForTheLog(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKECLI_EXIT", "1", "FAKECLI_STDERR", "Error: a message the classifier has never seen")
	_, err := h.engine.ReviewLocal(context.Background(), port.LocalReviewRequest{RepoPath: repoDir(t), Mode: port.LocalStaged})
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != errcode.Unknown {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ce.Detail, "a message the classifier has never seen") {
		t.Errorf("Detail = %q, want the raw stderr", ce.Detail)
	}
}

func TestErrorsCodeHelpers(t *testing.T) {
	if errcode.CodeOf(nil) != "" {
		t.Error("CodeOf(nil) should be empty")
	}
	if errcode.CodeOf(errors.New("plain")) != errcode.Unknown {
		t.Error("an uncoded error should be unknown")
	}
	wrapped := errcode.Wrap(errcode.Timeout, "x", errors.New("cause"))
	if !errcode.Is(wrapped, errcode.Timeout) || errcode.Is(wrapped, errcode.Canceled) {
		t.Error("Is does not compare codes")
	}
	for _, c := range errcode.All() {
		if !c.Valid() {
			t.Errorf("%q is in All() but not Valid()", c)
		}
	}
	if errcode.Code("nope").Valid() {
		t.Error("an invented code is valid")
	}
	if n := len(errcode.All()); n != 16 {
		t.Errorf("the closed set has %d codes, UI-0004 lists 16", n)
	}
}
