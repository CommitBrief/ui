// SPDX-License-Identifier: GPL-3.0-or-later

// Package clicmd is the adapter that drives the `commitbrief` CLI. It is the
// only package that executes it (UI-0003 section 4): it finds the binary,
// checks its version, builds argv without a shell, runs it in its own process
// tree, decodes JSON schema v1 into domain types, and reduces every failure to
// a closed errcode.Code.
package clicmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

const (
	defaultReviewTimeout = 10 * time.Minute
	// quickTimeout bounds calls that are not reviews: version, config,
	// providers list.
	quickTimeout = 30 * time.Second
	// providerTestTimeout and upgradeTimeout bound the calls that reach the
	// network.
	providerTestTimeout = 60 * time.Second
	upgradeTimeout      = 5 * time.Minute
	// backstopSlack is how much longer than the CLI's own --timeout the runner
	// waits, so the CLI reports its own expiry (timeout.exceeded) first.
	backstopSlack = 30 * time.Second
)

// postingWired is false until phase 12 writes PostPR. Capabilities.Posting
// stays false meanwhile, so no screen offers an action that would only answer
// capability_missing. Phase 12 flips it.
const postingWired = false

// Config wires an Engine.
type Config struct {
	// Env is the process environment; nil means the real one.
	Env port.ProcessEnv
	// DataDir is the application data directory (managed CLI copy, and the
	// working directory of PR calls that have no local clone).
	DataDir string
	// OverridePath returns the CLI path the user set in Settings; may be nil.
	OverridePath func() string
	// DefaultTimeout bounds a review when the request names none; zero means
	// 10 minutes.
	DefaultTimeout time.Duration
	// KillGrace is the SIGTERM -> SIGKILL window; zero means 5 seconds.
	KillGrace time.Duration

	// GOOS, Probe and BackstopSlack are test seams. BackstopSlack is how much
	// longer than the CLI's own --timeout the runner waits before it stops the
	// process itself; zero means 30 seconds.
	GOOS          string
	Probe         func(ctx context.Context) (string, error)
	KnownDirs     func() []string
	BackstopSlack time.Duration
}

// Engine implements port.ReviewEngine on top of the commitbrief binary.
type Engine struct {
	cfg  Config
	disc *Discoverer

	mu          sync.Mutex
	loc         *located
	locOverride string
}

var _ port.ReviewEngine = (*Engine)(nil)

// located is a discovered binary together with its version.
type located struct {
	disc Discovery
	ver  port.CLIVersion
}

// New builds an Engine. It does no I/O; the CLI is located on first use.
func New(cfg Config) *Engine {
	if cfg.Env == nil {
		cfg.Env = platform.OSEnv{}
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = defaultReviewTimeout
	}
	return &Engine{
		cfg: cfg,
		disc: &Discoverer{
			Env:       cfg.Env,
			GOOS:      cfg.GOOS,
			Override:  cfg.OverridePath,
			DataDir:   cfg.DataDir,
			Probe:     cfg.Probe,
			KnownDirs: cfg.KnownDirs,
		},
	}
}

// Invalidate forgets the located binary and its version, so the next call
// discovers again. Call it after Settings changed the CLI path, or after a
// managed copy was installed.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	e.loc = nil
	e.mu.Unlock()
	e.disc.Refresh()
}

// InstallManagedCLI downloads the managed CLI copy (UI-0003 section 2) and
// makes the next call discover it.
func (e *Engine) InstallManagedCLI(ctx context.Context) (InstalledCLI, error) {
	inst := &ManagedInstaller{DataDir: e.cfg.DataDir, GOOS: e.cfg.GOOS}
	out, err := inst.Install(ctx)
	if err != nil {
		return InstalledCLI{}, mapContextError(ctx, err)
	}
	e.Invalidate()
	return out, nil
}

// Discovery returns where the CLI was found and the PATH children get, for
// other adapters that need the same PATH (the gh adapter, UI-0005).
func (e *Engine) Discovery(ctx context.Context) (Discovery, error) {
	loc, err := e.locate(ctx)
	if err != nil {
		return Discovery{}, err
	}
	return loc.disc, nil
}

func (e *Engine) override() string {
	if e.cfg.OverridePath == nil {
		return ""
	}
	return strings.TrimSpace(e.cfg.OverridePath())
}

