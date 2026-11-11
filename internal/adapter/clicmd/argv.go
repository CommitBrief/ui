// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

// Argument construction (UI-0003 section 4). There is never a shell: every
// builder returns an argv slice that goes straight to the process. Everything
// that originates outside the adapter is validated here, and every
// value that could be read as a flag is either rejected or placed after `--`.

const (
	// flagAllowSecrets bypasses the CLI's pre-send secret scanner. The UI never
	// passes it; assertNoForbiddenArgs enforces that on every call.
	flagAllowSecrets = "--allow-secrets"

	maxPRNumber      = 1<<31 - 1
	maxConfigValue   = 4096
	maxRangeArgs     = 4
	maxArgumentBytes = 256
)

var (
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	revisionRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/@^~:{}+-]*$`)
	configKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`)
	providerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// errInvalidArgument marks a request that the adapter refuses before running
// anything. It surfaces as errcode.Unknown: a bad argument is a programming
// error of the caller, not a failure class of the CLI.
var errInvalidArgument = errors.New("invalid argument")

func invalid(format string, a ...any) error {
	return errcode.Wrap(errcode.Unknown, "refused: "+fmt.Sprintf(format, a...), errInvalidArgument)
}

// validateRepo accepts "owner/name" only.
func validateRepo(repo string) error {
	if len(repo) > maxArgumentBytes || !repoRe.MatchString(repo) {
		return invalid("repository must look like owner/name")
	}
	name := repo[strings.IndexByte(repo, '/')+1:]
	if name == "." || name == ".." {
		return invalid("repository name %q is not allowed", name)
	}
	return nil
}

// validatePRNumber accepts a positive integer that fits GitHub's range.
func validatePRNumber(n int) error {
	if n <= 0 || n > maxPRNumber {
		return invalid("pull request number must be a positive integer")
	}
	return nil
}

// validateLocalPath requires an absolute, clean path that is the root of a git
// repository: it must contain .git (a directory, or a file for a worktree).
// It does not run git.
func validateLocalPath(p string) error {
	if p == "" || !filepath.IsAbs(p) {
		return invalid("local path must be absolute")
	}
	if filepath.Clean(p) != p {
		return invalid("local path must be clean (no '..', '.', or duplicate separators)")
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return errcode.New(errcode.NotARepo, "local path is not a directory")
	}
	if _, err := os.Lstat(filepath.Join(p, ".git")); err != nil {
		return errcode.New(errcode.NotARepo, "local path is not a git repository root")
	}
	return nil
}

// validateRevision accepts one `git diff` revision argument. A leading '-' would
// make git read it as an option (--output=FILE writes files), so it is refused.
func validateRevision(arg string) error {
	if len(arg) > maxArgumentBytes || !revisionRe.MatchString(arg) {
		return invalid("%q is not a valid revision argument", arg)
	}
	return nil
}

func validateConfigKey(key string) error {
	if len(key) > maxArgumentBytes || !configKeyRe.MatchString(key) {
		return invalid("%q is not a valid config key", key)
	}
	return nil
}

func validateConfigValue(value string) error {
	if len(value) > maxConfigValue || strings.ContainsRune(value, 0) {
		return invalid("config value is too long or contains a NUL byte")
	}
	return nil
}

func validateProviderName(name string) error {
	if !providerRe.MatchString(name) {
		return invalid("%q is not a valid provider name", name)
	}
	return nil
}

// commonFlags are on every call: no colour, a bounded run, and, when nobody can
// answer a prompt, no cost check and no sandbox rerun. --allow-secrets is
// deliberately not here and is never added anywhere.
func commonFlags(timeout time.Duration, unattended bool) []string {
	flags := []string{"--color", "never", "--timeout", formatTimeout(timeout)}
	if unattended {
		flags = append(flags, "--no-cost-check", "--sandbox-rerun=0")
	}
	return flags
}

// formatTimeout renders d as whole seconds ("600s"), rounding up so the CLI's
// own deadline is never shorter than asked.
func formatTimeout(d time.Duration) string {
	secs := int64((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10) + "s"
}

// localReviewArgs builds `commitbrief [diff <range>] --json ...`.
func localReviewArgs(req port.LocalReviewRequest, timeout time.Duration) ([]string, error) {
	flags := append([]string{"--json"}, commonFlags(timeout, req.Options.Unattended)...)
	switch req.Mode {
	case port.LocalStaged:
		return append(flags, "--staged"), nil
	case port.LocalUnstaged:
		return append(flags, "--unstaged"), nil
	case port.LocalRange:
		if len(req.Range) == 0 || len(req.Range) > maxRangeArgs {
			return nil, invalid("a range review needs 1 to %d revision arguments", maxRangeArgs)
		}
		for _, r := range req.Range {
			if err := validateRevision(r); err != nil {
				return nil, err
			}
		}
		args := append([]string{"diff"}, flags...)
		return append(args, req.Range...), nil
	default:
		return nil, invalid("unknown local review mode %q", req.Mode)
	}
}

// previewPRArgs builds `commitbrief remote pr N --repo owner/name --no-post --json ...`.
func previewPRArgs(req port.PRRequest, timeout time.Duration) ([]string, error) {
	if err := validateRepo(req.Repo); err != nil {
		return nil, err
	}
	if err := validatePRNumber(req.Number); err != nil {
		return nil, err
	}
	args := []string{"remote", "pr", strconv.Itoa(req.Number), "--repo", req.Repo, "--no-post", "--json"}
	return append(args, commonFlags(timeout, req.Options.Unattended)...), nil
}

// postPRArgs builds `commitbrief remote pr N --repo owner/name --json
// --since-last-review --max-verdict comment ...`: the posting mode (no
// --no-post). The verdict ceiling is fixed here, not a parameter: the UI never
// submits an approval (UI-0008 section 3). --since-last-review makes later runs
// on the same pull request incremental (CLI ADR-0046).
func postPRArgs(req port.PRRequest, timeout time.Duration) ([]string, error) {
	if err := validateRepo(req.Repo); err != nil {
		return nil, err
	}
	if err := validatePRNumber(req.Number); err != nil {
		return nil, err
	}
	args := []string{
		"remote", "pr", strconv.Itoa(req.Number), "--repo", req.Repo,
		"--json", "--since-last-review", "--max-verdict", "comment",
	}
	return append(args, commonFlags(timeout, req.Options.Unattended)...), nil
}

func configGetArgs(key string, timeout time.Duration) ([]string, error) {
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}
	return append(append([]string{"config", "get"}, commonFlags(timeout, false)...), "--", key), nil
}

