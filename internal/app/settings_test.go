// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// settingsRoot is an absolute directory on every OS (a drive letter is needed on
// Windows); nothing is read from it, the paths only have to look absolute.
var settingsRoot = func() string {
	if runtime.GOOS == "windows" {
		return `C:\settings-test`
	}
	return "/settings-test"
}()

var settingsRepoPath = filepath.Join(settingsRoot, "repo")

// ----- fakes -----

type setCall struct {
	key, value string
	scope      port.Scope
}

type useCall struct {
	name  string
	scope port.Scope
}

// fakeEngine is a port.ReviewEngine that answers the config, provider, version
// and upgrade calls from fixed data and records what it was asked.
type fakeEngine struct {
	mu sync.Mutex

	caps    port.Capabilities
	capsErr error

	config map[string]string
	getErr map[string]error
	gets   []string
	// inFlight/maxInFlight measure how many ConfigGet calls overlap.
	inFlight, maxInFlight int32
	getDelay              time.Duration

	sets   []setCall
	setErr error

	providers    []port.ProviderInfo
	providersErr error
	used         []useCall
	useErr       error
	tested       []string
	testErr      error

	report     port.UpgradeReport
	reportErr  error
	upgrades   int
	upgradeErr error
}

var _ port.ReviewEngine = (*fakeEngine)(nil)

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		caps: port.Capabilities{
			Version:        port.CLIVersion{Major: 1, Minor: 22, Patch: 0, Raw: "1.22.0", Commit: "abc1234"},
			Base:           true,
			ConfigSetStdin: true,
			PostingInCLI:   true,
		},
		config: map[string]string{},
		getErr: map[string]error{},
	}
}

func (f *fakeEngine) Version(context.Context) (port.CLIVersion, error) {
	return f.caps.Version, f.capsErr
}
func (f *fakeEngine) Capabilities(context.Context) (port.Capabilities, error) {
	return f.caps, f.capsErr
}
func (f *fakeEngine) ReviewLocal(context.Context, port.LocalReviewRequest) (review.Report, error) {
	return review.Report{}, errors.New("not used")
}
func (f *fakeEngine) PreviewPR(context.Context, port.PRRequest) (review.Report, error) {
	return review.Report{}, errors.New("not used")
}
func (f *fakeEngine) PostPR(context.Context, port.PRRequest) (port.PostResult, error) {
	return port.PostResult{}, errors.New("not used")
}

func (f *fakeEngine) ConfigGet(ctx context.Context, key string) (string, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	defer atomic.AddInt32(&f.inFlight, -1)
	for {
		m := atomic.LoadInt32(&f.maxInFlight)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxInFlight, m, n) {
			break
		}
	}
	if f.getDelay > 0 {
		select {
		case <-time.After(f.getDelay):
		case <-ctx.Done():
			return "", errcode.Wrap(errcode.Canceled, "canceled", ctx.Err())
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, key)
	if err := f.getErr[key]; err != nil {
		return "", err
	}
	return f.config[key], nil
}

func (f *fakeEngine) SetConfig(_ context.Context, key, value string, scope port.Scope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, setCall{key, value, scope})
	return f.setErr
}

func (f *fakeEngine) Providers(context.Context) ([]port.ProviderInfo, error) {
	return f.providers, f.providersErr
}

func (f *fakeEngine) UseProvider(_ context.Context, name string, scope port.Scope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.used = append(f.used, useCall{name, scope})
	return f.useErr
}

func (f *fakeEngine) TestProvider(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, name)
	return f.testErr
}

func (f *fakeEngine) CheckUpgrade(context.Context) (port.UpgradeReport, error) {
	return f.report, f.reportErr
}

func (f *fakeEngine) Upgrade(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgrades++
	return f.upgradeErr
}

// memSettingsDB is an in-memory settings table with transactions: a failed
// transaction leaves the table as it was.
type memSettingsDB struct {
	mu   sync.Mutex
	rows map[string]string
	// failSetAt makes the n-th Set (1-based, across the test) fail.
	failSetAt int
	sets      int
	failAll   error
}

func newMemSettingsDB() *memSettingsDB { return &memSettingsDB{rows: map[string]string{}} }

func (m *memSettingsDB) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.rows[key]
	return v, ok, nil
}

func (m *memSettingsDB) Set(_ context.Context, key, value string) error {
	m.sets++
	if m.failSetAt == m.sets {
		return errors.New("disk full")
	}
	m.rows[key] = value
	return nil
}

