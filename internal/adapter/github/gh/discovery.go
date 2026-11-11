// SPDX-License-Identifier: GPL-3.0-or-later

// Package gh is the adapter that drives the GitHub CLI (UI-0005). It is, with
// internal/adapter/clicmd, the only package allowed to start processes, and the
// only one that runs `gh`: it finds the executable, builds argv without a
// shell, runs it in its own process tree and reduces what it prints to the
// ports' types. The UI stores no GitHub token; authentication is gh's.
package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

const (
	ghName = "gh"
	// probeWait bounds the login-shell PATH probe.
	probeWait  = 5 * time.Second
	probeLimit = 1 << 20
	// outputLimit caps what is read from gh: its answers are a few lines.
	outputLimit = 1 << 20
	// defaultTimeout bounds a gh call; `gh auth status` contacts the API.
	defaultTimeout   = 30 * time.Second
	defaultKillGrace = 2 * time.Second
	pipeWaitDelay    = 2 * time.Second
)

// Config wires a Client.
type Config struct {
	// Env is the process environment; nil means the real one.
	Env port.ProcessEnv
	// Timeout bounds one gh call; zero means 30 seconds.
	Timeout time.Duration
	// KillGrace is how long a canceled call has before the process tree is
	// killed; zero means 2 seconds.
	KillGrace time.Duration

	// GOOS, Probe and KnownDirs are test seams. Probe returns the login shell's
	// PATH (nil runs the real probe); KnownDirs replaces the well-known
	// install locations, which keeps a test from finding the developer's own
	// gh.
	GOOS      string
	Probe     func(ctx context.Context) (string, error)
	KnownDirs func() []string
}

// Client implements port.PRSource on top of the gh executable.
type Client struct {
	cfg Config

	mu     sync.Mutex
	probed bool
	shell  string
}

var _ port.PRSource = (*Client)(nil)

