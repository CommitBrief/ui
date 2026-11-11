// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"fmt"
	"time"

	"github.com/CommitBrief/ui/internal/domain/review"
)

// CLIVersion is the parsed output of `commitbrief --version`.
type CLIVersion struct {
	Major, Minor, Patch int
	// Pre is the pre-release suffix ("rc.1", "snapshot-abc"), empty for a
	// release.
	Pre string
	// Dev marks a source build whose version string is not a semantic version
	// ("dev"). Its version proves nothing about which features it has, so it
	// is at least no release (AtLeast is false); the adapter lets it pass the
	// base gate only so developers can run the UI, never a gate for a newer
	// feature.
	Dev bool
	// Raw is the version token exactly as the CLI printed it.
	Raw    string
	Commit string
	Date   string
}

// String renders the version for logs and messages.
func (v CLIVersion) String() string {
	if v.Dev {
		return v.Raw
	}
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// AtLeast reports whether v is the given release or newer. A pre-release sorts
// before its own release (1.22.0-rc.1 is not at least 1.22.0). A source build
// ("dev") has no version number and is not at least any release.
func (v CLIVersion) AtLeast(major, minor, patch int) bool {
	if v.Dev {
		return false
	}
	switch {
	case v.Major != major:
		return v.Major > major
	case v.Minor != minor:
		return v.Minor > minor
	case v.Patch != patch:
		return v.Patch > patch
	default:
		return v.Pre == ""
	}
}

// Capabilities says which UI features the installed CLI supports (UI-0003
// section 3). A false flag disables the matching action; it is not an error.
type Capabilities struct {
	Version CLIVersion
	// Base is local review, `remote pr --no-post --json`, config, providers
	// and `upgrade --json`.
	Base bool
	// ConfigSetStdin is true when the CLI can read a config value from stdin
	// (`config set <key> -`, v1.22.0). Without it, secrets such as API keys
	// cannot be written from the UI: SetConfig answers capability_missing and
	// never puts them on the command line.
	ConfigSetStdin bool
	// PostingInCLI is true when the installed CLI ships the machine-readable
	// posting report and verdict ceiling (CLI ADR-0048).
	PostingInCLI bool
	// Posting is true when the UI can actually post: PostingInCLI and the UI
	// side (PostPR) is implemented. A screen enables its Post action from this
	// field, never from PostingInCLI.
	Posting bool
}

// RunOptions are the knobs every CLI call shares.
type RunOptions struct {
	// Unattended adds --no-cost-check and --sandbox-rerun=0: nobody is there
	// to answer a prompt and the UI enforces the budget itself.
	Unattended bool
	// Timeout bounds the whole call; zero means the engine default.
	Timeout time.Duration
	// OnProgress receives the CLI's stderr line by line while it runs.
	OnProgress func(line string)
}

// LocalMode selects what a local review covers.
type LocalMode string

const (
	LocalStaged   LocalMode = "staged"
	LocalUnstaged LocalMode = "unstaged"
	LocalRange    LocalMode = "range"
)

// LocalReviewRequest is a review of a local repository.
type LocalReviewRequest struct {
	// RepoPath is the absolute path of the git repository root.
	RepoPath string
	Mode     LocalMode
	// Range holds the `git diff` revision arguments for LocalRange, for
	// example ["main..HEAD"] or ["main", "HEAD"].
	Range   []string
	Options RunOptions
}

// PRRequest addresses one GitHub pull request.
type PRRequest struct {
	// Repo is "owner/name" of the base repository.
	Repo   string
	Number int
	// LocalPath is an optional absolute path of a local clone; the CLI then
	// runs inside it and picks up the repository's own rules. Empty means the
	// application data directory.
	LocalPath string
	Options   RunOptions
}

// PostResult is the outcome of a posting-mode review. Its Remote field is set.
type PostResult = review.Report

// Scope selects which CLI config file a write goes to.
type Scope struct {
	// Local writes the repository's .commitbrief/config.yml instead of the
	// user-level file.
	Local bool
	// RepoPath is the absolute repository root; required when Local is true.
	RepoPath string
}

// UserScope writes the user-level config.
func UserScope() Scope { return Scope{} }

// RepoScope writes the config of the repository at repoPath.
func RepoScope(repoPath string) Scope { return Scope{Local: true, RepoPath: repoPath} }

// ProviderInfo is one line of `commitbrief providers list`.
type ProviderInfo struct {
	Name   string
	Active bool
	Model  string
	// KeyStatus is the masked key (or base URL for ollama, or the CLI's
	// "not set" text); it is a display string, never a usable secret.
	KeyStatus string
	// KeyInConfig is false when the CLI reported no key in its config files.
	// It does not mean "no key anywhere": `providers list` ignores keys that
	// come from an environment variable (ANTHROPIC_API_KEY and the like) or
	// api_key_env, and prints "(not set)" for them too.
	KeyInConfig bool
}

// UpgradeReport is `commitbrief upgrade --json`.
type UpgradeReport struct {
	Current         string
	Latest          string
	Method          string
	UpdateAvailable bool
	Action          string
}

// ReviewEngine is what the UI needs from the CommitBrief CLI. The single
// implementation is internal/adapter/clicmd. Every error it returns is an
// *errcode.Error.
//
// ReviewLocal and PreviewPR return a populated report together with an
// errcode.Degraded error when the provider answer could not be parsed into
// findings: the report then carries the raw model text in Content.
type ReviewEngine interface {
	Version(ctx context.Context) (CLIVersion, error)
	Capabilities(ctx context.Context) (Capabilities, error)
	ReviewLocal(ctx context.Context, req LocalReviewRequest) (review.Report, error)
	PreviewPR(ctx context.Context, req PRRequest) (review.Report, error)
	PostPR(ctx context.Context, req PRRequest) (PostResult, error)
	ConfigGet(ctx context.Context, key string) (string, error)
	SetConfig(ctx context.Context, key, value string, scope Scope) error
	Providers(ctx context.Context) ([]ProviderInfo, error)
	UseProvider(ctx context.Context, name string, scope Scope) error
	TestProvider(ctx context.Context, name string) error
	CheckUpgrade(ctx context.Context) (UpgradeReport, error)
	Upgrade(ctx context.Context) error
}