// locate finds the binary and reads its version. The result is cached until the
// override changes or Invalidate is called.
func (e *Engine) locate(ctx context.Context) (located, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.override()
	if e.loc != nil && e.locOverride == cur {
		return *e.loc, nil
	}
	disc, err := e.disc.Discover(ctx)
	if err != nil {
		return located{}, mapContextError(ctx, err)
	}
	ver, err := e.readVersion(ctx, disc)
	if err != nil {
		return located{}, err
	}
	l := located{disc: disc, ver: ver}
	e.loc, e.locOverride = &l, cur
	return l, nil
}

func (e *Engine) readVersion(ctx context.Context, disc Discovery) (port.CLIVersion, error) {
	res, err := runProcess(ctx, runSpec{
		Bin:       disc.Binary,
		Args:      []string{"--version"},
		Dir:       e.workDirIfExists(),
		Env:       disc.Env(e.cfg.Env.Environ()),
		Timeout:   quickTimeout,
		KillGrace: e.cfg.KillGrace,
	})
	if err != nil {
		var se *startError
		if errors.As(err, &se) {
			return port.CLIVersion{}, errcode.Wrap(errcode.CLINotFound, "could not start "+disc.Binary, err)
		}
		return port.CLIVersion{}, errcode.Wrap(errcode.Unknown, "reading the CLI version", err)
	}
	if res.Canceled || res.TimedOut {
		return port.CLIVersion{}, runInterrupted(res)
	}
	if res.ExitCode != 0 {
		return port.CLIVersion{}, errcode.New(errcode.CLINotFound, "the binary did not answer --version; it is not a usable commitbrief").
			WithDetail(tailString(res.StderrTail, 2048))
	}
	ver, err := ParseVersionOutput(string(res.Stdout))
	if err != nil {
		return port.CLIVersion{}, errcode.Wrap(errcode.CLINotFound, "the binary is not commitbrief", err)
	}
	return ver, nil
}

func (e *Engine) workDirIfExists() string {
	if e.cfg.DataDir == "" {
		return ""
	}
	if info, err := os.Stat(e.cfg.DataDir); err == nil && info.IsDir() {
		return e.cfg.DataDir
	}
	return ""
}

// requireBase locates the CLI and enforces the base capability.
func (e *Engine) requireBase(ctx context.Context) (located, error) {
	loc, err := e.locate(ctx)
	if err != nil {
		return located{}, err
	}
	if !Satisfies(loc.ver, CapBase) {
		return located{}, errcode.New(errcode.CLITooOld,
			fmt.Sprintf("commitbrief %s is older than the required %s", loc.ver, MinVersion(CapBase)))
	}
	return loc, nil
}

// Version implements port.ReviewEngine. It does not enforce a minimum.
func (e *Engine) Version(ctx context.Context) (port.CLIVersion, error) {
	loc, err := e.locate(ctx)
	if err != nil {
		return port.CLIVersion{}, err
	}
	return loc.ver, nil
}

// Capabilities implements port.ReviewEngine.
func (e *Engine) Capabilities(ctx context.Context) (port.Capabilities, error) {
	loc, err := e.locate(ctx)
	if err != nil {
		return port.Capabilities{}, err
	}
	inCLI := Satisfies(loc.ver, CapPosting)
	return port.Capabilities{
		Version:        loc.ver,
		Base:           Satisfies(loc.ver, CapBase),
		ConfigSetStdin: Satisfies(loc.ver, CapConfigSetStdin),
		PostingInCLI:   inCLI,
		Posting:        postingWired && inCLI,
	}, nil
}

// call is one CLI invocation.
type call struct {
	op     op
	args   []string
	stdin  []byte
	dir    string
	cliMax time.Duration // the CLI's own --timeout
	onLine func(string)
}

// run starts the CLI for c and turns "could not run it to completion" into a
// classified error. A finished process, whatever its exit code, comes back as a
// result for the caller to classify.
func (e *Engine) run(ctx context.Context, loc located, c call) (runResult, error) {
	if err := assertNoForbiddenArgs(c.args); err != nil {
		return runResult{}, err
	}
	res, err := runProcess(ctx, runSpec{
		Bin:          loc.disc.Binary,
		Args:         c.args,
		Dir:          c.dir,
		Env:          loc.disc.Env(e.cfg.Env.Environ()),
		Stdin:        c.stdin,
		Timeout:      c.cliMax + e.backstopSlack(),
		OnStderrLine: c.onLine,
		KillGrace:    e.cfg.KillGrace,
	})
	if err != nil {
		var se *startError
		if errors.As(err, &se) {
			e.Invalidate() // the binary may be gone or replaced
			return runResult{}, errcode.Wrap(errcode.CLINotFound, "could not start "+loc.disc.Binary, err)
		}
		return runResult{}, errcode.Wrap(errcode.Unknown, "running the CLI", err)
	}
	if res.Canceled || res.TimedOut {
		return res, runInterrupted(res)
	}
	if res.StdoutOverflow {
		return res, errcode.New(errcode.Unknown, fmt.Sprintf("the CLI wrote more than %d MiB to stdout", maxStdout>>20)).
			WithDetail(tailString(res.StderrTail, 2048))
	}
	return res, nil
}

