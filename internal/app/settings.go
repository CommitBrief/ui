// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// configReadParallelism is how many `config get` processes run at once when the
// Settings screen reads every key (UI-0004: without a machine-readable dump the
// UI reads key by key).
const configReadParallelism = 4

// Upgrade methods the CLI reports (`commitbrief upgrade --json`, field
// `method`). Only a manual install is replaced in place by the CLI itself; the
// others belong to their package manager, whose command is shown to the user.
const upgradeMethodManual = "manual"

// SettingsDeps are the ports the Settings use cases need.
type SettingsDeps struct {
	// Engine is the CLI adapter (config, providers, upgrade).
	Engine port.ReviewEngine
	// Tx runs UI-setting writes in one transaction; Store reads them.
	Tx    port.TxRunner
	Store port.SettingsStore
	// Repos resolves the repository of a local config scope.
	Repos port.RepoStore
	// GitHub reports the `gh` login (UI-0005).
	GitHub port.PRSource
	// Env is the process environment, for the config file path
	// (COMMITBRIEF_CONFIG, the home directory). nil means no environment: the
	// user-level path is then unknown and reported empty.
	Env port.ProcessEnv
}

// Settings is the use-case service behind the Settings screen: the CLI's
// configuration and providers (always through `commitbrief config` and
// `commitbrief providers`, never by touching the CLI's files), the CLI's
// version card, the `gh` status, and the UI's own settings.
//
// Every error it returns is a *settings.Error.
type Settings struct {
	d SettingsDeps

	// mu serializes UI-setting reads and writes so the cached CLI path override
	// always matches what was last committed.
	mu      sync.Mutex
	cliPath atomic.Pointer[string]
}

// NewSettings wires the service. It does no I/O; call Load once after the
// store is open.
func NewSettings(d SettingsDeps) *Settings { return &Settings{d: d} }

// Load reads the UI settings once so CLIPathOverride is right from the first
// CLI call on. A failure leaves the override empty (the CLI is then discovered
// automatically) and is returned for the caller to log.
func (s *Settings) Load(ctx context.Context) error {
	_, err := s.UI(ctx)
	return err
}

// CLIPathOverride is the CLI path the user set in Settings, empty for none. It
// is what clicmd.Config.OverridePath calls before every CLI call, so it reads
// memory, never the database.
func (s *Settings) CLIPathOverride() string {
	if p := s.cliPath.Load(); p != nil {
		return *p
	}
	return ""
}

// ----- UI settings -----

// UI returns the UI's own settings; a row that is missing or damaged reads as
// its default.
func (s *Settings) UI(ctx context.Context) (settings.UI, error) {
	// Under the same lock as UpdateUI: a read that started before a write must
	// not store its older CLI path over the newer one.
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.d.Store.All(ctx)
	if err != nil {
		return settings.UI{}, s.fail("", err)
	}
	u := settings.FromStored(rows)
	path := u.CLIPath
	s.cliPath.Store(&path)
	return u, nil
}

