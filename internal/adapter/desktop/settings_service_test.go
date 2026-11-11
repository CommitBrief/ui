// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/adapter/clicmd"
	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// settingsTestEnv is a port.ProcessEnv with fixed variables added to the real
// environment.
type settingsTestEnv struct {
	vars map[string]string
	home string
}

func (e settingsTestEnv) Environ() []string {
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

func (e settingsTestEnv) LookupEnv(k string) (string, bool) { v, ok := e.vars[k]; return v, ok }
func (e settingsTestEnv) HomeDir() (string, error)          { return e.home, nil }

type stubGitHub struct {
	port.PRSource // only AuthStatus is used here
	st            port.AuthStatus
	err           error
}

func (s stubGitHub) AuthStatus(context.Context) (port.AuthStatus, error) { return s.st, s.err }

// The tests need a stand-in commitbrief binary, and only internal/adapter/clicmd
// and internal/adapter/github/gh may import os/exec. The test binary therefore
// plays the CLI itself: the adapter starts it (the path comes from the user's
// CLI-path setting), and TestMain answers as the CLI when fakeCLIEnv is set. The
// variable is given to the child only, never set in the test process.
const fakeCLIEnv = "DESKTOP_TEST_FAKE_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(fakeCLIEnv) == "1" {
		runFakeCLI()
		return
	}
	os.Exit(m.Run())
}

// runFakeCLI is the stand-in commitbrief: it prints a version for --version and
// otherwise records argv and stdin in the file named by FAKECLI_ARGV_FILE.
func runFakeCLI() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v := os.Getenv("FAKECLI_VERSION")
		if v == "" {
			v = "commitbrief 1.21.2 (commit abc1234, built 2026-10-31T00:00:00Z)"
		}
		fmt.Println(v)
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	if f := os.Getenv("FAKECLI_ARGV_FILE"); f != "" {
		b, _ := json.Marshal(map[string]any{"args": args, "stdin": string(stdin)})
		_ = os.WriteFile(f, b, 0o600)
	}
}

func fakeCLIPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

type settingsRig struct {
	svc     *SettingsService
	vars    map[string]string
	argvLog string
	fakeCLI string
	dataDir string
}

// newSettingsRig wires the service the way the composition root does: a real
// SQLite store, the real CLI adapter pointed at a fake binary through the
// user's CLI-path setting, and a stub for gh.
func newSettingsRig(t *testing.T) *settingsRig {
	t.Helper()
	root := t.TempDir()
	r := &settingsRig{
		vars:    map[string]string{},
		argvLog: filepath.Join(root, "argv.json"),
		fakeCLI: fakeCLIPath(t),
		dataDir: filepath.Join(root, "data"),
	}
	r.vars["FAKECLI_ARGV_FILE"] = r.argvLog
	r.vars[fakeCLIEnv] = "1"
	r.vars["PATH"] = filepath.Join(root, "empty-bin")

	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "ui.db"), sqlite.WithLogger(testLogger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var set *app.Settings
	env := settingsTestEnv{vars: r.vars, home: root}
	engine := clicmd.New(clicmd.Config{
		Env:          env,
		DataDir:      r.dataDir,
		OverridePath: func() string { return set.CLIPathOverride() },
		Probe:        func(context.Context) (string, error) { return "", nil },
		KnownDirs:    func() []string { return nil },
	})
	set = app.NewSettings(app.SettingsDeps{
		Engine: engine, Tx: store, Store: store.Settings(), Repos: store.Repos(),
		GitHub: stubGitHub{st: port.AuthStatus{Installed: true, Authenticated: true, Host: "github.com", Account: "octocat", Scopes: []string{"repo"}}},
		Env:    env,
	})
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.svc = NewSettingsService(set, testLogger)
	return r
}

func (r *settingsRig) lastCall(t *testing.T) (args []string, stdin string) {
	t.Helper()
	b, err := os.ReadFile(r.argvLog)
	if err != nil {
		t.Fatalf("the fake CLI recorded nothing: %v", err)
	}
	var rec struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec.Args, rec.Stdin
}

// asJSON is what the webview receives for an error.
func asJSON(t *testing.T, err error) string {
	t.Helper()
	b, mErr := json.Marshal(err)
	if mErr != nil {
		t.Fatal(mErr)
	}
	return string(b)
}

func TestSettingsServiceUISettingsDriveTheCLIAdapter(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)

	// Nothing on the PATH and no override: the CLI is not found, which is a
	// state of the card, not an error.
	st, err := r.svc.CLIStatus(ctx)
	if err != nil || st.State != app.CLIStateNotFound {
		t.Fatalf("CLIStatus = %+v, %v", st, err)
	}

	// Saving the CLI path in Settings is all it takes: the adapter picks it up
	// on its next call.
	ui, err := r.svc.GetUI(ctx)
	if err != nil || ui != settings.DefaultUI() {
		t.Fatalf("GetUI = %+v, %v", ui, err)
	}
	ui.CLIPath = r.fakeCLI
	ui.Workers = 2
	if saved, err := r.svc.UpdateUI(ctx, ui); err != nil || saved != ui {
		t.Fatalf("UpdateUI = %+v, %v", saved, err)
	}
	st, err = r.svc.CLIStatus(ctx)
	if err != nil || st.State != app.CLIStateOK || st.Version != "1.21.2" || st.Capabilities.ConfigSetStdin {
		t.Fatalf("CLIStatus after the override = %+v, %v", st, err)
	}

	// An invalid form is a field error and stores nothing.
	bad := ui
	bad.Workers = 9
	_, err = r.svc.UpdateUI(ctx, bad)
	if got := asJSON(t, err); got != `{"code":"settings_value_invalid","field":"workers"}` {
		t.Errorf("error JSON = %s", got)
	}
	if again, _ := r.svc.GetUI(ctx); again.Workers != 2 {
		t.Errorf("workers = %d after a rejected update", again.Workers)
	}
}