func (e *Engine) backstopSlack() time.Duration {
	if e.cfg.BackstopSlack > 0 {
		return e.cfg.BackstopSlack
	}
	return backstopSlack
}

func runInterrupted(res runResult) error {
	if res.Canceled {
		return errcode.New(errcode.Canceled, "the run was canceled")
	}
	return errcode.New(errcode.Timeout, "the run exceeded its time limit").WithDetail(tailString(res.StderrTail, 2048))
}

func mapContextError(ctx context.Context, err error) error {
	var ce *errcode.Error
	if errors.As(err, &ce) {
		return err
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return errcode.Wrap(errcode.Canceled, "the run was canceled", err)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return errcode.Wrap(errcode.Timeout, "the run exceeded its time limit", err)
	default:
		return errcode.Wrap(errcode.Unknown, "", err)
	}
}

func (e *Engine) timeoutFor(opts port.RunOptions, fallback time.Duration) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return fallback
}

// reportFrom turns a finished review call into a report or a classified error.
// On a degraded review it returns both: the report holds the raw model text.
func reportFrom(res runResult, c call) (review.Report, error) {
	var parsed *review.Report
	if len(res.Stdout) > 0 {
		if rep, err := decodeReport(res.Stdout); err == nil {
			parsed = &rep
		} else if res.ExitCode == 0 {
			return review.Report{}, errcode.Wrap(errcode.Unknown, "the CLI output is not a usable report", err).
				WithDetail(tailString(res.StderrTail, 2048))
		}
	}
	if res.ExitCode != 0 {
		cerr := classify(failure{op: c.op, exitCode: res.ExitCode, stderr: res.StderrTail, report: parsed})
		if parsed != nil && cerr.Code == errcode.Degraded {
			return *parsed, cerr
		}
		return review.Report{}, cerr
	}
	if parsed == nil {
		return review.Report{}, errcode.New(errcode.Unknown, "the CLI produced no report").
			WithDetail(tailString(res.StderrTail, 2048))
	}
	if parsed.Degraded() {
		return *parsed, errcode.New(errcode.Degraded, "the provider answer degraded: "+parsed.Meta.DegradeReason).
			WithDetail(tailString(res.StderrTail, 2048))
	}
	return *parsed, nil
}

// ReviewLocal implements port.ReviewEngine: `commitbrief --json [diff <range>]`
// inside the repository.
func (e *Engine) ReviewLocal(ctx context.Context, req port.LocalReviewRequest) (review.Report, error) {
	if err := validateLocalPath(req.RepoPath); err != nil {
		return review.Report{}, err
	}
	timeout := e.timeoutFor(req.Options, e.cfg.DefaultTimeout)
	args, err := localReviewArgs(req, timeout)
	if err != nil {
		return review.Report{}, err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return review.Report{}, err
	}
	c := call{op: opReview, args: args, dir: req.RepoPath, cliMax: timeout, onLine: req.Options.OnProgress}
	res, err := e.run(ctx, loc, c)
	if err != nil {
		return review.Report{}, err
	}
	return reportFrom(res, c)
}

// PreviewPR implements port.ReviewEngine: `commitbrief remote pr N --no-post --json`.
func (e *Engine) PreviewPR(ctx context.Context, req port.PRRequest) (review.Report, error) {
	timeout := e.timeoutFor(req.Options, e.cfg.DefaultTimeout)
	args, err := previewPRArgs(req, timeout)
	if err != nil {
		return review.Report{}, err
	}
	dir := req.LocalPath
	if dir != "" {
		if err := validateLocalPath(dir); err != nil {
			return review.Report{}, err
		}
	} else if dir, err = e.ensureDataDir(); err != nil {
		return review.Report{}, err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return review.Report{}, err
	}
	// Preflight in the UI, before the CLI is started (UI-0004 section 5): a file
	// lookup only, nothing is executed.
	if _, ok := loc.disc.FindTool("gh"); !ok {
		return review.Report{}, errcode.New(errcode.GHNotFound, "the gh CLI was not found on the discovered PATH")
	}
	c := call{op: opReview, args: args, dir: dir, cliMax: timeout, onLine: req.Options.OnProgress}
	res, err := e.run(ctx, loc, c)
	if err != nil {
		return review.Report{}, err
	}
	return reportFrom(res, c)
}

