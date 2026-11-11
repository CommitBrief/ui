// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

func TestArgvRepoValidation(t *testing.T) {
	valid := []string{"CommitBrief/commitbrief", "a/b", "my-org/my.repo_name-2", "A1/.github"}
	for _, r := range valid {
		if err := validateRepo(r); err != nil {
			t.Errorf("validateRepo(%q) = %v, want nil", r, err)
		}
	}
	invalid := []string{
		"", "owner", "owner/", "/name", "a/b/c", "a/b c", "a/b;ls", "a/$(id)", "a/b\n", "a/b\x00",
		"own er/name", "a/..", "a/.", "../b", "a/b|c", "a/b`c`", "a/b&c", "a/b\\c",
		strings.Repeat("a", 300) + "/b",
	}
	for _, r := range invalid {
		err := validateRepo(r)
		if err == nil {
			t.Errorf("validateRepo(%q) = nil, want a refusal", r)
			continue
		}
		if got := errcode.CodeOf(err); got != errcode.Unknown {
			t.Errorf("validateRepo(%q) code = %q, want unknown", r, got)
		}
	}
}

func TestArgvPRNumberValidation(t *testing.T) {
	for _, n := range []int{1, 42, 1<<31 - 1} {
		if err := validatePRNumber(n); err != nil {
			t.Errorf("validatePRNumber(%d) = %v", n, err)
		}
	}
	for _, n := range []int{0, -1, -42, 1 << 31} {
		if err := validatePRNumber(n); err == nil {
			t.Errorf("validatePRNumber(%d) = nil, want a refusal", n)
		}
	}
}

func TestArgvLocalPathValidation(t *testing.T) {
	repo := repoDir(t)
	if err := validateLocalPath(repo); err != nil {
		t.Errorf("repo root refused: %v", err)
	}

	// A worktree's .git is a file.
	wt, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPath(wt); err != nil {
		t.Errorf("worktree root refused: %v", err)
	}

	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(plain, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		code errcode.Code
	}{
		{"empty", "", errcode.Unknown},
		{"relative", "some/repo", errcode.Unknown},
		{"dot-dot", repo + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(repo), errcode.Unknown},
		{"trailing separator", repo + string(filepath.Separator), errcode.Unknown},
		{"missing", filepath.Join(plain, "nope"), errcode.NotARepo},
		{"plain directory", plain, errcode.NotARepo},
		{"a file", file, errcode.NotARepo},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLocalPath(tc.path)
			if err == nil {
				t.Fatalf("validateLocalPath(%q) = nil, want a refusal", tc.path)
			}
			if got := errcode.CodeOf(err); got != tc.code {
				t.Errorf("code = %q, want %q (%v)", got, tc.code, err)
			}
		})
	}
}

func TestArgvRevisionValidation(t *testing.T) {
	for _, r := range []string{"main", "main..HEAD", "origin/main...feature/x", "HEAD~3", "v1.2.3", "a1b2c3d", "HEAD^"} {
		if err := validateRevision(r); err != nil {
			t.Errorf("validateRevision(%q) = %v", r, err)
		}
	}
	// A leading dash would make git read the argument as an option, and
	// `--output=FILE` would then write a file.
	for _, r := range []string{
		"", "--output=/tmp/x", "-p", "--allow-secrets", "main;ls", "a b", "main\n", "$(id)", "`id`", "a|b", "--", "-",
	} {
		if err := validateRevision(r); err == nil {
			t.Errorf("validateRevision(%q) = nil, want a refusal", r)
		}
	}
}