// UpdateUI validates and stores all UI settings together, in one transaction,
// and returns what is stored. The rows of a field that did not change are
// rewritten as they are, which is harmless: the form saves the whole screen.
func (s *Settings) UpdateUI(ctx context.Context, in settings.UI) (settings.UI, error) {
	if err := in.Validate(); err != nil {
		return settings.UI{}, err
	}
	set, unset := in.ToStored()

	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.d.Tx.InTx(ctx, func(tx port.Tx) error {
		for k, v := range set {
			if err := tx.Settings().Set(ctx, k, v); err != nil {
				return err
			}
		}
		for _, k := range unset {
			if err := tx.Settings().Delete(ctx, k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return settings.UI{}, s.fail("", err)
	}
	path := in.CLIPath
	s.cliPath.Store(&path)
	return in, nil
}

// ----- CLI configuration -----

// ConfigScope selects which CLI config file a write goes to: the user-level
// file (RepoID 0) or the .commitbrief/config.yml of a registered repository.
type ConfigScope struct {
	RepoID int64 `json:"repoId"`
}

// ConfigEntry is one CLI configuration key with the value the CLI reports.
type ConfigEntry struct {
	Field settings.ConfigField `json:"field"`
	// Value is what `config get` printed: the merged value (repository file,
	// user file, environment and defaults), empty when unset. A secret key is
	// never read, so its Value is always empty.
	Value string `json:"value"`
	// Error is the failure code when this one key could not be read, else
	// empty.
	Error string `json:"error"`
}

// ConfigFiles tells where the CLI's config files are, for "edit in the file"
// keys and for the help text.
type ConfigFiles struct {
	// UserPath is the user-level file; empty when the home directory is
	// unknown.
	UserPath string `json:"userPath"`
	// UserPathFromEnv is true when COMMITBRIEF_CONFIG decides UserPath.
	UserPathFromEnv bool `json:"userPathFromEnv"`
	// RepoPath is the repository file of the requested scope; empty for the
	// user scope.
	RepoPath string `json:"repoPath"`
}

// ListConfig reads every key of the catalogue the CLI can set, plus the two
// list keys it can only show (UI-0004: a `config get` per key). The values are
// the merged, effective ones: the CLI has no way to read a single file's value.
//
// A key that fails to read carries its code in ConfigEntry.Error; a CLI that
// cannot be found or is too old fails the whole call.
func (s *Settings) ListConfig(ctx context.Context) ([]ConfigEntry, error) {
	fields := settings.GlobalKeys()
	entries := make([]ConfigEntry, len(fields))
	for i, f := range fields {
		entries[i] = ConfigEntry{Field: f}
	}

	sem := make(chan struct{}, configReadParallelism)
	errs := make([]error, len(fields))
	var wg sync.WaitGroup
	for i := range fields {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			entries[i].Value, errs[i] = s.d.Engine.ConfigGet(ctx, fields[i].Key)
		}(i)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, s.fail("", err)
	}
	for i, err := range errs {
		if err == nil {
			continue
		}
		switch errcode.CodeOf(err) {
		case errcode.CLINotFound, errcode.CLITooOld, errcode.Canceled, errcode.Timeout:
			// Not about this key: the whole call cannot work.
			return nil, s.fail("", err)
		}
		entries[i].Value = ""
		entries[i].Error = string(s.fail(fields[i].Key, err).(*settings.Error).Code)
	}
	return entries, nil
}

// GetConfig reads one key. A secret key is refused: API keys are written, never
// read back.
func (s *Settings) GetConfig(ctx context.Context, key string) (ConfigEntry, error) {
	f, ok := settings.LookupConfigKey(key)
	if !ok {
		return ConfigEntry{}, settings.New(settings.CodeKeyUnknown, key)
	}
	if f.Secret {
		return ConfigEntry{}, settings.New(settings.CodeSecretNotReadable, key)
	}
	v, err := s.d.Engine.ConfigGet(ctx, key)
	if err != nil {
		return ConfigEntry{}, s.fail(key, err)
	}
	return ConfigEntry{Field: f, Value: v}, nil
}

// SetConfig writes one key with `commitbrief config set`, to the user file or,
// for a repository scope, to that repository's .commitbrief/config.yml.
//
// The value is checked against the key's type first, so a typo is a field error
// instead of a CLI round trip. A key that holds a list is refused (hand-edit the
// file). A secret (providers.<name>.api_key) goes to the user-level file only
// and is handed to the CLI on stdin by the adapter, never on the command line;
// it is not returned, echoed or logged here.
func (s *Settings) SetConfig(ctx context.Context, key, value string, scope ConfigScope) error {
	f, ok := settings.LookupConfigKey(key)
	if !ok {
		return settings.New(settings.CodeKeyUnknown, key)
	}
	if f.FileOnly {
		return settings.New(settings.CodeKeyFileOnly, key)
	}
	if f.Secret && scope.RepoID != 0 {
		return settings.New(settings.CodeSecretUserScopeOnly, key)
	}
	clean, err := f.ValidateValue(key, value)
	if err != nil {
		return err
	}
	sc, err := s.resolveScope(ctx, scope)
	if err != nil {
		return err
	}
	if err := s.d.Engine.SetConfig(ctx, key, clean, sc); err != nil {
		if errcode.CodeOf(err) == errcode.Unknown {
			// The CLI ran and said no: its own validation (an unrecognized
			// output language, say) or a file it could not write.
			return settings.Wrap(settings.CodeValueRejected, key, err)
		}
		return s.fail(key, err)
	}
	return nil
}

// resolveScope turns the screen's scope into the port's.
func (s *Settings) resolveScope(ctx context.Context, scope ConfigScope) (port.Scope, error) {
	if scope.RepoID == 0 {
		return port.UserScope(), nil
	}
	r, err := s.d.Repos.Get(ctx, scope.RepoID)
	if errors.Is(err, port.ErrNotFound) {
		return port.Scope{}, settings.Wrap(settings.CodeScopeInvalid, "repoId", err)
	}
	if err != nil {
		return port.Scope{}, s.fail("", err)
	}
	if r.LocalPath == "" {
		return port.Scope{}, settings.New(settings.CodeScopeInvalid, "repoId")
	}
	return port.RepoScope(r.LocalPath), nil
}

// Files reports where the config files of a scope are. The CLI's `config show`
// does not print a path, so it is derived from the documented layout:
// $COMMITBRIEF_CONFIG or ~/.commitbrief/config.yml, and
// <repo>/.commitbrief/config.yml.
func (s *Settings) Files(ctx context.Context, scope ConfigScope) (ConfigFiles, error) {
	var out ConfigFiles
	if s.d.Env != nil {
		if p, ok := s.d.Env.LookupEnv("COMMITBRIEF_CONFIG"); ok && p != "" {
			out.UserPath, out.UserPathFromEnv = p, true
		} else if home, err := s.d.Env.HomeDir(); err == nil && home != "" {
			out.UserPath = filepath.Join(home, ".commitbrief", "config.yml")
		}
	}
	if scope.RepoID != 0 {
		sc, err := s.resolveScope(ctx, scope)
		if err != nil {
			return ConfigFiles{}, err
		}
		out.RepoPath = filepath.Join(sc.RepoPath, ".commitbrief", "config.yml")
	}
	return out, nil
}

// ----- providers -----

// ProviderView is one provider of `commitbrief providers list`.
type ProviderView struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
	// Model is the configured model; empty means the provider's default.
	Model string `json:"model"`
	// KeyInConfig is false when the CLI found no key in its config files. It
	// does not mean "no key anywhere": a key from an environment variable or
	// api_key_env is not listed. The key itself, masked or not, is never
	// passed on.
	KeyInConfig bool `json:"keyInConfig"`
}

// ProviderConfig holds the two readable per-provider fields.
type ProviderConfig struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	BaseURL string `json:"baseUrl"`
}

