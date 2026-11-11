// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/port"
)

func TestEngineReviewLocalReturnsTheDecodedReport(t *testing.T) {
	h := newHarness(t, true)
	h.stdoutFromTestdata("v1_findings.golden")

	var mu sync.Mutex
	var progress []string
	h.set("FAKECLI_STDERR_LINES", "3")
	rep, err := h.engine.ReviewLocal(context.Background(), port.LocalReviewRequest{
		RepoPath: repoDir(t),
		Mode:     port.LocalStaged,
		Options: port.RunOptions{OnProgress: func(l string) {
			mu.Lock()
			progress = append(progress, l)
			mu.Unlock()
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Severity != review.SeverityHigh {
		t.Errorf("report = %+v", rep)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(progress, []string{"progress line 1", "progress line 2", "progress line 3"}) {
		t.Errorf("progress = %q", progress)
	}
}

func TestEngineReviewLocalRange(t *testing.T) {
	h := newHarness(t, true)
	h.stdoutFromTestdata("v1.golden")
	_, err := h.engine.ReviewLocal(context.Background(), port.LocalReviewRequest{
		RepoPath: repoDir(t), Mode: port.LocalRange, Range: []string{"main..HEAD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.lastCall()
	if rec.Args[0] != "diff" || rec.Args[len(rec.Args)-1] != "main..HEAD" {
		t.Errorf("argv = %v", rec.Args)
	}
}

func TestEngineReviewLocalOutputProblems(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		stdout string
		set    []string
		want   errcode.Code
	}{
		"not json at exit 0":   {stdout: "hello", want: errcode.Unknown},
		"newer schema":         {stdout: `{"schema":2}`, want: errcode.Unknown},
		"empty stdout, exit 0": {stdout: "", want: errcode.Unknown},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			h.stdoutLiteral(tc.stdout)
			h.set(tc.set...)
			_, err := h.engine.ReviewLocal(ctx, localStaged(t))
			if got := errcode.CodeOf(err); got != tc.want {
				t.Errorf("code = %q, want %q (%v)", got, tc.want, err)
			}
		})
	}

	t.Run("output past the cap", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_BIG_STDOUT", "35000000", "FAKECLI_SLEEP_MS", "60000")
		start := time.Now()
		_, err := h.engine.ReviewLocal(ctx, localStaged(t))
		if got := errcode.CodeOf(err); got != errcode.Unknown {
			t.Errorf("code = %q (%v)", got, err)
		}
		if d := time.Since(start); d > 15*time.Second {
			t.Errorf("took %v", d)
		}
	})
}

func TestEnginePreviewPR(t *testing.T) {
	h := newHarness(t, true)
	h.stdoutFromTestdata("v1_findings.golden")

	rep, err := h.engine.PreviewPR(context.Background(), port.PRRequest{
		Repo: "CommitBrief/commitbrief", Number: 42, Options: port.RunOptions{Unattended: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Errorf("findings = %d", len(rep.Findings))
	}
	rec := h.lastCall()
	want := []string{"remote", "pr", "42", "--repo", "CommitBrief/commitbrief", "--no-post", "--json"}
	if !reflect.DeepEqual(rec.Args[:len(want)], want) {
		t.Errorf("argv = %v, want prefix %v", rec.Args, want)
	}
	if !contains(rec.Args, "--no-cost-check") {
		t.Errorf("an unattended preview must skip the cost prompt: %v", rec.Args)
	}
	sameDir(t, rec.Cwd, h.dataDir)

	// With a local clone the CLI runs inside it.
	clone := repoDir(t)
	if _, err := h.engine.PreviewPR(context.Background(), port.PRRequest{Repo: "o/r", Number: 1, LocalPath: clone}); err != nil {
		t.Fatal(err)
	}
	sameDir(t, h.lastCall().Cwd, clone)
}

func TestEngineCapabilitiesAndMinimumVersion(t *testing.T) {
	tests := []struct {
		version     string
		wantErr     errcode.Code
		base, inCLI bool
		setStdin    bool
	}{
		{"commitbrief 1.20.9 (commit a, built b)", errcode.CLITooOld, false, false, false},
		{"commitbrief 1.21.2 (commit a, built b)", "", true, false, false},
		{"commitbrief 1.22.0 (commit a, built b)", "", true, true, true},
		{"commitbrief dev (commit none, built unknown)", "", true, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			h := newHarness(t, true)
			h.set("FAKECLI_VERSION", tc.version)
			h.stdoutFromTestdata("v1.golden")

			caps, err := h.engine.Capabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if caps.ConfigSetStdin != tc.setStdin {
				t.Errorf("ConfigSetStdin = %v, want %v", caps.ConfigSetStdin, tc.setStdin)
			}
			if caps.Base != tc.base || caps.PostingInCLI != tc.inCLI {
				t.Errorf("caps = %+v, want base=%v postingInCLI=%v", caps, tc.base, tc.inCLI)
			}
			// The UI side of posting is wired: a screen may offer Post exactly
			// when the CLI ships the posting report and the verdict ceiling.
			if caps.Posting != tc.inCLI {
				t.Errorf("Posting = %v, want %v: %+v", caps.Posting, tc.inCLI, caps)
			}
			_, err = h.engine.ReviewLocal(context.Background(), localStaged(t))
			if got := errcode.CodeOf(err); got != tc.wantErr {
				t.Errorf("ReviewLocal code = %q, want %q (%v)", got, tc.wantErr, err)
			}
			if v, err := h.engine.Version(context.Background()); err != nil || v.Raw == "" {
				t.Errorf("Version = %+v, %v: reading the version never needs the minimum", v, err)
			}
		})
	}
}

// Posting needs a CLI that has the posting report and the verdict ceiling; an
// older one (and a source build, which proves no version) answers
// capability_missing before any process is started for the review.
func TestEnginePostPRNeedsThePostingCapability(t *testing.T) {
	for _, v := range []string{
		"commitbrief 1.21.2 (commit a, built b)",
		"commitbrief 1.22.0-rc.1 (commit a, built b)",
		"commitbrief dev (commit none, built unknown)",
	} {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", v)
		h.stdoutFromTestdata("remote_pr_report.golden.json")
		_, err := h.engine.PostPR(context.Background(), port.PRRequest{Repo: "o/r", Number: 3})
		if !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Errorf("%s: err = %v, want capability_missing", v, err)
		}
		if _, statErr := os.Stat(h.argvLog); statErr == nil {
			t.Errorf("%s: the review was started although the capability is missing", v)
		}
	}

	h := newHarness(t, true)
	h.set("FAKECLI_VERSION", "commitbrief 1.22.0 (commit a, built b)")
	if _, err := h.engine.PostPR(context.Background(), port.PRRequest{Repo: "bad", Number: 3}); errcode.Is(err, errcode.CapabilityMissing) {
		t.Error("an invalid request should be refused before the capability answer")
	}
}

func TestEnginePostPRRunsThePostingMode(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKECLI_VERSION", "commitbrief 1.22.0 (commit a, built b)")
	h.stdoutFromTestdata("remote_pr_report.golden.json")
	clone := repoDir(t)

	rep, err := h.engine.PostPR(context.Background(), port.PRRequest{
		Repo: "CommitBrief/web", Number: 42, LocalPath: clone, Options: port.RunOptions{Unattended: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Remote == nil || rep.Remote.Status != review.RemoteSubmitted || rep.Remote.HeadOID != "stable" || rep.Remote.Verdict != review.VerdictComment {
		t.Errorf("remote = %+v", rep.Remote)
	}
	rec := h.lastCall()
	if contains(rec.Args, "--no-post") {
		t.Errorf("posting mode must not carry --no-post: %v", rec.Args)
	}
	for _, want := range []string{"--json", "--since-last-review", "--no-cost-check"} {
		if !contains(rec.Args, want) {
			t.Errorf("argv %v lacks %s", rec.Args, want)
		}
	}
	sameDir(t, rec.Cwd, clone)
}

func TestEnginePostPRFailureShapes(t *testing.T) {
	ctx := context.Background()
	req := port.PRRequest{Repo: "o/r", Number: 3, Options: port.RunOptions{Unattended: true}}
	const v = "commitbrief 1.22.0 (commit a, built b)"

	t.Run("gh missing is refused before the review", func(t *testing.T) {
		h := newHarness(t, false)
		h.set("FAKECLI_VERSION", v)
		if _, err := h.engine.PostPR(ctx, req); !errcode.Is(err, errcode.GHNotFound) {
			t.Errorf("err = %v", err)
		}
		if _, statErr := os.Stat(h.argvLog); statErr == nil {
			t.Error("the CLI ran without gh")
		}
	})

	t.Run("a successful run without the remote container is not accepted", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", v)
		h.stdoutFromTestdata("v1_findings.golden")
		if _, err := h.engine.PostPR(ctx, req); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("too_volatile keeps its class", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", v, "FAKECLI_EXIT", "1")
		h.stdoutLiteral(strings.Replace(string(readTestdata(t, "remote_pr_report.golden.json")), `"status": "submitted"`, `"status": "too_volatile"`, 1))
		if _, err := h.engine.PostPR(ctx, req); !errcode.Is(err, errcode.TooVolatile) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("self pr and plain-text providers keep their class", func(t *testing.T) {
		for stderr, want := range map[string]errcode.Code{
			"Error: you are the author of this PR":              errcode.SelfPR,
			"Error: provider can't produce structured findings": errcode.ProviderNotAPI,
		} {
			h := newHarness(t, true)
			h.set("FAKECLI_VERSION", v, "FAKECLI_EXIT", "1", "FAKECLI_STDERR", stderr)
			if _, err := h.engine.PostPR(ctx, req); !errcode.Is(err, want) {
				t.Errorf("%q: err = %v, want %s", stderr, err, want)
			}
		}
	})
}

func TestEngineRelocatesWhenTheOverrideChanges(t *testing.T) {
	current := fakeBin
	h := newHarness(t, true, func(c *Config) { c.OverridePath = func() string { return current } })

	if _, err := h.engine.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	current = filepath.Join(t.TempDir(), "gone") // Settings now points at a path that does not exist
	_, err := h.engine.Version(context.Background())
	if !errcode.Is(err, errcode.CLINotFound) {
		t.Errorf("err = %v, want cli_not_found after the override changed", err)
	}
}

func TestEngineConfigAndProviders(t *testing.T) {
	ctx := context.Background()
	repo := repoDir(t)

	t.Run("config get trims the newline and sends the key after --", func(t *testing.T) {
		h := newHarness(t, true)
		h.stdoutLiteral("claude-opus-5-5\n")
		got, err := h.engine.ConfigGet(ctx, "providers.anthropic.model")
		if err != nil || got != "claude-opus-5-5" {
			t.Fatalf("got %q, %v", got, err)
		}
		a := h.lastCall().Args
		if a[0] != "config" || a[1] != "get" || a[len(a)-2] != "--" || a[len(a)-1] != "providers.anthropic.model" {
			t.Errorf("argv = %v", a)
		}
	})

	t.Run("config set local runs in the repository", func(t *testing.T) {
		h := newHarness(t, true)
		if err := h.engine.SetConfig(ctx, "output.lang", "tr", port.RepoScope(repo)); err != nil {
			t.Fatal(err)
		}
		rec := h.lastCall()
		sameDir(t, rec.Cwd, repo)
		if !contains(rec.Args, "--local") || rec.Args[len(rec.Args)-2] != "output.lang" || rec.Args[len(rec.Args)-1] != "tr" {
			t.Errorf("call = %+v", rec)
		}
	})

	t.Run("config set user scope has no --local", func(t *testing.T) {
		h := newHarness(t, true)
		if err := h.engine.SetConfig(ctx, "cache.ttl_days", "14", port.UserScope()); err != nil {
			t.Fatal(err)
		}
		if contains(h.lastCall().Args, "--local") {
			t.Error("--local present for the user scope")
		}
	})

	t.Run("a failing config set is classified", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_EXIT", "1", "FAKECLI_STDERR", "Error: unknown config key \"nope\"")
		err := h.engine.SetConfig(ctx, "nope.nope", "x", port.UserScope())
		if !errcode.Is(err, errcode.Unknown) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("providers list parses both catalogs", func(t *testing.T) {
		h := newHarness(t, true)
		h.stdoutLiteral("Configured providers (active marked with '*'):\n" +
			"  anthropic   model=claude-opus-5-5                key=sk-ant-…ABCD\n" +
			"* openai      model=gpt-5                          key=(configured)\n" +
			"  ollama      model=—                              key=http://localhost:11434\n" +
			"  gemini      model=—                              key=(not set)\n" +
			"  mistral     model=—                              key=(ayarlanmadı)\n" +
			"Switch with 'commitbrief providers use <name>'; add a new key with 'commitbrief setup'.\n")
		got, err := h.engine.Providers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := []port.ProviderInfo{
			{Name: "anthropic", Model: "claude-opus-5-5", KeyStatus: "sk-ant-…ABCD", KeyInConfig: true},
			{Name: "openai", Active: true, Model: "gpt-5", KeyStatus: "(configured)", KeyInConfig: true},
			{Name: "ollama", Model: "", KeyStatus: "http://localhost:11434", KeyInConfig: true},
			{Name: "gemini", KeyStatus: "(not set)"},
			{Name: "mistral", KeyStatus: "(ayarlanmadı)"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("providers =\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("providers list with nothing parsable is an empty slice", func(t *testing.T) {
		h := newHarness(t, true)
		got, err := h.engine.Providers(ctx)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("got %#v, %v", got, err)
		}
	})

	t.Run("providers use", func(t *testing.T) {
		h := newHarness(t, true)
		if err := h.engine.UseProvider(ctx, "openai", port.RepoScope(repo)); err != nil {
			t.Fatal(err)
		}
		rec := h.lastCall()
		sameDir(t, rec.Cwd, repo)
		if !contains(rec.Args, "--local") || rec.Args[len(rec.Args)-1] != "openai" {
			t.Errorf("call = %+v", rec)
		}
	})

	t.Run("providers test failure is a provider error", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_EXIT", "1", "FAKECLI_STDERR", "Error: openai: boom")
		if err := h.engine.TestProvider(ctx, "openai"); !errcode.Is(err, errcode.ProviderError) {
			t.Errorf("err = %v, want provider_error", err)
		}
		h.set("FAKECLI_EXIT", "0")
		if err := h.engine.TestProvider(ctx, "openai"); err != nil {
			t.Errorf("a passing test returned %v", err)
		}
		if err := h.engine.TestProvider(ctx, "--bad"); err == nil {
			t.Error("a flag-shaped provider name was accepted")
		}
	})
}

func TestEngineUpgrade(t *testing.T) {
	ctx := context.Background()

	t.Run("check decodes the report and needs no minimum version", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.10.0 (commit a, built b)")
		h.stdoutLiteral(`{"current":"1.10.0","latest":"1.21.2","method":"manual","update_available":true,"action":"download"}`)
		rep, err := h.engine.CheckUpgrade(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !rep.UpdateAvailable || rep.Latest != "1.21.2" || rep.Method != "manual" {
			t.Errorf("report = %+v", rep)
		}
		a := h.lastCall().Args
		if a[0] != "upgrade" || !contains(a, "--json") || contains(a, "--yes") {
			t.Errorf("argv = %v", a)
		}
	})

	t.Run("upgrade passes --yes and forgets the old version", func(t *testing.T) {
		h := newHarness(t, true)
		if err := h.engine.Upgrade(ctx); err != nil {
			t.Fatal(err)
		}
		a := h.lastCall().Args
		if a[0] != "upgrade" || !contains(a, "--yes") || contains(a, "--json") {
			t.Errorf("argv = %v", a)
		}
		h.engine.mu.Lock()
		cached := h.engine.loc != nil
		h.engine.mu.Unlock()
		if cached {
			t.Error("the located binary must be forgotten after an upgrade")
		}
	})

	t.Run("garbage report", func(t *testing.T) {
		h := newHarness(t, true)
		h.stdoutLiteral("not json")
		if _, err := h.engine.CheckUpgrade(ctx); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestEngineHonoursACanceledContextBeforeStarting(t *testing.T) {
	h := newHarness(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.engine.ReviewLocal(ctx, localStaged(t))
	if !errcode.Is(err, errcode.Canceled) {
		t.Errorf("err = %v, want canceled", err)
	}
}

// TestEngineSecretsGoThroughStdinOnly: the fake CLI records what it was given;
// the secret must be on stdin and nowhere in argv or in the environment the
// adapter built.
func TestEngineSecretsGoThroughStdinOnly(t *testing.T) {
	ctx := context.Background()
	const secret = "sk-ant-api03-TOPSECRET-0123456789"

	t.Run("CLI with config_set_stdin", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.22.0 (commit a, built b)")
		repo := repoDir(t)
		for _, scope := range []port.Scope{port.UserScope(), port.RepoScope(repo)} {
			if err := h.engine.SetConfig(ctx, "providers.anthropic.api_key", secret, scope); err != nil {
				t.Fatal(err)
			}
			rec := h.lastCall()
			for _, a := range rec.Args {
				if strings.Contains(a, secret) {
					t.Fatalf("the secret is in argv: %v", rec.Args)
				}
			}
			if rec.Stdin != secret {
				t.Errorf("stdin = %q, want the secret exactly (no newline)", rec.Stdin)
			}
			if n := len(rec.Args); rec.Args[n-1] != "-" || rec.Args[n-2] != "providers.anthropic.api_key" || rec.Args[n-3] != "--" {
				t.Errorf("argv = %v", rec.Args)
			}
			if scope.Local != contains(rec.Args, "--local") {
				t.Errorf("--local mismatch for scope %+v: %v", scope, rec.Args)
			}
		}
	})

	t.Run("older CLI: capability_missing and no process", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.21.2 (commit a, built b)")
		err := h.engine.SetConfig(ctx, "providers.anthropic.api_key", secret, port.UserScope())
		if !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Fatalf("err = %v, want capability_missing", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Error("the error text contains the secret")
		}
		if _, statErr := os.Stat(h.argvLog); !os.IsNotExist(statErr) {
			t.Error("the CLI was started although the secret has no safe way in: there must be no argv fallback")
		}
	})

	t.Run("the value - is routed like a secret", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.21.2 (commit a, built b)")
		if err := h.engine.SetConfig(ctx, "output.lang", "-", port.UserScope()); !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Errorf("err = %v: an old CLI would take - literally, a new one would read stdin", err)
		}
		h.set("FAKECLI_VERSION", "commitbrief 1.22.0 (commit a, built b)")
		h.engine.Invalidate() // the version is cached per located binary
		if err := h.engine.SetConfig(ctx, "output.lang", "-", port.UserScope()); err != nil {
			t.Fatal(err)
		}
		if rec := h.lastCall(); rec.Stdin != "-" {
			t.Errorf("stdin = %q", rec.Stdin)
		}
	})

	t.Run("a non-secret value stays on argv and needs no new CLI", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.21.2 (commit a, built b)")
		if err := h.engine.SetConfig(ctx, "output.lang", "tr", port.UserScope()); err != nil {
			t.Fatal(err)
		}
		rec := h.lastCall()
		if rec.Args[len(rec.Args)-1] != "tr" || rec.StdinBytes != 0 {
			t.Errorf("call = %+v", rec)
		}
	})

	t.Run("a failing write never echoes the secret", func(t *testing.T) {
		h := newHarness(t, true)
		h.set("FAKECLI_VERSION", "commitbrief 1.22.0 (commit a, built b)",
			"FAKECLI_EXIT", "1", "FAKECLI_STDERR", "Error: could not store "+secret+" in the config")
		err := h.engine.SetConfig(ctx, "providers.anthropic.api_key", secret, port.UserScope())
		var ce *errcode.Error
		if !errors.As(err, &ce) {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(ce.Detail, secret) || strings.Contains(ce.Error(), secret) {
			t.Errorf("the secret leaked into the error: %q", ce.Detail)
		}
		if !strings.Contains(ce.Detail, "***") {
			t.Errorf("Detail = %q, want the secret replaced", ce.Detail)
		}
	})

	t.Run("reading a secret key is refused", func(t *testing.T) {
		h := newHarness(t, true)
		for _, k := range []string{"providers.anthropic.api_key", "providers.x.token"} {
			if _, err := h.engine.ConfigGet(ctx, k); err == nil {
				t.Errorf("ConfigGet(%q) was allowed", k)
			}
		}
	})

	t.Run("a secret never appears in any argv the adapter can build", func(t *testing.T) {
		for name, args := range allBuilders(t) {
			for _, a := range args {
				if strings.Contains(a, "sk-secret-value-123456") {
					t.Errorf("%s: argv carries the secret: %v", name, args)
				}
			}
		}
	})
}

// TestEngineDevBuildNeverGetsASecretWritten: a source build cannot prove it reads
// stdin, and a CLI that does not would store the literal "-" as the API key.
func TestEngineDevBuildNeverGetsASecretWritten(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKECLI_VERSION", "commitbrief dev (commit none, built unknown)")
	ctx := context.Background()

	for _, tc := range []struct{ key, value string }{
		{"providers.anthropic.api_key", "sk-ant-api03-DEVSECRET"},
		{"output.lang", "-"},
	} {
		err := h.engine.SetConfig(ctx, tc.key, tc.value, port.UserScope())
		if !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Errorf("SetConfig(%q) on a dev build = %v, want capability_missing", tc.key, err)
		}
	}
	if _, statErr := os.Stat(h.argvLog); !os.IsNotExist(statErr) {
		t.Error("the dev build was started for a secret write")
	}

	// Everything the base needs still works on a dev build.
	if err := h.engine.SetConfig(ctx, "output.lang", "tr", port.UserScope()); err != nil {
		t.Errorf("an ordinary write on a dev build: %v", err)
	}
	caps, err := h.engine.Capabilities(ctx)
	if err != nil || !caps.Base || caps.ConfigSetStdin || caps.PostingInCLI {
		t.Errorf("caps = %+v, %v", caps, err)
	}
}