func TestArgvConfigAndProviderValidation(t *testing.T) {
	for _, k := range []string{"provider", "output.lang", "cache.ttl_days", "providers.anthropic.model", "providers.openai-compatible.base_url"} {
		if err := validateConfigKey(k); err != nil {
			t.Errorf("validateConfigKey(%q) = %v", k, err)
		}
	}
	for _, k := range []string{"", "-x", "--local", "a b", "a..b", ".a", "a.", "a;b", "A.b", "a/b"} {
		if err := validateConfigKey(k); err == nil {
			t.Errorf("validateConfigKey(%q) = nil, want a refusal", k)
		}
	}
	for _, n := range []string{"openai", "claude-cli", "openai-compatible"} {
		if err := validateProviderName(n); err != nil {
			t.Errorf("validateProviderName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "-x", "--local", "a b", "a;b", strings.Repeat("a", 65)} {
		if err := validateProviderName(n); err == nil {
			t.Errorf("validateProviderName(%q) = nil, want a refusal", n)
		}
	}
	if err := validateConfigValue("sk-ant-xyz"); err != nil {
		t.Errorf("plain value refused: %v", err)
	}
	if err := validateConfigValue("a\x00b"); err == nil {
		t.Error("NUL byte accepted")
	}
	if err := validateConfigValue(strings.Repeat("x", maxConfigValue+1)); err == nil {
		t.Error("oversized value accepted")
	}
}

func TestArgvCommonFlags(t *testing.T) {
	interactive := commonFlags(10*time.Minute, false)
	if i := indexOf(interactive, "--color"); i < 0 || interactive[i+1] != "never" {
		t.Errorf("--color never missing: %v", interactive)
	}
	if i := indexOf(interactive, "--timeout"); i < 0 || interactive[i+1] != "600s" {
		t.Errorf("--timeout 600s missing: %v", interactive)
	}
	if contains(interactive, "--no-cost-check") || contains(interactive, "--sandbox-rerun=0") {
		t.Errorf("an interactive call must keep the cost check: %v", interactive)
	}

	unattended := commonFlags(90*time.Second, true)
	if !contains(unattended, "--no-cost-check") || !contains(unattended, "--sandbox-rerun=0") {
		t.Errorf("an unattended call needs --no-cost-check and --sandbox-rerun=0: %v", unattended)
	}
}

func TestArgvTimeoutFormat(t *testing.T) {
	tests := map[time.Duration]string{
		10 * time.Minute:        "600s",
		90 * time.Second:        "90s",
		1500 * time.Millisecond: "2s", // rounded up, never shorter than asked
		time.Millisecond:        "1s",
		0:                       "1s",
	}
	for d, want := range tests {
		if got := formatTimeout(d); got != want {
			t.Errorf("formatTimeout(%v) = %q, want %q", d, got, want)
		}
	}
}

// allBuilders returns every argv the adapter can build, for invariants that
// must hold for all of them.
func allBuilders(t *testing.T) map[string][]string {
	t.Helper()
	repo := repoDir(t)
	out := map[string][]string{}
	add := func(name string, args []string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = args
	}
	for _, unattended := range []bool{false, true} {
		suffix := map[bool]string{false: "/interactive", true: "/unattended"}[unattended]
		opts := port.RunOptions{Unattended: unattended}
		for _, mode := range []port.LocalMode{port.LocalStaged, port.LocalUnstaged} {
			a, err := localReviewArgs(port.LocalReviewRequest{RepoPath: repo, Mode: mode, Options: opts}, time.Minute)
			add("local "+string(mode)+suffix, a, err)
		}
		a, err := localReviewArgs(port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalRange, Range: []string{"main", "HEAD"}, Options: opts}, time.Minute)
		add("local range"+suffix, a, err)
		a, err = previewPRArgs(port.PRRequest{Repo: "o/r", Number: 5, Options: opts}, time.Minute)
		add("preview pr"+suffix, a, err)
	}
	a, err := configGetArgs("provider", time.Minute)
	add("config get", a, err)
	a, _, _, err = configSetArgs("output.lang", "tr", port.UserScope(), time.Minute)
	add("config set user", a, err)
	a, _, _, err = configSetArgs("output.lang", "tr", port.RepoScope(repo), time.Minute)
	add("config set local", a, err)
	a, _, _, err = configSetArgs("providers.openai.api_key", "sk-secret-value-123456", port.UserScope(), time.Minute)
	add("config set secret", a, err)
	out["providers list"] = providersListArgs(time.Minute)
	a, err = providersUseArgs("openai", port.UserScope(), time.Minute)
	add("providers use", a, err)
	a, err = providersTestArgs("openai", time.Minute)
	add("providers test", a, err)
	out["upgrade check"] = upgradeCheckArgs()
	out["upgrade"] = upgradeArgs()
	return out
}

// TestArgvNeverAllowSecrets locks the rule from UI-0003 section 4: the UI never
// disables the CLI's secret scanner, on any call.
func TestArgvNeverAllowSecrets(t *testing.T) {
	for name, args := range allBuilders(t) {
		for _, a := range args {
			if a == "--allow-secrets" || strings.HasPrefix(a, "--allow-secrets") {
				t.Errorf("%s: argv contains %q: %v", name, a, args)
			}
		}
		if err := assertNoForbiddenArgs(args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The gate itself, including the forms a value could smuggle in.
	for _, bad := range [][]string{
		{"--allow-secrets"},
		{"--allow-secrets=true"},
		{"config", "set", "--", "k", "--allow-secrets"},
	} {
		if err := assertNoForbiddenArgs(bad); err == nil {
			t.Errorf("assertNoForbiddenArgs(%v) = nil, want a refusal", bad)
		}
	}
}

// TestArgvRevisionCannotSmuggleAFlag drives the whole builder with hostile
// revisions.
func TestArgvRevisionCannotSmuggleAFlag(t *testing.T) {
	repo := repoDir(t)
	for _, r := range []string{"--allow-secrets", "--output=/tmp/pwned", "-p", "--no-index"} {
		_, err := localReviewArgs(port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalRange, Range: []string{r}}, time.Minute)
		if err == nil {
			t.Errorf("range %q was accepted", r)
		}
	}
	if _, err := localReviewArgs(port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalRange}, time.Minute); err == nil {
		t.Error("an empty range was accepted")
	}
	if _, err := localReviewArgs(port.LocalReviewRequest{RepoPath: repo, Mode: "whatever"}, time.Minute); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestArgvShapes(t *testing.T) {
	b := allBuilders(t)

	if a := b["local range/unattended"]; a[0] != "diff" || !contains(a, "--json") || a[len(a)-2] != "main" || a[len(a)-1] != "HEAD" {
		t.Errorf("range review argv = %v", a)
	}
	if a := b["local staged/interactive"]; a[len(a)-1] != "--staged" || !contains(a, "--json") {
		t.Errorf("staged review argv = %v", a)
	}
	a := b["preview pr/unattended"]
	if a[0] != "remote" || a[1] != "pr" || a[2] != "5" || !contains(a, "--no-post") || !contains(a, "--json") {
		t.Errorf("preview argv = %v", a)
	}
	if i := indexOf(a, "--repo"); i < 0 || a[i+1] != "o/r" {
		t.Errorf("--repo missing: %v", a)
	}

	// Values that could be read as flags sit after `--`.
	set := b["config set local"]
	dd := indexOf(set, "--")
	if dd < 0 || set[dd+1] != "output.lang" || set[dd+2] != "tr" || indexOf(set, "--local") > dd {
		t.Errorf("config set argv = %v", set)
	}
	hostile, _, _, err := configSetArgs("output.lang", "-rf", port.UserScope(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if dd := indexOf(hostile, "--"); dd < 0 || indexOf(hostile, "-rf") < dd {
		t.Errorf("a dash-led value must follow --: %v", hostile)
	}
	for _, name := range []string{"config get", "providers use", "providers test"} {
		if indexOf(b[name], "--") < 0 {
			t.Errorf("%s argv has no -- guard: %v", name, b[name])
		}
	}

	// Upgrade calls use only flags every CLI with `upgrade` has.
	if !contains(b["upgrade check"], "--json") || contains(b["upgrade check"], "--timeout") {
		t.Errorf("upgrade check argv = %v", b["upgrade check"])
	}
	if !contains(b["upgrade"], "--yes") {
		t.Errorf("upgrade argv = %v", b["upgrade"])
	}
}

// TestArgvReachesTheProcessIntact runs the real adapter against the fake CLI and
// checks what the process actually saw: argv, working directory, environment and
// stdin.
func TestArgvReachesTheProcessIntact(t *testing.T) {
	h := newHarness(t, true)
	h.stdoutFromTestdata("v1.golden")
	h.set("NO_COLOR", "0", "GIT_TERMINAL_PROMPT", "1") // the adapter must override these
	repo := repoDir(t)

	_, err := h.engine.ReviewLocal(context.Background(), port.LocalReviewRequest{
		RepoPath: repo,
		Mode:     port.LocalStaged,
		Options:  port.RunOptions{Unattended: true, Timeout: 2 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.lastCall()
	for _, want := range []string{"--json", "--staged", "--no-cost-check", "--sandbox-rerun=0"} {
		if !contains(rec.Args, want) {
			t.Errorf("argv %v lacks %s", rec.Args, want)
		}
	}
	if i := indexOf(rec.Args, "--timeout"); i < 0 || rec.Args[i+1] != "120s" {
		t.Errorf("argv %v lacks --timeout 120s", rec.Args)
	}
	if contains(rec.Args, "--allow-secrets") {
		t.Errorf("argv %v contains --allow-secrets", rec.Args)
	}
	sameDir(t, rec.Cwd, repo)
	if rec.StdinBytes != 0 {
		t.Errorf("stdin delivered %d bytes, want the null device", rec.StdinBytes)
	}
	if rec.Env["NO_COLOR"] != "1" || rec.Env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("env = %v, want NO_COLOR=1 and GIT_TERMINAL_PROMPT=0", rec.Env)
	}
	if !strings.Contains(rec.Env["PATH"], h.binDir) {
		t.Errorf("child PATH %q lacks the discovered directory %q", rec.Env["PATH"], h.binDir)
	}

	// Interactive: the cost check stays on.
	if _, err := h.engine.ReviewLocal(context.Background(), port.LocalReviewRequest{RepoPath: repo, Mode: port.LocalUnstaged}); err != nil {
		t.Fatal(err)
	}
	rec = h.lastCall()
	if contains(rec.Args, "--no-cost-check") || contains(rec.Args, "--sandbox-rerun=0") {
		t.Errorf("interactive argv = %v", rec.Args)
	}
	if i := indexOf(rec.Args, "--timeout"); i < 0 || rec.Args[i+1] != "600s" {
		t.Errorf("default timeout should be 600s: %v", rec.Args)
	}
}

func TestArgvRefusedRequestsNeverStartAProcess(t *testing.T) {
	h := newHarness(t, true)
	h.stdoutFromTestdata("v1.golden")
	ctx := context.Background()

	for _, req := range []port.PRRequest{
		{Repo: "bad repo", Number: 1},
		{Repo: "o/r", Number: 0},
		{Repo: "o/r", Number: -3},
		{Repo: "o/r", Number: 1, LocalPath: "relative/path"},
	} {
		if _, err := h.engine.PreviewPR(ctx, req); err == nil {
			t.Errorf("PreviewPR(%+v) = nil error, want a refusal", req)
		}
	}
	if _, err := h.engine.ReviewLocal(ctx, port.LocalReviewRequest{RepoPath: "relative", Mode: port.LocalStaged}); err == nil {
		t.Error("a relative repo path was accepted")
	}
	if _, err := h.engine.ConfigGet(ctx, "providers.openai.api_key"); err == nil {
		t.Error("reading an api_key was allowed")
	}
	if err := h.engine.SetConfig(ctx, "--local", "x", port.UserScope()); err == nil {
		t.Error("a flag-shaped key was accepted")
	}
	if err := h.engine.SetConfig(ctx, "output.lang", "tr", port.RepoScope(t.TempDir())); err == nil {
		t.Error("a local scope on a non-repository was accepted")
	}
	if _, err := os.Stat(h.argvLog); !os.IsNotExist(err) {
		t.Errorf("a refused request still started the CLI (argv log exists, err=%v)", err)
	}
}

func TestArgvIsSecretKey(t *testing.T) {
	for _, k := range []string{
		"providers.anthropic.api_key", "providers.openai-compatible.api_key", "providers.x.API_KEY",
		"providers.x.token", "some.secret", "a.b.password", "a.b.service_token", "a.deploy_secret", "a.b.passphrase",
	} {
		if !isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{
		"provider", "output.lang", "providers.anthropic.api_key_env", "providers.anthropic.model",
		"providers.x.base_url", "guard.token_preflight", "guard.secret_scan", "guard.secret_patterns",
		"cost.warn_threshold_usd", "review.timeout",
	} {
		if isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = true, want false", k)
		}
	}
}

// TestArgvSecretValueNeverInArgv locks the rule: a secret goes to the CLI on
// stdin as `config set <key> -`, never as an argument.
func TestArgvSecretValueNeverInArgv(t *testing.T) {
	const secret = "sk-ant-api03-SUPERSECRET-value"
	for _, scope := range []port.Scope{port.UserScope(), port.RepoScope(repoDir(t))} {
		args, stdin, viaStdin, err := configSetArgs("providers.anthropic.api_key", secret, scope, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !viaStdin || string(stdin) != secret {
			t.Errorf("viaStdin=%v stdin=%q", viaStdin, stdin)
		}
		for _, a := range args {
			if strings.Contains(a, secret) {
				t.Errorf("argv contains the secret: %v", args)
			}
		}
		if n := len(args); args[n-3] != "--" || args[n-2] != "providers.anthropic.api_key" || args[n-1] != "-" {
			t.Errorf("argv tail = %v, want -- <key> -", args)
		}
	}

	// The literal "-" would be read as "from stdin" by the CLI, so it goes the
	// same way even for a non-secret key.
	args, stdin, viaStdin, err := configSetArgs("output.lang", "-", port.UserScope(), time.Minute)
	if err != nil || !viaStdin || string(stdin) != "-" || args[len(args)-1] != "-" {
		t.Errorf("value \"-\": args=%v stdin=%q viaStdin=%v err=%v", args, stdin, viaStdin, err)
	}

	// An ordinary value stays an argument.
	args, stdin, viaStdin, err = configSetArgs("output.lang", "tr", port.UserScope(), time.Minute)
	if err != nil || viaStdin || stdin != nil || args[len(args)-1] != "tr" {
		t.Errorf("ordinary value: args=%v stdin=%q viaStdin=%v err=%v", args, stdin, viaStdin, err)
	}

	for _, bad := range []string{"", "two\nlines", "cr\rvalue"} {
		if _, _, _, err := configSetArgs("providers.x.api_key", bad, port.UserScope(), time.Minute); err == nil {
			t.Errorf("secret value %q was accepted", bad)
		}
	}
}

// The bot posts under the user's own name (UI-0008): the ceiling is part of the
// argv, always, and the call is never a preview.
func TestBotPostArgvCarriesTheVerdictCeiling(t *testing.T) {
	args, err := postPRArgs(port.PRRequest{Repo: "Acme/App", Number: 7, Options: port.RunOptions{Unattended: true}}, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	i := indexOf(args, "--max-verdict")
	if i < 0 || i+1 >= len(args) || args[i+1] != "comment" {
		t.Fatalf("argv %v must carry --max-verdict comment", args)
	}
	if contains(args, "--no-post") || contains(args, flagAllowSecrets) {
		t.Errorf("argv %v: posting mode has no --no-post and never --allow-secrets", args)
	}
	if !contains(args, "--since-last-review") || !contains(args, "--json") || !contains(args, "--no-cost-check") {
		t.Errorf("argv %v lacks an incremental, machine-readable, unattended run", args)
	}
	want := []string{"remote", "pr", "7", "--repo", "Acme/App"}
	if !reflect.DeepEqual(args[:len(want)], want) {
		t.Errorf("argv = %v, want prefix %v", args, want)
	}
	if assertNoForbiddenArgs(args) != nil {
		t.Error("the posting argv trips the forbidden-flag gate")
	}

	// The same input checks as a preview.
	for _, bad := range []port.PRRequest{{Repo: "a/b;ls", Number: 1}, {Repo: "a/b", Number: 0}, {Repo: "a/b", Number: -3}, {Repo: "ab", Number: 1}} {
		if _, err := postPRArgs(bad, time.Minute); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}