// PostPR implements port.ReviewEngine. The signature is final; the body arrives
// with phase 12, which needs CLI ADR-0048 (released as v1.22.0) for the posting
// report and the verdict ceiling. Until then it reports the missing capability.
func (e *Engine) PostPR(_ context.Context, req port.PRRequest) (port.PostResult, error) {
	if err := validateRepo(req.Repo); err != nil {
		return port.PostResult{}, err
	}
	if err := validatePRNumber(req.Number); err != nil {
		return port.PostResult{}, err
	}
	return port.PostResult{}, errcode.New(errcode.CapabilityMissing,
		"posting reviews is not available yet (needs commitbrief "+MinVersion(CapPosting)+" or later and phase 12)")
}

func (e *Engine) ensureDataDir() (string, error) {
	if e.cfg.DataDir == "" {
		return "", errcode.New(errcode.Unknown, "no application data directory configured")
	}
	if err := os.MkdirAll(e.cfg.DataDir, 0o700); err != nil {
		return "", errcode.Wrap(errcode.Unknown, "creating the application data directory", err)
	}
	return e.cfg.DataDir, nil
}

// simple runs a non-review call and returns its stdout on exit 0.
func (e *Engine) simple(ctx context.Context, loc located, c call) (string, error) {
	res, err := e.run(ctx, loc, c)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", classify(failure{op: c.op, exitCode: res.ExitCode, stderr: res.StderrTail})
	}
	return string(res.Stdout), nil
}

// scopeDir resolves the working directory of a config write: the repository
// for a local scope, otherwise the data directory when it exists.
func (e *Engine) scopeDir(scope port.Scope) (string, error) {
	if !scope.Local {
		return e.workDirIfExists(), nil
	}
	if err := validateLocalPath(scope.RepoPath); err != nil {
		return "", err
	}
	return scope.RepoPath, nil
}

// ConfigGet implements port.ReviewEngine: `commitbrief config get <key>`. Keys
// that hold a secret are refused, so an API key is never read into the UI.
func (e *Engine) ConfigGet(ctx context.Context, key string) (string, error) {
	if isSecretKey(key) {
		return "", invalid("secret config keys are never read")
	}
	args, err := configGetArgs(key, quickTimeout)
	if err != nil {
		return "", err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return "", err
	}
	out, err := e.simple(ctx, loc, call{op: opConfig, args: args, dir: e.workDirIfExists(), cliMax: quickTimeout})
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r"), nil
}

// SetConfig implements port.ReviewEngine: `commitbrief config set [--local] <key> <value>`.
//
// A secret value (providers.<n>.api_key and every key isSecretKey recognises)
// never enters argv: it would be readable by every local user through `ps` or
// /proc/<pid>/cmdline. It is written as `config set <key> -` with the value on
// stdin, which needs CLI v1.22.0 (capability config_set_stdin). On an older CLI
// the call fails with capability_missing; there is no fallback to argv. The
// error detail of such a call has the value scrubbed.
func (e *Engine) SetConfig(ctx context.Context, key, value string, scope port.Scope) error {
	args, stdin, viaStdin, err := configSetArgs(key, value, scope, quickTimeout)
	if err != nil {
		return err
	}
	dir, err := e.scopeDir(scope)
	if err != nil {
		return err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return err
	}
	if viaStdin && !Satisfies(loc.ver, CapConfigSetStdin) {
		return errcode.New(errcode.CapabilityMissing,
			"writing this value needs commitbrief "+MinVersion(CapConfigSetStdin)+" or later (config set reading stdin); it is never passed on the command line")
	}
	_, err = e.simple(ctx, loc, call{op: opConfig, args: args, stdin: stdin, dir: dir, cliMax: quickTimeout})
	if viaStdin && isSecretKey(key) {
		err = scrubSecret(err, value)
	}
	return err
}