func (m *memSettingsDB) Delete(_ context.Context, key string) error {
	delete(m.rows, key)
	return nil
}

func (m *memSettingsDB) All(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll != nil {
		return nil, m.failAll
	}
	out := make(map[string]string, len(m.rows))
	for k, v := range m.rows {
		out[k] = v
	}
	return out, nil
}

func (m *memSettingsDB) InTx(_ context.Context, fn func(port.Tx) error) error {
	snapshot := make(map[string]string, len(m.rows))
	for k, v := range m.rows {
		snapshot[k] = v
	}
	if err := fn(settingsTx{m}); err != nil {
		m.rows = snapshot
		return err
	}
	return nil
}

type settingsTx struct{ m *memSettingsDB }

func (t settingsTx) Repos() port.RepoStore        { return nil }
func (t settingsTx) Settings() port.SettingsStore { return t.m }

type fakeGitHub struct {
	st  port.AuthStatus
	err error
}

func (f fakeGitHub) AuthStatus(context.Context) (port.AuthStatus, error) { return f.st, f.err }

type fakeEnv struct {
	vars map[string]string
	home string
}

func (e fakeEnv) Environ() []string { return nil }
func (e fakeEnv) LookupEnv(k string) (string, bool) {
	v, ok := e.vars[k]
	return v, ok
}
func (e fakeEnv) HomeDir() (string, error) { return e.home, nil }

type settingsRig struct {
	svc    *Settings
	engine *fakeEngine
	db     *memSettingsDB
	repos  *memStore
	gh     *fakeGitHub
}

func newSettingsRig(t *testing.T) *settingsRig {
	t.Helper()
	r := &settingsRig{engine: newFakeEngine(), db: newMemSettingsDB(), repos: newMemStore(), gh: &fakeGitHub{}}
	r.svc = NewSettings(SettingsDeps{
		Engine: r.engine,
		Tx:     r.db,
		Store:  r.db,
		Repos:  r.repos,
		GitHub: r.gh,
		Env:    fakeEnv{vars: map[string]string{}, home: filepath.Join(settingsRoot, "home")},
	})
	return r
}

// wantSettingsCode fails the test unless err is a *settings.Error with the
// given code and field (field "-" skips the field check).
func wantSettingsCode(t *testing.T, err error, code settings.Code, field string) *settings.Error {
	t.Helper()
	var se *settings.Error
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *settings.Error %s", err, err, code)
	}
	if se.Code != code || (field != "-" && se.Field != field) {
		t.Fatalf("err = %q, want code %s field %q", se, code, field)
	}
	return se
}

// ----- UI settings -----

