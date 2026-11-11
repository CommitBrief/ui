// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// SettingsService is the Wails-bound face of the Settings use cases: the CLI's
// configuration and providers, the CLI version card, the `gh` status and the
// UI's own settings. Its exported methods, and only those, are what the
// frontend can call (frontend/bindings). Like ReposService it adds no logic
// beyond shaping errors: every error leaves as a *settings.Error, serialized as
// {code, field}. Causes (paths, process output) stay in the log.
//
// Secret values (an API key passed to SetConfig) are never logged: no log line
// in this file mentions an argument, only the operation and the error.
type SettingsService struct {
	settings *app.Settings
	log      *slog.Logger
}

// NewSettingsService binds the use cases. A nil logger means slog.Default().
func NewSettingsService(s *app.Settings, log *slog.Logger) *SettingsService {
	if log == nil {
		log = slog.Default()
	}
	return &SettingsService{settings: s, log: log}
}

// GetUI returns the UI's own settings.
func (s *SettingsService) GetUI(ctx context.Context) (settings.UI, error) {
	out, err := s.settings.UI(ctx)
	return out, s.fail("get UI settings", err)
}

// UpdateUI validates and stores all UI settings and returns what is stored.
func (s *SettingsService) UpdateUI(ctx context.Context, in settings.UI) (settings.UI, error) {
	out, err := s.settings.UpdateUI(ctx, in)
	return out, s.fail("update UI settings", err)
}

// ListConfig reads every CLI configuration key the screen shows.
func (s *SettingsService) ListConfig(ctx context.Context) ([]app.ConfigEntry, error) {
	out, err := s.settings.ListConfig(ctx)
	return out, s.fail("list CLI config", err)
}

// GetConfig reads one CLI configuration key. API keys are refused.
func (s *SettingsService) GetConfig(ctx context.Context, key string) (app.ConfigEntry, error) {
	out, err := s.settings.GetConfig(ctx, key)
	return out, s.fail("get CLI config key", err)
}

// SetConfig writes one CLI configuration key through `commitbrief config set`.
// The value of an API key goes to the CLI on stdin and is never returned.
func (s *SettingsService) SetConfig(ctx context.Context, key, value string, scope app.ConfigScope) error {
	return s.fail("set CLI config key", s.settings.SetConfig(ctx, key, value, scope))
}

// ConfigFiles tells where the CLI's config files are.
func (s *SettingsService) ConfigFiles(ctx context.Context, scope app.ConfigScope) (app.ConfigFiles, error) {
	out, err := s.settings.Files(ctx, scope)
	return out, s.fail("locate CLI config files", err)
}

// Providers lists the CLI's providers and which one is active.
func (s *SettingsService) Providers(ctx context.Context) ([]app.ProviderView, error) {
	out, err := s.settings.Providers(ctx)
	return out, s.fail("list providers", err)
}

// ProviderConfig reads the model and base URL of one provider.
func (s *SettingsService) ProviderConfig(ctx context.Context, name string) (app.ProviderConfig, error) {
	out, err := s.settings.ProviderConfig(ctx, name)
	return out, s.fail("read provider config", err)
}

// UseProvider makes a provider the active one.
func (s *SettingsService) UseProvider(ctx context.Context, name string, scope app.ConfigScope) error {
	return s.fail("use provider", s.settings.UseProvider(ctx, name, scope))
}

// TestProvider runs the CLI's connection test for a provider.
func (s *SettingsService) TestProvider(ctx context.Context, name string) error {
	return s.fail("test provider", s.settings.TestProvider(ctx, name))
}

// CLIStatus reports whether commitbrief is found, its version and capabilities.
func (s *SettingsService) CLIStatus(ctx context.Context) (app.CLIStatus, error) {
	out, err := s.settings.CLI(ctx)
	return out, s.fail("read CLI status", err)
}

// CheckCLIUpgrade asks whether a newer CLI release exists. Nothing is installed.
func (s *SettingsService) CheckCLIUpgrade(ctx context.Context) (app.CLIUpgrade, error) {
	out, err := s.settings.CheckCLIUpgrade(ctx)
	return out, s.fail("check CLI upgrade", err)
}

// UpgradeCLI installs the newer CLI release and returns the new version card.
// An install owned by a package manager is refused with
// settings_upgrade_delegated.
func (s *SettingsService) UpgradeCLI(ctx context.Context) (app.CLIStatus, error) {
	out, err := s.settings.UpgradeCLI(ctx)
	return out, s.fail("upgrade CLI", err)
}

// GitHubStatus reports whether gh is installed and logged in.
func (s *SettingsService) GitHubStatus(ctx context.Context) (port.AuthStatus, error) {
	out, err := s.settings.GitHub(ctx)
	return out, s.fail("read gh status", err)
}

// fail hands the frontend a copy of the classified error with no cause. The
// application layer classifies everything; an error that is somehow not a
// *settings.Error is reported as "internal".
func (s *SettingsService) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	var se *settings.Error
	if !errors.As(err, &se) {
		s.log.Error("settings operation failed", "op", op, "error", err)
		return settings.New(settings.CodeInternal, "")
	}
	switch se.Code {
	case settings.CodeInternal:
		s.log.Error("settings operation failed", "op", op, "error", se.Err)
	case settings.CodeCanceled:
		// The user moved on; not worth a line.
	default:
		if se.Err != nil {
			s.log.Debug("settings operation failed", "op", op, "code", se.Code, "field", se.Field, "cause", se.Err)
		}
	}
	return &settings.Error{Code: se.Code, Field: se.Field}
}