func useFakeCLI(t *testing.T, r *settingsRig) {
	t.Helper()
	u := settings.DefaultUI()
	u.CLIPath = r.fakeCLI
	if _, err := r.svc.UpdateUI(context.Background(), u); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsServiceConfigWritesGoThroughTheCLI(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)
	useFakeCLI(t, r)

	if err := r.svc.SetConfig(ctx, "output.lang", "tr", app.ConfigScope{}); err != nil {
		t.Fatal(err)
	}
	args, stdin := r.lastCall(t)
	if got := strings.Join(args, " "); !strings.HasPrefix(got, "config set ") || !strings.HasSuffix(got, "-- output.lang tr") || stdin != "" {
		t.Errorf("argv = %q, stdin = %q", got, stdin)
	}

	// A rejected key never starts a process.
	_ = os.Remove(r.argvLog)
	err := r.svc.SetConfig(ctx, "review.sandbox_command", "sh -c boom", app.ConfigScope{})
	if got := asJSON(t, err); got != `{"code":"settings_key_file_only","field":"review.sandbox_command"}` {
		t.Errorf("error JSON = %s", got)
	}
	if _, statErr := os.Stat(r.argvLog); statErr == nil {
		t.Error("the CLI was started for a key it cannot set")
	}
}

const testSecret = "sk-test-do-not-leak-0123456789"

// A secret on a CLI without config_set_stdin (1.21.2) is refused; it is never
// put on the command line instead.
func TestSettingsServiceSecretNeedsStdinCapability(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)
	useFakeCLI(t, r)

	err := r.svc.SetConfig(ctx, "providers.openai.api_key", testSecret, app.ConfigScope{})
	js := asJSON(t, err)
	if js != `{"code":"capability_missing","field":"providers.openai.api_key"}` {
		t.Errorf("error JSON = %s", js)
	}
	if strings.Contains(js, testSecret) || strings.Contains(err.Error(), testSecret) {
		t.Error("the secret is in the error")
	}
	if _, statErr := os.Stat(r.argvLog); statErr == nil {
		t.Error("the CLI was started for a secret it cannot take on stdin")
	}
}

func TestSettingsServiceSecretTravelsOnStdin(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)
	r.vars["FAKECLI_VERSION"] = "commitbrief 1.22.0 (commit abc1234, built 2026-11-20T00:00:00Z)"
	useFakeCLI(t, r)

	if err := r.svc.SetConfig(ctx, "providers.openai.api_key", testSecret, app.ConfigScope{}); err != nil {
		t.Fatal(err)
	}
	args, stdin := r.lastCall(t)
	if strings.Contains(strings.Join(args, " "), testSecret) {
		t.Errorf("the secret is in argv: %v", args)
	}
	if args[len(args)-1] != "-" || stdin != testSecret {
		t.Errorf("argv = %v, stdin = %q; want the value on stdin", args, stdin)
	}

	// Reading it back is refused before any process starts.
	_ = os.Remove(r.argvLog)
	_, err := r.svc.GetConfig(ctx, "providers.openai.api_key")
	if got := asJSON(t, err); got != `{"code":"settings_secret_not_readable","field":"providers.openai.api_key"}` {
		t.Errorf("error JSON = %s", got)
	}
	if _, statErr := os.Stat(r.argvLog); statErr == nil {
		t.Error("the CLI was started to read an API key")
	}
}

func TestSettingsServiceErrorsLeakNothing(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)

	// The CLI path points at something that is not commitbrief.
	u := settings.DefaultUI()
	u.CLIPath = filepath.Join(t.TempDir(), "missing", "commitbrief")
	if runtime.GOOS == "windows" {
		u.CLIPath += ".exe"
	}
	if _, err := r.svc.UpdateUI(ctx, u); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.ListConfig(ctx)
	js := asJSON(t, err)
	if js != `{"code":"cli_not_found"}` {
		t.Errorf("error JSON = %s", js)
	}
	if strings.Contains(js, "missing") || strings.Contains(err.Error(), string(filepath.Separator)) {
		t.Errorf("the CLI path leaked: %s / %v", js, err)
	}

	// An unclassified error becomes "internal" with no text.
	var se *settings.Error
	if got := r.svc.fail("x", errors.New("open /Users/someone/.commitbrief/config.yml: denied")); !errors.As(got, &se) || se.Code != settings.CodeInternal || strings.Contains(asJSON(t, got), "someone") {
		t.Errorf("fail(non-settings error) = %v", got)
	}
	// A classified error loses its cause.
	cause := settings.Wrap(settings.CodeValueRejected, "output.lang", errors.New("stderr with /secret/path"))
	got := r.svc.fail("x", cause)
	if !errors.As(got, &se) || se.Err != nil || asJSON(t, got) != `{"code":"settings_value_rejected","field":"output.lang"}` {
		t.Errorf("fail(classified) = %v (%s)", got, asJSON(t, got))
	}
	if r.svc.fail("x", nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestSettingsServiceGitHubStatus(t *testing.T) {
	r := newSettingsRig(t)
	st, err := r.svc.GitHubStatus(context.Background())
	if err != nil || !st.Authenticated || st.Account != "octocat" {
		t.Fatalf("= %+v, %v", st, err)
	}
	b, _ := json.Marshal(st)
	for _, want := range []string{`"installed":true`, `"authenticated":true`, `"account":"octocat"`, `"scopes":["repo"]`, `"problem":""`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON %s lacks %s", b, want)
		}
	}
}