// Providers lists the providers the CLI knows and which one is active.
func (s *Settings) Providers(ctx context.Context) ([]ProviderView, error) {
	infos, err := s.d.Engine.Providers(ctx)
	if err != nil {
		return nil, s.fail("", err)
	}
	out := make([]ProviderView, 0, len(infos))
	for _, in := range infos {
		out = append(out, ProviderView{Name: in.Name, Active: in.Active, Model: in.Model, KeyInConfig: in.KeyInConfig})
	}
	return out, nil
}

// ProviderConfig reads the model and base URL of one provider.
func (s *Settings) ProviderConfig(ctx context.Context, name string) (ProviderConfig, error) {
	if !settings.ValidProviderName(name) {
		return ProviderConfig{}, settings.New(settings.CodeProviderInvalid, "provider")
	}
	model, err := s.d.Engine.ConfigGet(ctx, settings.ProviderKey(name, "model"))
	if err != nil {
		return ProviderConfig{}, s.fail(settings.ProviderKey(name, "model"), err)
	}
	baseURL, err := s.d.Engine.ConfigGet(ctx, settings.ProviderKey(name, "base_url"))
	if err != nil {
		return ProviderConfig{}, s.fail(settings.ProviderKey(name, "base_url"), err)
	}
	return ProviderConfig{Name: name, Model: model, BaseURL: baseURL}, nil
}

// UseProvider makes a provider the active one, in the user file or a
// repository's.
func (s *Settings) UseProvider(ctx context.Context, name string, scope ConfigScope) error {
	if !settings.ValidProviderName(name) {
		return settings.New(settings.CodeProviderInvalid, "provider")
	}
	sc, err := s.resolveScope(ctx, scope)
	if err != nil {
		return err
	}
	return s.fail("provider", s.d.Engine.UseProvider(ctx, name, sc))
}

// TestProvider runs the CLI's connection test for a provider. It may call the
// provider's API, so it can take a while and fail with provider_error.
func (s *Settings) TestProvider(ctx context.Context, name string) error {
	if !settings.ValidProviderName(name) {
		return settings.New(settings.CodeProviderInvalid, "provider")
	}
	return s.fail("provider", s.d.Engine.TestProvider(ctx, name))
}

// ----- CLI version card -----

// CLI states of the version card.
const (
	CLIStateOK       = "ok"
	CLIStateNotFound = "not_found"
	CLIStateTooOld   = "too_old"
)

// CLICapabilities are the features the installed CLI supports (UI-0003
// section 3). A false flag disables the matching action.
type CLICapabilities struct {
	Base           bool `json:"base"`
	ConfigSetStdin bool `json:"configSetStdin"`
	PostingInCLI   bool `json:"postingInCli"`
	Posting        bool `json:"posting"`
}