func TestSettingsUIDefaultsAndRoundTrip(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()

	got, err := r.svc.UI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != settings.DefaultUI() {
		t.Errorf("fresh settings = %+v, want the defaults", got)
	}
	if r.svc.CLIPathOverride() != "" {
		t.Error("no override on a fresh installation")
	}

	cli := filepath.Join(settingsRoot, "opt", "commitbrief")
	in := settings.UI{Language: settings.LanguageTR, CLIPath: cli, Workers: 3, DailyBudgetUSD: 4.25, LogRetentionDays: 7}
	saved, err := r.svc.UpdateUI(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if saved != in {
		t.Errorf("saved = %+v, want %+v", saved, in)
	}
	if r.svc.CLIPathOverride() != cli {
		t.Errorf("CLIPathOverride() = %q after the update", r.svc.CLIPathOverride())
	}

	// A new process reads the same values, and Load primes the override.
	again := NewSettings(SettingsDeps{Store: r.db, Tx: r.db})
	if again.CLIPathOverride() != "" {
		t.Error("the override is only known after Load")
	}
	if err := again.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if again.CLIPathOverride() != cli {
		t.Errorf("after Load: CLIPathOverride() = %q", again.CLIPathOverride())
	}
	if got, _ := again.UI(ctx); got != in {
		t.Errorf("reloaded = %+v, want %+v", got, in)
	}

	// An empty CLI path removes the row and the override.
	in.CLIPath = ""
	if _, err := r.svc.UpdateUI(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.db.rows[settings.KeyCLIPath]; ok {
		t.Error("the cli.path row must be deleted, not stored empty")
	}
	if r.svc.CLIPathOverride() != "" {
		t.Error("the override must be cleared")
	}
}

func TestSettingsUIUpdateRejectsInvalidAndStoresNothing(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()
	good := settings.DefaultUI()
	good.CLIPath = filepath.Join(settingsRoot, "bin", "commitbrief")
	if _, err := r.svc.UpdateUI(ctx, good); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for k, v := range r.db.rows {
		before[k] = v
	}

	for name, tc := range map[string]struct {
		mut   func(*settings.UI)
		field string
	}{
		"workers":   {func(u *settings.UI) { u.Workers = 0 }, "workers"},
		"language":  {func(u *settings.UI) { u.Language = "xx" }, "language"},
		"budget":    {func(u *settings.UI) { u.DailyBudgetUSD = -1 }, "dailyBudgetUsd"},
		"retention": {func(u *settings.UI) { u.LogRetentionDays = 1000 }, "logRetentionDays"},
		"cli path":  {func(u *settings.UI) { u.CLIPath = "relative/commitbrief" }, "cliPath"},
	} {
		in := good
		tc.mut(&in)
		_, err := r.svc.UpdateUI(ctx, in)
		wantSettingsCode(t, err, settings.CodeValueInvalid, tc.field)
		if !reflect.DeepEqual(r.db.rows, before) {
			t.Errorf("%s: an invalid update changed the table: %v", name, r.db.rows)
		}
		if r.svc.CLIPathOverride() != good.CLIPath {
			t.Errorf("%s: an invalid update changed the override", name)
		}
	}
}

func TestSettingsUIUpdateIsAtomic(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()
	old := settings.DefaultUI()
	old.Workers = 2
	if _, err := r.svc.UpdateUI(ctx, old); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for k, v := range r.db.rows {
		before[k] = v
	}

	// Fail the third write of the next update: the first two must roll back.
	r.db.failSetAt = r.db.sets + 3
	next := old
	next.Workers = 4
	next.LogRetentionDays = 3
	next.CLIPath = filepath.Join(settingsRoot, "x", "commitbrief")
	_, err := r.svc.UpdateUI(ctx, next)
	wantSettingsCode(t, err, settings.CodeInternal, "")
	if !reflect.DeepEqual(r.db.rows, before) {
		t.Errorf("a failed update left a half-written table: %v", r.db.rows)
	}
	if r.svc.CLIPathOverride() != "" {
		t.Error("the cached override must not move when the write failed")
	}
}

func TestSettingsUIReadErrorIsInternal(t *testing.T) {
	r := newSettingsRig(t)
	r.db.failAll = errors.New("database is locked")
	_, err := r.svc.UI(context.Background())
	se := wantSettingsCode(t, err, settings.CodeInternal, "")
	if se.Unwrap() == nil || strings.Contains(se.Error(), "locked") {
		t.Errorf("the cause must be kept for the log and kept out of the text: %q / %v", se.Error(), se.Unwrap())
	}
	if err := r.svc.Load(context.Background()); err == nil {
		t.Error("Load must return the error for the caller to log")
	}
}

func TestSettingsUIDamagedRowsReadAsDefaults(t *testing.T) {
	r := newSettingsRig(t)
	r.db.rows[settings.KeyWorkers] = "99"
	r.db.rows[settings.KeyLanguage] = "klingon"
	r.db.rows[settings.KeyCLIPath] = "not/absolute"
	got, err := r.svc.UI(context.Background())
	if err != nil || got != settings.DefaultUI() {
		t.Errorf("got %+v, %v; want the defaults", got, err)
	}
	if r.svc.CLIPathOverride() != "" {
		t.Error("a damaged cli.path row must not become an override")
	}
}

// ----- CLI configuration -----

func TestSettingsListConfigReadsEveryCatalogKeyOnce(t *testing.T) {
	r := newSettingsRig(t)
	r.engine.getDelay = 20 * time.Millisecond
	r.engine.config["output.lang"] = "tr"
	r.engine.config["cache.ttl_days"] = "7"
	r.engine.config["review.sandbox_command"] = "go test -run {{.Test}}"

	entries, err := r.svc.ListConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var want []string
	for _, f := range settings.GlobalKeys() {
		want = append(want, f.Key)
	}
	var got, gets []string
	for _, e := range entries {
		got = append(got, e.Field.Key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries are not in catalogue order:\n got %v\nwant %v", got, want)
	}
	gets = append(gets, r.engine.gets...)
	sort.Strings(gets)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(gets, sorted) {
		t.Errorf("config get calls = %v, want each global key exactly once", gets)
	}
	for _, k := range r.engine.gets {
		if strings.Contains(k, "api_key") || strings.HasPrefix(k, "providers.") {
			t.Errorf("ListConfig must not read %q", k)
		}
	}
	if m := atomic.LoadInt32(&r.engine.maxInFlight); m < 2 || m > configReadParallelism {
		t.Errorf("max concurrent config get = %d, want 2..%d", m, configReadParallelism)
	}

	byKey := map[string]ConfigEntry{}
	for _, e := range entries {
		byKey[e.Field.Key] = e
	}
	if e := byKey["output.lang"]; e.Value != "tr" || e.Error != "" {
		t.Errorf("output.lang = %+v", e)
	}
	if e := byKey["review.sandbox_command"]; !e.Field.FileOnly || e.Value != "go test -run {{.Test}}" {
		t.Errorf("list key = %+v, want a read-only value", e)
	}
	if e := byKey["guard.secret_patterns"]; !e.Field.FileOnly {
		t.Errorf("guard.secret_patterns = %+v, want file only", e)
	}
}

func TestSettingsListConfigKeyErrorsStayOnTheEntry(t *testing.T) {
	r := newSettingsRig(t)
	r.engine.getErr["cache.ttl_days"] = errcode.New(errcode.Unknown, "boom").WithDetail("stderr text")
	entries, err := r.svc.ListConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Field.Key {
		case "cache.ttl_days":
			if e.Error != "unknown" || e.Value != "" {
				t.Errorf("entry = %+v", e)
			}
		default:
			if e.Error != "" {
				t.Errorf("%s: unexpected error %q", e.Field.Key, e.Error)
			}
		}
	}
}

func TestSettingsListConfigFailsWholeWhenTheCLIIsUnusable(t *testing.T) {
	for _, code := range []errcode.Code{errcode.CLINotFound, errcode.CLITooOld, errcode.Timeout} {
		r := newSettingsRig(t)
		r.engine.getErr["output.lang"] = errcode.New(code, "x")
		_, err := r.svc.ListConfig(context.Background())
		wantSettingsCode(t, err, settings.Code(code), "-")
	}

	r := newSettingsRig(t)
	r.engine.getDelay = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := r.svc.ListConfig(ctx)
	wantSettingsCode(t, err, settings.CodeCanceled, "")
}

func TestSettingsGetConfig(t *testing.T) {
	r := newSettingsRig(t)
	r.engine.config["output.color"] = "never"
	r.engine.config["providers.openai.model"] = "gpt-5.4-mini"
	ctx := context.Background()

	e, err := r.svc.GetConfig(ctx, "output.color")
	if err != nil || e.Value != "never" || e.Field.Kind != settings.KindEnum {
		t.Errorf("= %+v, %v", e, err)
	}
	if e, err := r.svc.GetConfig(ctx, "providers.openai.model"); err != nil || e.Value != "gpt-5.4-mini" {
		t.Errorf("= %+v, %v", e, err)
	}

	_, err = r.svc.GetConfig(ctx, "providers.openai.api_key")
	wantSettingsCode(t, err, settings.CodeSecretNotReadable, "providers.openai.api_key")
	_, err = r.svc.GetConfig(ctx, "version")
	wantSettingsCode(t, err, settings.CodeKeyUnknown, "version")
	for _, k := range r.engine.gets {
		if strings.Contains(k, "api_key") || k == "version" {
			t.Errorf("the engine was asked for %q", k)
		}
	}
}

func TestSettingsSetConfigValidatesBeforeTheCLI(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()

	for _, tc := range []struct {
		key, value string
		code       settings.Code
	}{
		{"version", "2", settings.CodeKeyUnknown},
		{"commit.type", "gitmoji", settings.CodeKeyUnknown},
		{"providers.openai.api_key_env", "X", settings.CodeKeyUnknown},
		{"review.sandbox_command", "sh -c boom", settings.CodeKeyFileOnly},
		{"guard.secret_patterns", "x", settings.CodeKeyFileOnly},
		{"cache.ttl_days", "-3", settings.CodeValueInvalid},
		{"cache.ttl_days", "many", settings.CodeValueInvalid},
		{"output.color", "blue", settings.CodeValueInvalid},
		{"output.stream", "perhaps", settings.CodeValueInvalid},
		{"review.timeout", "later", settings.CodeValueInvalid},
		{"cost.warn_threshold_usd", "-1", settings.CodeValueInvalid},
	} {
		err := r.svc.SetConfig(ctx, tc.key, tc.value, ConfigScope{})
		wantSettingsCode(t, err, tc.code, tc.key)
	}
	if len(r.engine.sets) != 0 {
		t.Errorf("the CLI was called for refused writes: %v", r.engine.sets)
	}

	// Accepted values reach the CLI in their canonical spelling.
	for _, tc := range []struct{ key, in, out string }{
		{"output.stream", "OFF", "false"},
		{"cache.ttl_days", " 14 ", "14"},
		{"cost.warn_threshold_usd", "0.50", "0.5"},
		{"output.lang", "tr", "tr"},
		{"providers.anthropic.model", "claude-opus-5-5", "claude-opus-5-5"},
	} {
		if err := r.svc.SetConfig(ctx, tc.key, tc.in, ConfigScope{}); err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		last := r.engine.sets[len(r.engine.sets)-1]
		if last.key != tc.key || last.value != tc.out || last.scope != port.UserScope() {
			t.Errorf("%s: CLI got %+v", tc.key, last)
		}
	}
}

func TestSettingsSetConfigRepoScope(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()
	with, _ := r.repos.Create(ctx, repo.Repository{Owner: "CommitBrief", Name: "ui", LocalPath: settingsRepoPath, Enabled: true})
	without, _ := r.repos.Create(ctx, repo.Repository{Owner: "CommitBrief", Name: "cli", Enabled: true})

	if err := r.svc.SetConfig(ctx, "output.lang", "de", ConfigScope{RepoID: with.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.engine.sets[0].scope; got != port.RepoScope(settingsRepoPath) {
		t.Errorf("scope = %+v, want the repository's", got)
	}

	err := r.svc.SetConfig(ctx, "output.lang", "de", ConfigScope{RepoID: without.ID})
	wantSettingsCode(t, err, settings.CodeScopeInvalid, "repoId")
	err = r.svc.SetConfig(ctx, "output.lang", "de", ConfigScope{RepoID: 9999})
	wantSettingsCode(t, err, settings.CodeScopeInvalid, "repoId")
	if len(r.engine.sets) != 1 {
		t.Errorf("a bad scope must not reach the CLI: %v", r.engine.sets)
	}

	// Switching the provider honours the scope too.
	if err := r.svc.UseProvider(ctx, "openai", ConfigScope{RepoID: with.ID}); err != nil {
		t.Fatal(err)
	}
	if r.engine.used[0] != (useCall{"openai", port.RepoScope(settingsRepoPath)}) {
		t.Errorf("used = %+v", r.engine.used)
	}
}

func TestSettingsSetConfigSecrets(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()
	repoRow, _ := r.repos.Create(ctx, repo.Repository{Owner: "o", Name: "n", LocalPath: settingsRepoPath, Enabled: true})
	const secret = "sk-test-0123456789"

	// The user-level file only: a repository's .commitbrief/config.yml is
	// meant to stay out of version control but nothing guarantees it.
	err := r.svc.SetConfig(ctx, "providers.openai.api_key", secret, ConfigScope{RepoID: repoRow.ID})
	wantSettingsCode(t, err, settings.CodeSecretUserScopeOnly, "providers.openai.api_key")
	if len(r.engine.sets) != 0 {
		t.Fatal("the secret reached the CLI for a repository scope")
	}

	if err := r.svc.SetConfig(ctx, "providers.openai.api_key", secret, ConfigScope{}); err != nil {
		t.Fatal(err)
	}
	// The adapter decides how it travels (stdin); the service hands it over as is.
	if got := r.engine.sets[0]; got.key != "providers.openai.api_key" || got.value != secret || got.scope != port.UserScope() {
		t.Errorf("CLI got %+v", got)
	}

	// An old CLI answers capability_missing; the code passes through, with the
	// key as the field and nothing of the value in the text.
	r.engine.setErr = errcode.New(errcode.CapabilityMissing, "needs 1.22.0").WithDetail("value was " + secret)
	err = r.svc.SetConfig(ctx, "providers.openai.api_key", secret, ConfigScope{})
	se := wantSettingsCode(t, err, settings.Code(errcode.CapabilityMissing), "providers.openai.api_key")
	if strings.Contains(se.Error(), secret) {
		t.Errorf("error text %q carries the secret", se.Error())
	}

	// Malformed secrets never reach the CLI.
	before := len(r.engine.sets)
	for _, bad := range []string{"", "two\nlines", "cr\r"} {
		err := r.svc.SetConfig(ctx, "providers.openai.api_key", bad, ConfigScope{})
		wantSettingsCode(t, err, settings.CodeValueInvalid, "providers.openai.api_key")
	}
	if len(r.engine.sets) != before {
		t.Error("a malformed secret reached the CLI")
	}
}

func TestSettingsSetConfigCLIAnswers(t *testing.T) {
	ctx := context.Background()

	r := newSettingsRig(t)
	r.engine.setErr = errcode.New(errcode.Unknown, "the CLI failed with an unrecognized message").WithDetail(`config: output.lang "xx" is not a recognized language`)
	err := r.svc.SetConfig(ctx, "output.lang", "xx", ConfigScope{})
	se := wantSettingsCode(t, err, settings.CodeValueRejected, "output.lang")
	if strings.Contains(se.Error(), "recognized") {
		t.Errorf("the CLI's raw text must not cross the boundary: %q", se.Error())
	}

	for _, code := range []errcode.Code{errcode.CLINotFound, errcode.CLITooOld, errcode.Timeout, errcode.NotARepo} {
		r := newSettingsRig(t)
		r.engine.setErr = errcode.New(code, "x")
		err := r.svc.SetConfig(ctx, "output.lang", "tr", ConfigScope{})
		wantSettingsCode(t, err, settings.Code(code), "output.lang")
	}
}

func TestSettingsFiles(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)
	home := filepath.Join(settingsRoot, "home")
	repoRow, _ := r.repos.Create(ctx, repo.Repository{Owner: "o", Name: "n", LocalPath: settingsRepoPath, Enabled: true})

	got, err := r.svc.Files(ctx, ConfigScope{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ConfigFiles{UserPath: filepath.Join(home, ".commitbrief", "config.yml")}); got != want {
		t.Errorf("= %+v, want %+v", got, want)
	}

	got, err = r.svc.Files(ctx, ConfigScope{RepoID: repoRow.ID})
	if err != nil || got.RepoPath != filepath.Join(settingsRepoPath, ".commitbrief", "config.yml") {
		t.Errorf("= %+v, %v", got, err)
	}

	envPath := filepath.Join(settingsRoot, "elsewhere", "cb.yml")
	r.svc.d.Env = fakeEnv{vars: map[string]string{"COMMITBRIEF_CONFIG": envPath}, home: home}
	got, _ = r.svc.Files(ctx, ConfigScope{})
	if got.UserPath != envPath || !got.UserPathFromEnv {
		t.Errorf("= %+v, want the COMMITBRIEF_CONFIG path", got)
	}

	r.svc.d.Env = nil
	if got, _ := r.svc.Files(ctx, ConfigScope{}); got.UserPath != "" {
		t.Errorf("without an environment the path is unknown, got %q", got.UserPath)
	}

	_, err = r.svc.Files(ctx, ConfigScope{RepoID: 4242})
	wantSettingsCode(t, err, settings.CodeScopeInvalid, "repoId")
}

// ----- providers -----

func TestSettingsProvidersNeverCarryTheKey(t *testing.T) {
	r := newSettingsRig(t)
	r.engine.providers = []port.ProviderInfo{
		{Name: "anthropic", Active: true, Model: "claude-opus-5-5", KeyStatus: "sk-ant-…WXYZ", KeyInConfig: true},
		{Name: "ollama", Model: "qwen", KeyStatus: "http://localhost:11434", KeyInConfig: true},
		{Name: "gemini", KeyStatus: "(not set)"},
	}
	got, err := r.svc.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ProviderView{
		{Name: "anthropic", Active: true, Model: "claude-opus-5-5", KeyInConfig: true},
		{Name: "ollama", Model: "qwen", KeyInConfig: true},
		{Name: "gemini"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("= %+v\nwant %+v", got, want)
	}

	r.engine.providersErr = errcode.New(errcode.CLINotFound, "x")
	_, err = r.svc.Providers(context.Background())
	wantSettingsCode(t, err, settings.Code(errcode.CLINotFound), "-")
}

func TestSettingsProviderConfigAndActions(t *testing.T) {
	r := newSettingsRig(t)
	ctx := context.Background()
	r.engine.config["providers.ollama.model"] = "qwen2.5-coder:14b"
	r.engine.config["providers.ollama.base_url"] = "http://localhost:11434"

	pc, err := r.svc.ProviderConfig(ctx, "ollama")
	if err != nil || pc != (ProviderConfig{Name: "ollama", Model: "qwen2.5-coder:14b", BaseURL: "http://localhost:11434"}) {
		t.Errorf("= %+v, %v", pc, err)
	}

	if err := r.svc.UseProvider(ctx, "openai", ConfigScope{}); err != nil {
		t.Fatal(err)
	}
	if r.engine.used[0] != (useCall{"openai", port.UserScope()}) {
		t.Errorf("used = %+v", r.engine.used)
	}
	if err := r.svc.TestProvider(ctx, "openai"); err != nil {
		t.Fatal(err)
	}
	r.engine.testErr = errcode.New(errcode.ProviderError, "401").WithDetail("invalid api key sk-xyz")
	err = r.svc.TestProvider(ctx, "openai")
	se := wantSettingsCode(t, err, settings.Code(errcode.ProviderError), "provider")
	if strings.Contains(se.Error(), "sk-xyz") {
		t.Errorf("provider text leaked: %q", se.Error())
	}

	for _, bad := range []string{"", "a.b", "-x", "a b", "../x"} {
		_, err := r.svc.ProviderConfig(ctx, bad)
		wantSettingsCode(t, err, settings.CodeProviderInvalid, "provider")
		wantSettingsCode(t, r.svc.UseProvider(ctx, bad, ConfigScope{}), settings.CodeProviderInvalid, "provider")
		wantSettingsCode(t, r.svc.TestProvider(ctx, bad), settings.CodeProviderInvalid, "provider")
	}
	if len(r.engine.used) != 1 || len(r.engine.tested) != 2 {
		t.Errorf("invalid names reached the CLI: used=%v tested=%v", r.engine.used, r.engine.tested)
	}
}

// ----- CLI card -----

func TestSettingsCLIStatus(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)

	st, err := r.svc.CLI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := CLIStatus{
		State: CLIStateOK, Version: "1.22.0", Commit: "abc1234",
		Capabilities: CLICapabilities{Base: true, ConfigSetStdin: true, PostingInCLI: true},
	}
	if st != want {
		t.Errorf("= %+v\nwant %+v", st, want)
	}

	// Too old: found, but below the base version.
	r.engine.caps = port.Capabilities{Version: port.CLIVersion{Major: 1, Minor: 20, Patch: 0, Raw: "1.20.0"}}
	if st, _ := r.svc.CLI(ctx); st.State != CLIStateTooOld || st.Version != "1.20.0" || st.Capabilities.Base {
		t.Errorf("= %+v", st)
	}

	// A source build.
	r.engine.caps = port.Capabilities{Version: port.CLIVersion{Dev: true, Raw: "dev"}, Base: true}
	if st, _ := r.svc.CLI(ctx); st.State != CLIStateOK || !st.Dev || st.Capabilities.ConfigSetStdin {
		t.Errorf("= %+v", st)
	}

	// Not found is a state, not an error.
	r.engine.capsErr = errcode.New(errcode.CLINotFound, "no commitbrief binary found")
	st, err = r.svc.CLI(ctx)
	if err != nil || st.State != CLIStateNotFound || st.Version != "" {
		t.Errorf("= %+v, %v", st, err)
	}

	// Anything else is an error.
	r.engine.capsErr = errcode.New(errcode.Timeout, "slow")
	_, err = r.svc.CLI(ctx)
	wantSettingsCode(t, err, settings.Code(errcode.Timeout), "-")
}

func TestSettingsCheckCLIUpgrade(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)

	for method, delegated := range map[string]bool{"homebrew": true, "scoop": true, "go-install": true, "manual": false, "": false} {
		r.engine.report = port.UpgradeReport{Current: "1.21.2", Latest: "1.22.0", Method: method, UpdateAvailable: true, Action: "brew upgrade commitbrief"}
		got, err := r.svc.CheckCLIUpgrade(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.Delegated != delegated || got.Method != method || got.Current != "1.21.2" || got.Latest != "1.22.0" || !got.UpdateAvailable || got.Action != "brew upgrade commitbrief" {
			t.Errorf("method %q: %+v", method, got)
		}
	}
	if r.engine.upgrades != 0 {
		t.Error("a check must install nothing")
	}

	r.engine.reportErr = errcode.New(errcode.Unknown, "could not reach the GitHub release API")
	_, err := r.svc.CheckCLIUpgrade(ctx)
	wantSettingsCode(t, err, settings.Code(errcode.Unknown), "-")
}

func TestSettingsUpgradeCLI(t *testing.T) {
	ctx := context.Background()

	t.Run("manual install is upgraded", func(t *testing.T) {
		r := newSettingsRig(t)
		r.engine.report = port.UpgradeReport{Current: "1.21.2", Latest: "1.22.0", Method: "manual", UpdateAvailable: true, Action: "commitbrief_linux_amd64.tar.gz"}
		st, err := r.svc.UpgradeCLI(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if r.engine.upgrades != 1 || st.State != CLIStateOK {
			t.Errorf("upgrades = %d, status = %+v", r.engine.upgrades, st)
		}
	})

	t.Run("a package manager owns the binary", func(t *testing.T) {
		r := newSettingsRig(t)
		r.engine.report = port.UpgradeReport{Method: "homebrew", UpdateAvailable: true, Action: "brew upgrade commitbrief"}
		_, err := r.svc.UpgradeCLI(ctx)
		wantSettingsCode(t, err, settings.CodeUpgradeDelegated, "")
		if r.engine.upgrades != 0 {
			t.Error("the UI must not run a delegated upgrade")
		}
	})

	t.Run("nothing newer", func(t *testing.T) {
		r := newSettingsRig(t)
		r.engine.report = port.UpgradeReport{Current: "1.22.0", Latest: "1.22.0", Method: "manual"}
		st, err := r.svc.UpgradeCLI(ctx)
		if err != nil || r.engine.upgrades != 0 || st.State != CLIStateOK {
			t.Errorf("upgrades = %d, status = %+v, err = %v", r.engine.upgrades, st, err)
		}
	})

	t.Run("failures", func(t *testing.T) {
		r := newSettingsRig(t)
		r.engine.report = port.UpgradeReport{Method: "manual", UpdateAvailable: true}
		r.engine.upgradeErr = errcode.New(errcode.Unknown, "install target is not writable")
		_, err := r.svc.UpgradeCLI(ctx)
		wantSettingsCode(t, err, settings.Code(errcode.Unknown), "-")

		r.engine.upgradeErr = nil
		r.engine.reportErr = errcode.New(errcode.Timeout, "x")
		_, err = r.svc.UpgradeCLI(ctx)
		wantSettingsCode(t, err, settings.Code(errcode.Timeout), "-")
	})
}

// ----- gh -----

func TestSettingsGitHub(t *testing.T) {
	ctx := context.Background()
	r := newSettingsRig(t)

	r.gh.st = port.AuthStatus{Installed: true, Path: "/usr/bin/gh", Authenticated: true, Host: "github.com", Account: "octocat", Scopes: nil}
	got, err := r.svc.GitHub(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Authenticated || got.Account != "octocat" || got.Scopes == nil {
		t.Errorf("= %+v (Scopes must be a non-nil slice for the frontend)", got)
	}

	r.gh.st = port.AuthStatus{Host: "github.com"}
	if got, err := r.svc.GitHub(ctx); err != nil || got.Installed {
		t.Errorf("= %+v, %v", got, err)
	}

	r.gh.err = errcode.New(errcode.Timeout, "slow")
	_, err = r.svc.GitHub(ctx)
	wantSettingsCode(t, err, settings.Code(errcode.Timeout), "-")
}

// ----- error shaping -----

func TestSettingsFailShapesEveryKindOfError(t *testing.T) {
	r := newSettingsRig(t)
	if r.svc.fail("k", nil) != nil {
		t.Error("nil must stay nil")
	}
	wantSettingsCode(t, r.svc.fail("", context.Canceled), settings.CodeCanceled, "")
	wantSettingsCode(t, r.svc.fail("", context.DeadlineExceeded), settings.CodeCanceled, "")
	wantSettingsCode(t, r.svc.fail("", errors.New("surprise")), settings.CodeInternal, "")
	wantSettingsCode(t, r.svc.fail("", settings.New(settings.CodeKeyUnknown, "x")), settings.CodeKeyUnknown, "x")
	wrapped := errors.Join(errors.New("ctx"), errcode.New(errcode.GHNotFound, "x"))
	wantSettingsCode(t, r.svc.fail("f", wrapped), settings.Code(errcode.GHNotFound), "f")
}
