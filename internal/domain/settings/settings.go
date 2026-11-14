// SPDX-License-Identifier: GPL-3.0-or-later

// Package settings is the domain model of the Settings screen: the UI's own
// settings (stored in the `settings` table, UI-0006 section 3), the catalogue
// of CLI configuration keys the UI may read and write through `commitbrief
// config`, and the error codes the screen reasons about. It performs no I/O.
package settings

import (
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/CommitBrief/ui/internal/domain/fspath"
)

// Code is a member of the closed set of settings error codes. The backend
// hands only the code (and the offending field) to the frontend, never text.
// Failures of the CLI keep their errcode.Code string ("cli_not_found",
// "cli_too_old", "capability_missing", "provider_error", ...); they travel in
// the same field, so the frontend has one error shape for the whole screen.
type Code string

const (
	// CodeKeyUnknown: the key is not one the UI manages.
	CodeKeyUnknown Code = "settings_key_unknown"
	// CodeKeyFileOnly: the key holds a list and can only be edited in the
	// config file; the CLI refuses `config set` for it.
	CodeKeyFileOnly Code = "settings_key_file_only"
	// CodeSecretNotReadable: a secret value (an API key) is never read back.
	CodeSecretNotReadable Code = "settings_secret_not_readable"
	// CodeSecretUserScopeOnly: a secret is written to the user-level config
	// only, never to a repository's .commitbrief/config.yml.
	CodeSecretUserScopeOnly Code = "settings_secret_user_scope_only"
	// CodeValueInvalid: the value does not fit the key's type or range.
	CodeValueInvalid Code = "settings_value_invalid"
	// CodeValueRejected: the CLI refused the value (or could not write it).
	CodeValueRejected Code = "settings_value_rejected"
	// CodeScopeInvalid: the repository scope does not exist or has no local
	// checkout.
	CodeScopeInvalid Code = "settings_scope_invalid"
	// CodeProviderInvalid: the provider name is not a valid name.
	CodeProviderInvalid Code = "settings_provider_invalid"
	// CodeUpgradeDelegated: the CLI was installed through a package manager,
	// which has to run the upgrade; the UI shows the command instead.
	CodeUpgradeDelegated Code = "settings_upgrade_delegated"
	// CodeConfigPathUnknown: the config file's location could not be worked out
	// (no home directory) or is not a usable local path.
	CodeConfigPathUnknown Code = "settings_config_path_unknown"
	// CodeConfigFileMissing: the config file does not exist yet (the CLI
	// creates it on the first `config set`).
	CodeConfigFileMissing Code = "settings_config_file_missing"
	// CodeOpenFailed: the system could not open the config file.
	CodeOpenFailed Code = "settings_open_failed"
	// CodeCanceled: the call was canceled by the frontend.
	CodeCanceled Code = "canceled"
	// CodeInternal: anything unexpected; details are in the log only.
	CodeInternal Code = "internal"
)

// Error is a classified settings error, serialized as {code, field}. Err (the
// cause, which may hold paths or process output) never leaves the process.
type Error struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
	Err   error  `json:"-"`
}

// Error returns only the code and the field, so text that crosses the process
// boundary is stable and free of paths and secrets.
func (e *Error) Error() string {
	if e.Field != "" {
		return string(e.Code) + " (" + e.Field + ")"
	}
	return string(e.Code)
}

// Unwrap exposes the cause for logs.
func (e *Error) Unwrap() error { return e.Err }

// Is matches by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// New builds an *Error for a code and an optional field.
func New(code Code, field string) *Error { return &Error{Code: code, Field: field} }

// Wrap builds an *Error that keeps cause for logs.
func Wrap(code Code, field string, cause error) *Error {
	return &Error{Code: code, Field: field, Err: cause}
}

// Keys of the `settings` table. The prefix before the dot is the group.
const (
	KeyLanguage         = "ui.language"
	KeyCLIPath          = "cli.path"
	KeyWorkers          = "worker.count"
	KeyDailyBudgetUSD   = "budget.daily_usd"
	KeyLogRetentionDays = "log.retention_days"
)

// Languages the interface is translated into. LanguageSystem follows the
// operating system and is resolved by the frontend.
const (
	LanguageSystem = "system"
	LanguageEN     = "en"
	LanguageTR     = "tr"
)

// Bounds and defaults of the UI settings.
const (
	MinWorkers     = 1
	MaxWorkers     = 4
	DefaultWorkers = 1

	// DefaultDailyBudgetUSD of 0 means no daily limit.
	DefaultDailyBudgetUSD = 0.0
	// MaxDailyBudgetUSD is a sanity bound against a typo (an extra digit).
	MaxDailyBudgetUSD = 10000.0

	MinLogRetentionDays     = 1
	MaxLogRetentionDays     = 365
	DefaultLogRetentionDays = 30

	// MaxCLIPathLen bounds the CLI path override.
	MaxCLIPathLen = 1024
)

