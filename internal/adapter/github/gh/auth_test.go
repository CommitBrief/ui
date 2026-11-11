// SPDX-License-Identifier: GPL-3.0-or-later

package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

// fakeBin is the stand-in gh executable, built once for the test run under the
// name gh (gh.exe on Windows) so that PATH lookup finds it.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gh-fakegh-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		os.Exit(2)
	}
	fakeBin = filepath.Join(dir, ghExeName())

	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the go tool is needed on PATH to build the fake gh:", err)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	build := exec.Command(goBin, "build", "-o", fakeBin, "./testdata/fakegh")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakegh: %v\n%s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testEnv is a port.ProcessEnv: the real environment overlaid with fixed values.
type testEnv struct {
	vars map[string]string
	home string
}

func (e testEnv) Environ() []string {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range e.vars {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func (e testEnv) LookupEnv(key string) (string, bool) {
	v, ok := e.vars[key]
	return v, ok
}

func (e testEnv) HomeDir() (string, error) { return e.home, nil }

// harness is a Client whose PATH holds only the fake gh (or nothing).
type harness struct {
	t       *testing.T
	client  *Client
	vars    map[string]string
	binDir  string
	argvLog string
}

func newHarness(t *testing.T, withGH bool, mutate ...func(*Config)) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{
		t:       t,
		vars:    map[string]string{},
		binDir:  filepath.Join(root, "bin"),
		argvLog: filepath.Join(root, "argv.json"),
	}
	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withGH {
		data, err := os.ReadFile(fakeBin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.binDir, ghExeName()), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.vars["PATH"] = h.binDir
	h.vars["FAKEGH_ARGV_FILE"] = h.argvLog
	cfg := Config{
		Env: testEnv{vars: h.vars, home: root},
		// A fixed Probe and KnownDirs keep tests from starting the developer's
		// login shell or finding the developer's own gh.
		Probe:     func(context.Context) (string, error) { return "", nil },
		KnownDirs: func() []string { return nil },
		Timeout:   10 * time.Second,
		KillGrace: 200 * time.Millisecond,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	h.client = New(cfg)
	return h
}

func (h *harness) set(kv ...string) {
	h.t.Helper()
	for i := 0; i+1 < len(kv); i += 2 {
		h.vars[kv[i]] = kv[i+1]
	}
}

type argvRecord struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

func (h *harness) lastCall() argvRecord {
	h.t.Helper()
	b, err := os.ReadFile(h.argvLog)
	if err != nil {
		h.t.Fatalf("fakegh recorded nothing: %v", err)
	}
	var rec argvRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		h.t.Fatal(err)
	}
	return rec
}

const loggedInNew = `github.com
  ✓ Logged in to github.com account octocat (keyring)
  - Active account: true
  - Git operations protocol: https
  - Token: gho_************************************
  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'`

func TestAuthStatusLoggedIn(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_STDOUT", loggedInNew)

	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := port.AuthStatus{
		Installed: true, Path: filepath.Join(h.binDir, ghExeName()),
		Authenticated: true, Host: "github.com", Account: "octocat",
		Scopes: []string{"gist", "read:org", "repo", "workflow"},
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("status = %+v\nwant     %+v", st, want)
	}

	rec := h.lastCall()
	if !reflect.DeepEqual(rec.Args, []string{"auth", "status", "--hostname", "github.com"}) {
		t.Errorf("argv = %v", rec.Args)
	}
	for _, a := range rec.Args {
		if strings.Contains(a, "token") {
			t.Errorf("argv %v must never ask for the token", rec.Args)
		}
	}
	if rec.Env["GH_PROMPT_DISABLED"] != "1" || rec.Env["NO_COLOR"] != "1" ||
		rec.Env["GH_NO_UPDATE_NOTIFIER"] != "1" || rec.Env["GH_PAGER"] != "cat" {
		t.Errorf("gh must run non-interactively, got env %v", rec.Env)
	}
	if rec.Env["PATH"] != h.binDir {
		t.Errorf("PATH = %q, want the discovered %q", rec.Env["PATH"], h.binDir)
	}

	// Nothing secret reaches the result, even though gh printed a masked token.
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "gho_") || strings.Contains(string(b), "****") {
		t.Errorf("the result carries token text: %s", b)
	}
}

func TestAuthStatusOlderFormat(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_STDOUT", `github.com
  ✓ Logged in to github.com as octocat (GH_TOKEN)
  ✓ Git operations for github.com configured to use https protocol.
  ✓ Token: *******************
  ✓ Token scopes: gist, read:org, repo`)

	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Authenticated || st.Account != "octocat" || !reflect.DeepEqual(st.Scopes, []string{"gist", "read:org", "repo"}) {
		t.Errorf("status = %+v", st)
	}
}

func TestAuthStatusNotInstalled(t *testing.T) {
	h := newHarness(t, false)
	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("a missing gh is a status, not an error: %v", err)
	}
	if st.Installed || st.Authenticated || st.Path != "" || st.Account != "" || st.Problem != "" || st.Host != "github.com" {
		t.Errorf("status = %+v", st)
	}
	if st.Scopes == nil {
		t.Error("Scopes must be an empty slice (JSON []), not nil")
	}
}

func TestAuthStatusNotLoggedIn(t *testing.T) {
	// gh's wording differs by version and by what else is logged in.
	for name, stderr := range map[string]string{
		"no hosts":                  "You are not logged into any GitHub hosts. To log in, run: gh auth login",
		"only another host":         "You are not logged into any accounts on github.com",
		"reworded, hint kept":       "no accounts are configured for github.com; to log in, run: gh auth login",
		"only another host (older)": "You are not logged into any accounts on github.com. To log in, run: gh auth login --hostname github.com",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			h.set("FAKEGH_STDERR", stderr, "FAKEGH_EXIT", "1")

			st, err := h.client.AuthStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !st.Installed || st.Authenticated || st.Problem != port.AuthProblemNotLoggedIn || st.Account != "" {
				t.Errorf("status = %+v", st)
			}
		})
	}
}

func TestAuthStatusUnreachable(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_STDERR", `github.com
  X Timeout trying to log in to github.com account octocat (keyring)
  - Active account: true`, "FAKEGH_EXIT", "1")

	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Authenticated || st.Problem != port.AuthProblemUnreachable || st.Account != "octocat" {
		t.Errorf("status = %+v, want the account kept and the problem unreachable", st)
	}
}

func TestAuthStatusTokenInvalid(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_STDERR", `github.com
  X Failed to log in to github.com account octocat (keyring)
  - Active account: true
  - The token in keyring is invalid.
  - To re-authenticate, run: gh auth refresh -h github.com
  - To forget about this account, run: gh auth logout -h github.com -u octocat`, "FAKEGH_EXIT", "1")

	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || st.Authenticated || st.Problem != port.AuthProblemTokenInvalid || st.Account != "octocat" {
		t.Errorf("status = %+v", st)
	}
}

func TestAuthStatusActiveAccountDecides(t *testing.T) {
	// gh exits 1 when any account has a problem, but the active one works.
	h := newHarness(t, true)
	h.set("FAKEGH_STDERR", `github.com
  X Failed to log in to github.com account old-account (keyring)
  - Active account: false
  - The token in keyring is invalid.

  ✓ Logged in to github.com account current-account (keyring)
  - Active account: true
  - Token scopes: 'repo'`, "FAKEGH_EXIT", "1")

	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Authenticated || st.Account != "current-account" || !reflect.DeepEqual(st.Scopes, []string{"repo"}) {
		t.Errorf("status = %+v", st)
	}
}

func TestAuthStatusUnrecognizedOutputIsAnError(t *testing.T) {
	for name, env := range map[string][]string{
		"exit 0":  {"FAKEGH_STDOUT", "something entirely different"},
		"exit 1":  {"FAKEGH_STDERR", "error connecting to api.github.com", "FAKEGH_EXIT", "1"},
		"silence": {"FAKEGH_EXIT", "1"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			h.set(env...)
			st, err := h.client.AuthStatus(context.Background())
			if !errcode.Is(err, errcode.Unknown) {
				t.Fatalf("err = %v, want unknown", err)
			}
			if !st.Installed {
				t.Error("the status must still say gh is installed")
			}
			var ce *errcode.Error
			if errors.As(err, &ce) && name == "exit 1" && !strings.Contains(ce.Detail, "api.github.com") {
				t.Errorf("detail = %q, want gh's own words for the log", ce.Detail)
			}
		})
	}
}

func TestAuthStatusTimeout(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.Timeout = 300 * time.Millisecond })
	h.set("FAKEGH_SLEEP_MS", "10000", "FAKEGH_STDOUT", loggedInNew)
	start := time.Now()
	_, err := h.client.AuthStatus(context.Background())
	if !errcode.Is(err, errcode.Timeout) {
		t.Fatalf("err = %v, want timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the call took %v; the process was not stopped", d)
	}
}

func TestAuthStatusCanceled(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_SLEEP_MS", "10000")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	_, err := h.client.AuthStatus(ctx)
	if !errcode.Is(err, errcode.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	// An already canceled context never starts a process.
	h2 := newHarness(t, true)
	if _, err := h2.client.AuthStatus(ctx); !errcode.Is(err, errcode.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if _, statErr := os.Stat(h2.argvLog); statErr == nil {
		t.Error("gh was started under a canceled context")
	}
}

func TestAuthDiscoveryOrderAndSources(t *testing.T) {
	// gh only in a "known location".
	t.Run("known location", func(t *testing.T) {
		known := t.TempDir()
		data, _ := os.ReadFile(fakeBin)
		if err := os.WriteFile(filepath.Join(known, ghExeName()), data, 0o755); err != nil {
			t.Fatal(err)
		}
		h := newHarness(t, false, func(c *Config) { c.KnownDirs = func() []string { return []string{known} } })
		h.set("FAKEGH_STDOUT", loggedInNew)
		st, err := h.client.AuthStatus(context.Background())
		if err != nil || !st.Installed || !st.Authenticated {
			t.Fatalf("status = %+v, err = %v", st, err)
		}
		if rec := h.lastCall(); !strings.Contains(rec.Env["PATH"], known) {
			t.Errorf("the known directory must be on the child's PATH, got %q", rec.Env["PATH"])
		}
	})

	// gh only on the login shell's PATH (the Finder-launch case on macOS).
	t.Run("login shell path", func(t *testing.T) {
		shellDir := t.TempDir()
		data, _ := os.ReadFile(fakeBin)
		if err := os.WriteFile(filepath.Join(shellDir, ghExeName()), data, 0o755); err != nil {
			t.Fatal(err)
		}
		probes := 0
		h := newHarness(t, false, func(c *Config) {
			c.Probe = func(context.Context) (string, error) { probes++; return shellDir, nil }
		})
		h.set("FAKEGH_STDOUT", loggedInNew)
		for i := 0; i < 2; i++ {
			st, err := h.client.AuthStatus(context.Background())
			if err != nil || !st.Installed {
				t.Fatalf("status = %+v, err = %v", st, err)
			}
		}
		if probes != 1 {
			t.Errorf("the login shell was probed %d times, want once", probes)
		}
	})

	// A failing probe is not fatal: the process PATH still counts.
	t.Run("probe failure", func(t *testing.T) {
		h := newHarness(t, true, func(c *Config) {
			c.Probe = func(context.Context) (string, error) { return "", fmt.Errorf("no shell") }
		})
		h.set("FAKEGH_STDOUT", loggedInNew)
		st, err := h.client.AuthStatus(context.Background())
		if err != nil || !st.Installed {
			t.Fatalf("status = %+v, err = %v", st, err)
		}
	})
}

func TestAuthDiscoveryIgnoresRelativeAndNonExecutable(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		// A file named gh without the executable bit is not gh.
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, false)
	// A relative PATH entry (here the harness directory's name) and the empty
	// entry must not be searched.
	h.set("PATH", strings.Join([]string{"", ".", "relative/bin", dir}, string(os.PathListSeparator)))
	st, err := h.client.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed {
		t.Errorf("found %q although nothing usable is on the PATH", st.Path)
	}
}

func TestAuthParseOtherHostsAreIgnored(t *testing.T) {
	rep := parseAuthStatus(`ghe.example.com
  ✓ Logged in to ghe.example.com account corp-user (keyring)
  - Active account: true
  - Token scopes: 'repo'`)
	if rep.account != nil || rep.notLoggedIn {
		t.Errorf("report = %+v, want nothing for github.com", rep)
	}

	rep = parseAuthStatus(`ghe.example.com
  ✓ Logged in to ghe.example.com account corp-user (keyring)
  - Active account: true

github.com
  ✓ Logged in to github.com account octocat (keyring)
  - Active account: true`)
	if rep.account == nil || rep.account.login != "octocat" {
		t.Errorf("account = %+v, want octocat", rep.account)
	}
}

func TestAuthParseScopes(t *testing.T) {
	for in, want := range map[string][]string{
		"'gist', 'read:org', 'repo'": {"gist", "read:org", "repo"},
		"gist, repo":                 {"gist", "repo"},
		"none":                       {},
		"":                           {},
		"'repo'":                     {"repo"},
	} {
		if got := parseScopes(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseScopes(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestAuthParseLongLoginIsBounded(t *testing.T) {
	rep := parseAuthStatus("github.com\n  ✓ Logged in to github.com account " + strings.Repeat("a", 500) + " (keyring)\n")
	if rep.account == nil || len(rep.account.login) != maxAccountLen {
		t.Errorf("account = %+v", rep.account)
	}
}

func TestAuthChildEnvDropsInheritedPromptSettings(t *testing.T) {
	h := newHarness(t, true)
	h.set("GH_PROMPT_DISABLED", "0", "GH_PAGER", "less", "FAKEGH_STDOUT", loggedInNew, "GH_TOKEN", "from-the-environment")
	if _, err := h.client.AuthStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec := h.lastCall()
	if rec.Env["GH_PROMPT_DISABLED"] != "1" || rec.Env["GH_PAGER"] != "cat" {
		t.Errorf("env = %v", rec.Env)
	}
	// The identity of the environment is gh's own business and passes through.
	if rec.Env["GH_TOKEN"] != "from-the-environment" {
		t.Errorf("GH_TOKEN = %q, must pass through unchanged", rec.Env["GH_TOKEN"])
	}
}

// ghExeName is the file name the fake gh is installed under.
func ghExeName() string { return platform.ExecutableNames(ghName, runtime.GOOS)[0] }