// CLIStatus is the state of the commitbrief installation.
type CLIStatus struct {
	// State is CLIStateOK, CLIStateNotFound or CLIStateTooOld.
	State string `json:"state"`
	// Version is the CLI's version as it printed it; empty when not found.
	Version string `json:"version"`
	// Dev is true for a source build, which has no version number.
	Dev          bool            `json:"dev"`
	Commit       string          `json:"commit"`
	Capabilities CLICapabilities `json:"capabilities"`
}

// CLI reports whether the CLI is found, its version and what it can do. A
// missing CLI is a state, not an error.
func (s *Settings) CLI(ctx context.Context) (CLIStatus, error) {
	caps, err := s.d.Engine.Capabilities(ctx)
	if err != nil {
		if errcode.Is(err, errcode.CLINotFound) {
			return CLIStatus{State: CLIStateNotFound}, nil
		}
		return CLIStatus{}, s.fail("", err)
	}
	st := CLIStatus{
		State:   CLIStateOK,
		Version: caps.Version.Raw,
		Dev:     caps.Version.Dev,
		Commit:  caps.Version.Commit,
		Capabilities: CLICapabilities{
			Base:           caps.Base,
			ConfigSetStdin: caps.ConfigSetStdin,
			PostingInCLI:   caps.PostingInCLI,
			Posting:        caps.Posting,
		},
	}
	if !caps.Base {
		st.State = CLIStateTooOld
	}
	return st, nil
}

// CLIUpgrade is the result of an upgrade check.
type CLIUpgrade struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	Method          string `json:"method"`
	UpdateAvailable bool   `json:"updateAvailable"`
	// Action is, for a package-manager install, the command that upgrades it,
	// and, for a manual install, the release asset that would be installed.
	Action string `json:"action"`
	// Delegated is true when a package manager (Homebrew, Scoop, go install)
	// owns the binary: the UI shows Action instead of running an upgrade.
	Delegated bool `json:"delegated"`
}

// CheckCLIUpgrade asks the CLI whether a newer release exists
// (`commitbrief upgrade --json`; the CLI's only network call). Nothing is
// installed. It works with a CLI that is too old for the UI: updating is how
// that gets fixed.
func (s *Settings) CheckCLIUpgrade(ctx context.Context) (CLIUpgrade, error) {
	rep, err := s.d.Engine.CheckUpgrade(ctx)
	if err != nil {
		return CLIUpgrade{}, s.fail("", err)
	}
	return CLIUpgrade{
		Current:         rep.Current,
		Latest:          rep.Latest,
		Method:          rep.Method,
		UpdateAvailable: rep.UpdateAvailable,
		Action:          rep.Action,
		Delegated:       rep.Method != "" && rep.Method != upgradeMethodManual,
	}, nil
}

// UpgradeCLI installs the newer release with `commitbrief upgrade --yes` and
// returns the version card of what is installed afterwards. It re-checks first:
// the user clicked on a check made some time ago. When nothing newer exists it
// installs nothing. A package-manager install is refused with
// settings_upgrade_delegated; the UI shows the manager's command instead.
func (s *Settings) UpgradeCLI(ctx context.Context) (CLIStatus, error) {
	up, err := s.CheckCLIUpgrade(ctx)
	if err != nil {
		return CLIStatus{}, err
	}
	if up.UpdateAvailable {
		if up.Delegated {
			return CLIStatus{}, settings.New(settings.CodeUpgradeDelegated, "")
		}
		if err := s.d.Engine.Upgrade(ctx); err != nil {
			return CLIStatus{}, s.fail("", err)
		}
	}
	return s.CLI(ctx)
}

// ----- GitHub CLI -----

// GitHub reports whether gh is installed and logged in (UI-0005 section 1). A
// missing gh or a missing login is part of the status.
func (s *Settings) GitHub(ctx context.Context) (port.AuthStatus, error) {
	st, err := s.d.GitHub.AuthStatus(ctx)
	if err != nil {
		return port.AuthStatus{}, s.fail("", err)
	}
	if st.Scopes == nil {
		st.Scopes = []string{}
	}
	return st, nil
}

// ----- errors -----

// fail turns any error into the *settings.Error the frontend sees: a CLI or gh
// failure keeps its closed errcode as the code, a canceled context becomes
// "canceled", and everything else is "internal" with the cause kept for the
// log. A nil error stays nil (as a nil interface, not a typed nil pointer).
func (s *Settings) fail(field string, err error) error {
	if err == nil {
		return nil
	}
	var se *settings.Error
	if errors.As(err, &se) {
		return se
	}
	var ce *errcode.Error
	if errors.As(err, &ce) {
		return settings.Wrap(settings.Code(ce.Code), field, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return settings.Wrap(settings.CodeCanceled, "", err)
	}
	return settings.Wrap(settings.CodeInternal, "", err)
}