// New builds a Client. It does no I/O; gh is located on first use.
func New(cfg Config) *Client {
	if cfg.Env == nil {
		cfg.Env = platform.OSEnv{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = defaultKillGrace
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	return &Client{cfg: cfg}
}

// found is a located gh together with the environment its calls get.
type found struct {
	bin string
	env []string
}

// locate finds gh on the same PATH the CLI adapter builds (UI-0003 section 1):
// the login shell's PATH, the process PATH, then the well-known install
// locations. It reports false when there is no gh.
func (c *Client) locate(ctx context.Context) (found, bool) {
	dirs := platform.UniqueDirs(
		platform.SplitPathList(c.loginShellPath(ctx)),
		platform.SplitPathList(c.getenv("PATH")),
	)
	known := platform.ExistingDirs(c.knownDirs())
	all := platform.UniqueDirs(dirs, known)
	bin, ok := platform.LookupInDirs(all, ghName, c.cfg.GOOS)
	if !ok {
		return found{}, false
	}
	return found{bin: bin, env: c.childEnv(all)}, true
}

func (c *Client) getenv(key string) string {
	v, _ := c.cfg.Env.LookupEnv(key)
	return v
}

// childEnv is the process environment with PATH replaced by the discovered one
// and the variables that keep gh from prompting, paging, colouring or looking
// for updates. GH_TOKEN, GH_HOST and the like pass through: the identity is the
// one `commitbrief remote pr` uses.
func (c *Client) childEnv(pathDirs []string) []string {
	drop := map[string]bool{
		"PATH": true, "NO_COLOR": true, "GH_PROMPT_DISABLED": true,
		"GH_NO_UPDATE_NOTIFIER": true, "GH_PAGER": true, "PAGER": true,
		"GIT_TERMINAL_PROMPT": true, "CLICOLOR_FORCE": true,
	}
	base := c.cfg.Env.Environ()
	out := make([]string, 0, len(base)+8)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if c.cfg.GOOS == "windows" {
			name = strings.ToUpper(name)
		}
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	pathKey := "PATH"
	if c.cfg.GOOS == "windows" {
		pathKey = "Path"
	}
	return append(out,
		pathKey+"="+strings.Join(pathDirs, string(os.PathListSeparator)),
		"NO_COLOR=1",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_PAGER=cat",
		"PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
	)
}

func (c *Client) loginShellPath(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.probed {
		return c.shell
	}
	probe := c.cfg.Probe
	if probe == nil {
		probe = c.runLoginShellProbe
	}
	p, err := probe(ctx)
	if err != nil {
		p = "" // best effort: the process PATH and the known locations remain
	}
	// A probe that was cut short by the caller's context says nothing about the
	// shell; do not cache that.
	if ctx.Err() == nil {
		c.probed, c.shell = true, p
	}
	return p
}

// runLoginShellProbe asks the user's login shell for its PATH. internal/platform
// describes the command; it is executed here, where starting processes is
// allowed.
func (c *Client) runLoginShellProbe(ctx context.Context) (string, error) {
	cmd, ok := platform.LoginShellPathCommand(c.getenv)
	if !ok {
		return "", nil
	}
	res, err := c.run(ctx, runSpec{
		Bin:         cmd.Path,
		Args:        cmd.Args,
		Env:         c.cfg.Env.Environ(),
		Timeout:     probeWait,
		StdoutLimit: probeLimit,
		KillGrace:   time.Second,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("login shell exited with code %d", res.ExitCode)
	}
	p, ok := platform.ParseLoginShellPath(string(res.Stdout))
	if !ok {
		return "", errors.New("login shell printed no PATH")
	}
	return p, nil
}

// knownDirs are the well-known install locations of gh.
func (c *Client) knownDirs() []string {
	if c.cfg.KnownDirs != nil {
		return c.cfg.KnownDirs()
	}
	home := c.getenv("HOME")
	if h, err := c.cfg.Env.HomeDir(); err == nil && h != "" {
		home = h
	}
	join := func(base string, elem ...string) string {
		if base == "" {
			return ""
		}
		return filepath.Join(append([]string{base}, elem...)...)
	}
	switch c.cfg.GOOS {
	case "windows":
		scoop := c.getenv("SCOOP")
		if scoop == "" {
			scoop = join(home, "scoop")
		}
		return []string{
			join(c.getenv("ProgramFiles"), "GitHub CLI"),
			join(c.getenv("LOCALAPPDATA"), "Programs", "GitHub CLI"),
			join(scoop, "shims"),
		}
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	default:
		return []string{"/usr/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin", "/snap/bin", join(home, ".local", "bin")}
	}
}

// runSpec describes one child process.
type runSpec struct {
	Bin  string
	Args []string
	Env  []string
	// Timeout is a hard limit for the whole run; zero means none.
	Timeout     time.Duration
	StdoutLimit int64
	KillGrace   time.Duration
}

// runResult is what a finished process left behind.
type runResult struct {
	Stdout []byte
	Stderr []byte
	// ExitCode is the process exit code; -1 when it was killed by a signal.
	ExitCode int
	// Canceled and TimedOut say why the run was stopped early.
	Canceled bool
	TimedOut bool
}

// cappedBuffer collects up to limit bytes and drops the rest.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - int64(b.buf.Len()); room > 0 {
		if int64(len(p)) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// run starts spec's process in its own process tree with the null device as
// stdin, and enforces timeout and cancellation: on either, the whole tree gets
// the termination signal, then the kill after the grace period. It returns an
// error only when the process could not be started; a non-zero exit is
// reported through runResult.ExitCode.
func (c *Client) run(parent context.Context, spec runSpec) (runResult, error) {
	var res runResult
	ctx := parent
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
		defer cancel()
	}
	if ctx.Err() != nil {
		res.Canceled = errors.Is(parent.Err(), context.Canceled)
		res.TimedOut = !res.Canceled
		res.ExitCode = -1
		return res, nil
	}
	limit := spec.StdoutLimit
	if limit <= 0 {
		limit = outputLimit
	}
	grace := spec.KillGrace
	if grace <= 0 {
		grace = c.cfg.KillGrace
	}
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: outputLimit}

	cmd := exec.Command(spec.Bin, spec.Args...)
	cmd.Env = spec.Env
	cmd.Stdin = nil // the null device
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = platform.NewSysProcAttr()
	cmd.WaitDelay = pipeWaitDelay
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("start %s: %w", filepath.Base(spec.Bin), err)
	}
	tree, err := platform.AttachTree(cmd.Process.Pid)
	if err != nil {
		// Without a handle on the tree a cancel could leave descendants behind;
		// refuse to run rather than run unsupervised.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return res, fmt.Errorf("supervise %s: %w", filepath.Base(spec.Bin), err)
	}
	defer func() { _ = tree.Close() }()

	done := make(chan struct{})
	escalated := make(chan struct{})
	go func() {
		defer close(escalated)
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		_ = tree.Terminate()
		deadline := time.Now().Add(grace)
		for tree.Alive() {
			if time.Now().After(deadline) {
				_ = tree.Kill()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	<-escalated
	_ = tree.Kill() // nothing may outlive a finished call
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil
	}

	res.Stdout, res.Stderr = stdout.bytes(), stderr.bytes()
	res.ExitCode = -1
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err := parent.Err(); err != nil {
		res.Canceled = errors.Is(err, context.Canceled)
		res.TimedOut = errors.Is(err, context.DeadlineExceeded)
	} else if ctx.Err() != nil {
		res.TimedOut = true
	}
	var ee *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &ee) {
		return res, fmt.Errorf("wait for %s: %w", filepath.Base(spec.Bin), waitErr)
	}
	return res, nil
}

// interrupted turns a canceled or timed-out run into its errcode.
func interrupted(res runResult) error {
	if res.Canceled {
		return errcode.New(errcode.Canceled, "the gh call was canceled")
	}
	return errcode.New(errcode.Timeout, "the gh call exceeded its time limit")
}