// UI is the set of UI-only settings.
type UI struct {
	// Language is LanguageSystem, LanguageEN or LanguageTR.
	Language string `json:"language"`
	// CLIPath overrides where the commitbrief binary is looked up; empty means
	// discover it (UI-0003).
	CLIPath string `json:"cliPath"`
	// Workers is how many reviews the scheduler runs at once, 1 to 4.
	Workers int `json:"workers"`
	// DailyBudgetUSD stops the bot from starting new tasks once the day's cost
	// reaches it; 0 means no limit.
	DailyBudgetUSD float64 `json:"dailyBudgetUsd"`
	// LogRetentionDays is how long log files are kept.
	LogRetentionDays int `json:"logRetentionDays"`
}

// DefaultUI returns the settings of a fresh installation.
func DefaultUI() UI {
	return UI{
		Language:         LanguageSystem,
		Workers:          DefaultWorkers,
		DailyBudgetUSD:   DefaultDailyBudgetUSD,
		LogRetentionDays: DefaultLogRetentionDays,
	}
}

// Validate checks every field and reports the first invalid one as an *Error
// with Field set to the JSON name of the field.
func (u UI) Validate() error {
	switch u.Language {
	case LanguageSystem, LanguageEN, LanguageTR:
	default:
		return New(CodeValueInvalid, "language")
	}
	if err := ValidateCLIPath(u.CLIPath); err != nil {
		return err
	}
	if u.Workers < MinWorkers || u.Workers > MaxWorkers {
		return New(CodeValueInvalid, "workers")
	}
	if !(u.DailyBudgetUSD >= 0 && u.DailyBudgetUSD <= MaxDailyBudgetUSD) { // also rejects NaN
		return New(CodeValueInvalid, "dailyBudgetUsd")
	}
	if u.LogRetentionDays < MinLogRetentionDays || u.LogRetentionDays > MaxLogRetentionDays {
		return New(CodeValueInvalid, "logRetentionDays")
	}
	return nil
}

// ValidateCLIPath checks the shape of the CLI path override: empty, or an
// absolute, clean local path without a NUL byte. It looks at the string only;
// whether it names a usable binary is found out when the CLI is located. The
// fspath check comes first so a network or device path on Windows (\\host\share,
// \\?\, \\.\, \??\) is refused before anything can touch the file system.
func ValidateCLIPath(p string) error { return ValidateCLIPathFor(runtime.GOOS, p) }

// ValidateCLIPathFor is ValidateCLIPath with the operating system as a
// parameter, so the Windows rules can be tested on any machine. The
// platform-specific cleanliness check runs only when goos is the running OS.
// On Windows a drive path may use forward slashes (c:/opt/x.exe is accepted by
// fspath), but filepath.Clean rewrites them to backslashes; the path is
// compared with its backslash form so that only real redundancy (doubled
// separators, dot segments) counts as unclean.
func ValidateCLIPathFor(goos, p string) error {
	if p == "" {
		return nil
	}
	if len(p) > MaxCLIPathLen || fspath.ValidateLocalFor(goos, p) != nil {
		return New(CodeValueInvalid, "cliPath")
	}
	if goos == runtime.GOOS && (!filepath.IsAbs(p) || filepath.Clean(p) != filepath.FromSlash(p)) {
		return New(CodeValueInvalid, "cliPath")
	}
	return nil
}

// FromStored builds UI from the rows of the settings table. A missing, damaged
// or out-of-range row falls back to its default instead of failing: a hand-
// edited database must not keep the Settings screen from opening.
func FromStored(rows map[string]string) UI { return fromStoredFor(runtime.GOOS, rows) }

// fromStoredFor is FromStored with the operating system as a parameter, for
// testing the Windows path rules on any machine.
func fromStoredFor(goos string, rows map[string]string) UI {
	u := DefaultUI()
	if v, ok := rows[KeyLanguage]; ok {
		switch v {
		case LanguageSystem, LanguageEN, LanguageTR:
			u.Language = v
		}
	}
	if v, ok := rows[KeyCLIPath]; ok && ValidateCLIPathFor(goos, v) == nil {
		u.CLIPath = v
	}
	if n, err := strconv.Atoi(rows[KeyWorkers]); err == nil && n >= MinWorkers && n <= MaxWorkers {
		u.Workers = n
	}
	if f, err := strconv.ParseFloat(rows[KeyDailyBudgetUSD], 64); err == nil && f >= 0 && f <= MaxDailyBudgetUSD {
		u.DailyBudgetUSD = f
	}
	if n, err := strconv.Atoi(rows[KeyLogRetentionDays]); err == nil && n >= MinLogRetentionDays && n <= MaxLogRetentionDays {
		u.LogRetentionDays = n
	}
	return u
}

// ToStored renders u as the rows of the settings table. The CLI path row is
// omitted when empty (the default), so "unset" and "empty" are one state.
func (u UI) ToStored() (set map[string]string, unset []string) {
	set = map[string]string{
		KeyLanguage:         u.Language,
		KeyWorkers:          strconv.Itoa(u.Workers),
		KeyDailyBudgetUSD:   strconv.FormatFloat(u.DailyBudgetUSD, 'f', -1, 64),
		KeyLogRetentionDays: strconv.Itoa(u.LogRetentionDays),
	}
	if u.CLIPath == "" {
		unset = append(unset, KeyCLIPath)
	} else {
		set[KeyCLIPath] = u.CLIPath
	}
	return set, unset
}