// stdinValueArg is the value argument that tells `config set` to read the value
// from stdin (CLI phase 11b).
const stdinValueArg = "-"

// secretLastSegments are the final config-key segments whose value is a
// credential. config-schema.md names providers.<n>.api_key; the others are a
// conservative guard for keys the CLI may add. `api_key_env` is a variable
// *name*, not a secret, and does not match.
var secretLastSegments = map[string]bool{
	"api_key": true, "token": true, "secret": true, "password": true, "passphrase": true,
}

// isSecretKey reports whether the value of a config key is a credential. Such
// values never enter argv.
func isSecretKey(key string) bool {
	last := key
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		last = key[i+1:]
	}
	last = strings.ToLower(last)
	return secretLastSegments[last] ||
		strings.HasSuffix(last, "_api_key") || strings.HasSuffix(last, "_secret") ||
		strings.HasSuffix(last, "_token") || strings.HasSuffix(last, "_password")
}

// configSetArgs builds `config set [--local] -- <key> <value>`. A secret value
// (and the literal value "-", which the CLI would otherwise read as "take it
// from stdin") travels on stdin instead: the returned argv then ends in "-" and
// stdin holds the value, with no trailing newline. viaStdin reports which way
// it went. The value is never in argv in the stdin form.
func configSetArgs(key, value string, scope port.Scope, timeout time.Duration) (args []string, stdin []byte, viaStdin bool, err error) {
	if err := validateConfigKey(key); err != nil {
		return nil, nil, false, err
	}
	if err := validateConfigValue(value); err != nil {
		return nil, nil, false, err
	}
	viaStdin = isSecretKey(key) || value == stdinValueArg
	if viaStdin && isSecretKey(key) {
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return nil, nil, false, invalid("a secret value must be a single non-empty line")
		}
	}
	args = append([]string{"config", "set"}, commonFlags(timeout, false)...)
	if scope.Local {
		args = append(args, "--local")
	}
	if viaStdin {
		return append(args, "--", key, stdinValueArg), []byte(value), true, nil
	}
	return append(args, "--", key, value), nil, false, nil
}

func providersListArgs(timeout time.Duration) []string {
	return append([]string{"providers", "list"}, commonFlags(timeout, false)...)
}

func providersUseArgs(name string, scope port.Scope, timeout time.Duration) ([]string, error) {
	if err := validateProviderName(name); err != nil {
		return nil, err
	}
	args := append([]string{"providers", "use"}, commonFlags(timeout, false)...)
	if scope.Local {
		args = append(args, "--local")
	}
	return append(args, "--", name), nil
}

func providersTestArgs(name string, timeout time.Duration) ([]string, error) {
	if err := validateProviderName(name); err != nil {
		return nil, err
	}
	return append(append([]string{"providers", "test"}, commonFlags(timeout, false)...), "--", name), nil
}

// upgradeCheckArgs: `--json` implies `--check`, so nothing is installed. The
// upgrade calls carry no --timeout and need no minimum version: they are how a
// CLI that is too old for the UI gets updated, so they use only flags every CLI
// with an `upgrade` command has. The runner's own timeout bounds them.
func upgradeCheckArgs() []string {
	return []string{"upgrade", "--json", "--color", "never"}
}

// upgradeArgs: --yes skips the confirmation prompt that a null stdin would
// otherwise answer with "no". It is the only call where the UI asks for an
// install, and only ever after the user clicked Upgrade.
func upgradeArgs() []string {
	return []string{"upgrade", "--yes", "--color", "never"}
}

// assertNoForbiddenArgs is the last gate before a process starts. It rejects
// --allow-secrets wherever it appears, including as a value, because the UI
// must never be the thing that disables the secret scanner.
func assertNoForbiddenArgs(args []string) error {
	for _, a := range args {
		if a == flagAllowSecrets || strings.HasPrefix(a, flagAllowSecrets+"=") {
			return invalid("%s is never passed to the CLI", flagAllowSecrets)
		}
	}
	return nil
}