// scrubSecret removes a secret from an error's raw diagnostic text.
func scrubSecret(err error, secret string) error {
	var ce *errcode.Error
	if err == nil || secret == "" || !errors.As(err, &ce) {
		return err
	}
	c := *ce
	c.Detail = strings.ReplaceAll(c.Detail, secret, "***")
	c.Message = strings.ReplaceAll(c.Message, secret, "***")
	return &c
}

// providerLineRe matches one row of `commitbrief providers list`:
// "* anthropic   model=claude-…   key=sk-ant-…xxxx".
var providerLineRe = regexp.MustCompile(`^(\* |  )(\S+)\s+model=(.*?)\s+key=(.*)$`)

// keyNotSetTexts are the CLI's localized "(not set)" markers (en, tr).
var keyNotSetTexts = map[string]bool{"(not set)": true, "(ayarlanmadı)": true}

func parseProviders(out string) []port.ProviderInfo {
	infos := []port.ProviderInfo{}
	for _, line := range strings.Split(out, "\n") {
		m := providerLineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		model := strings.TrimSpace(m[3])
		if model == "—" {
			model = ""
		}
		key := strings.TrimSpace(m[4])
		infos = append(infos, port.ProviderInfo{
			Name:        m[2],
			Active:      m[1] == "* ",
			Model:       model,
			KeyStatus:   key,
			KeyInConfig: !keyNotSetTexts[key],
		})
	}
	return infos
}

// Providers implements port.ReviewEngine: `commitbrief providers list`.
func (e *Engine) Providers(ctx context.Context) ([]port.ProviderInfo, error) {
	loc, err := e.requireBase(ctx)
	if err != nil {
		return nil, err
	}
	out, err := e.simple(ctx, loc, call{op: opProviders, args: providersListArgs(quickTimeout), dir: e.workDirIfExists(), cliMax: quickTimeout})
	if err != nil {
		return nil, err
	}
	return parseProviders(out), nil
}

// UseProvider implements port.ReviewEngine: `commitbrief providers use [--local] <name>`.
func (e *Engine) UseProvider(ctx context.Context, name string, scope port.Scope) error {
	args, err := providersUseArgs(name, scope, quickTimeout)
	if err != nil {
		return err
	}
	dir, err := e.scopeDir(scope)
	if err != nil {
		return err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return err
	}
	_, err = e.simple(ctx, loc, call{op: opProviders, args: args, dir: dir, cliMax: quickTimeout})
	return err
}

// TestProvider implements port.ReviewEngine: `commitbrief providers test <name>`.
func (e *Engine) TestProvider(ctx context.Context, name string) error {
	args, err := providersTestArgs(name, providerTestTimeout)
	if err != nil {
		return err
	}
	loc, err := e.requireBase(ctx)
	if err != nil {
		return err
	}
	_, err = e.simple(ctx, loc, call{op: opProviderTest, args: args, dir: e.workDirIfExists(), cliMax: providerTestTimeout})
	return err
}

// CheckUpgrade implements port.ReviewEngine: `commitbrief upgrade --json`.
//
// It does not enforce the base minimum version: updating is how a CLI that is
// too old for the UI gets fixed.
func (e *Engine) CheckUpgrade(ctx context.Context) (port.UpgradeReport, error) {
	loc, err := e.locate(ctx)
	if err != nil {
		return port.UpgradeReport{}, err
	}
	out, err := e.simple(ctx, loc, call{op: opUpgrade, args: upgradeCheckArgs(), dir: e.workDirIfExists(), cliMax: quickTimeout})
	if err != nil {
		return port.UpgradeReport{}, err
	}
	rep, err := decodeUpgrade([]byte(out))
	if err != nil {
		return port.UpgradeReport{}, errcode.Wrap(errcode.Unknown, "the upgrade report is not usable", err)
	}
	return rep, nil
}

// Upgrade implements port.ReviewEngine: `commitbrief upgrade --yes`. The located
// binary and its version are forgotten afterwards, since the upgrade replaced
// them. Like CheckUpgrade it needs no minimum version.
func (e *Engine) Upgrade(ctx context.Context) error {
	loc, err := e.locate(ctx)
	if err != nil {
		return err
	}
	_, err = e.simple(ctx, loc, call{op: opUpgrade, args: upgradeArgs(), dir: e.workDirIfExists(), cliMax: upgradeTimeout})
	e.Invalidate()
	return err
}
