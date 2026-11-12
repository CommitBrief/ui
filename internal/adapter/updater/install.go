// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CommitBrief/ui/internal/domain/update"
	"github.com/CommitBrief/ui/internal/port"
)

// Feed is the release source the updater drives. *Source implements it.
type Feed interface {
	// Latest returns the authenticated candidate newer than current on the
	// channel, or nil.
	Latest(ctx context.Context, current update.Version, channel update.Channel) (*Candidate, error)
	// Open starts the download of the candidate's artefact.
	Open(ctx context.Context, c *Candidate) (io.ReadCloser, int64, error)
}

// Artefact is a downloaded file that has already been checked against the
// signed checksum.
type Artefact struct {
	// Path is the file in the staging directory.
	Path string
	// Name is the release artefact name.
	Name string
	// Version is the release version.
	Version string
	// SHA256 is the signed digest the file was verified against.
	SHA256 [sha256.Size]byte
}

// Backend puts a verified artefact in place of the running application. The
// desktop adapter implements it on the Wails updater, whose helper process does
// the swap after this process has quit and restores the previous version when
// the swap or the first launch fails.
type Backend interface {
	// Stage unpacks and validates the artefact so a swap can follow. It must
	// not touch the running installation.
	Stage(ctx context.Context, a Artefact) error
	// Apply swaps the staged payload in and restarts into it. It returns once
	// the swap has been handed over. On error the running installation must be
	// intact.
	Apply(ctx context.Context) error
	// Discard drops what Stage prepared and leaves the running installation as
	// it was. It is called after a failed Stage or Apply; an error from it means
	// the previous version could not be put back.
	Discard(ctx context.Context) error
}

// Config configures the Updater.
type Config struct {
	Feed    Feed
	Backend Backend
	// Current is the running version.
	Current string
	// Delegation, when set, turns the updater off: Check and Install fail with
	// update.CodeDelegated. A development build is delegated automatically.
	Delegation update.Delegation
	// StagingDir is where downloads land before they are checked. It is created
	// (0700) on first use.
	StagingDir string
	// MaxArtefactBytes caps a download; zero means DefaultMaxArtefactBytes.
	MaxArtefactBytes int64
	// StallTimeout ends a download that receives no bytes for this long; zero
	// means DefaultStallTimeout.
	StallTimeout time.Duration
	// Observer is told about every status change (progress at most every
	// progressEvery). It must not block.
	Observer func(update.Status)
	Logger   *slog.Logger
}

// progressEvery is the pause between two progress notifications.
const progressEvery = 250 * time.Millisecond

// DefaultStallTimeout is how long a download may receive nothing before it is
// abandoned.
const DefaultStallTimeout = 60 * time.Second

// Updater is the self-update engine: it checks the signed release feed,
// downloads and verifies an artefact and hands it to the Backend.
type Updater struct {
	cfg     Config
	current update.Version
	max     int64

	mu      sync.Mutex
	status  update.Status
	pending *Candidate
	busy    bool // a check or an install is running
}

var _ port.Updater = (*Updater)(nil)

// New builds the engine. A running version that is not a stamped release (a
// development build, or a string that is not a semantic version) disables it.
func New(cfg Config) *Updater {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	u := &Updater{cfg: cfg, max: cfg.MaxArtefactBytes}
	if u.max <= 0 {
		u.max = DefaultMaxArtefactBytes
	}
	v, err := update.ParseVersion(cfg.Current)
	switch {
	case update.IsDevBuild(cfg.Current) || err != nil:
		if u.cfg.Delegation == update.DelegationNone {
			u.cfg.Delegation = update.DelegationDevBuild
		}
	default:
		u.current = v
	}
	u.status = update.Status{State: update.StateIdle, Current: cfg.Current, Delegation: u.cfg.Delegation}
	return u
}

// Current implements port.Updater.
func (u *Updater) Current() string { return u.cfg.Current }

// Delegation implements port.Updater.
func (u *Updater) Delegation() update.Delegation { return u.cfg.Delegation }

// Status implements port.Updater.
func (u *Updater) Status() update.Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.snapshot()
}

// snapshot copies the status; the caller holds mu.
func (u *Updater) snapshot() update.Status {
	st := u.status
	if st.Available != nil {
		r := *st.Available
		st.Available = &r
	}
	if st.Progress != nil {
		p := *st.Progress
		st.Progress = &p
	}
	return st
}

// set changes the status under the lock and tells the observer after releasing
// it. fn edits the status.
func (u *Updater) set(fn func(*update.Status)) {
	u.mu.Lock()
	fn(&u.status)
	st := u.snapshot()
	u.mu.Unlock()
	if u.cfg.Observer != nil {
		u.cfg.Observer(st)
	}
}

// begin claims the engine for one operation.
func (u *Updater) begin() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cfg.Delegation != update.DelegationNone {
		return update.New(update.CodeDelegated, "")
	}
	// restarting: the new version is already handed over; nothing more may start.
	if u.busy || u.status.State == update.StateRestarting {
		return update.New(update.CodeBusy, "")
	}
	u.busy = true
	return nil
}

func (u *Updater) end() {
	u.mu.Lock()
	u.busy = false
	u.mu.Unlock()
}

// Check implements port.Updater.
func (u *Updater) Check(ctx context.Context, channel update.Channel) (*update.Release, error) {
	if _, ok := update.ParseChannel(string(channel)); !ok {
		return nil, update.New(update.CodeInvalid, "channel")
	}
	if err := u.begin(); err != nil {
		return nil, err
	}
	defer u.end()
	u.set(func(s *update.Status) { s.State, s.Error = update.StateChecking, "" })

	cand, err := u.cfg.Feed.Latest(ctx, u.current, channel)
	if err != nil {
		ue := classify(ctx, err, update.CodeCheckFailed)
		u.cfg.Logger.Warn("update check failed", "code", ue.Code, "cause", ue.Err)
		// A failed look does not forget an update found earlier.
		u.set(func(s *update.Status) { s.State, s.Error = update.StateFailed, ue.Code })
		return nil, ue
	}
	if cand == nil {
		u.mu.Lock()
		u.pending = nil
		u.mu.Unlock()
		u.set(func(s *update.Status) { s.State, s.Available, s.Progress, s.Error = update.StateUpToDate, nil, nil, "" })
		return nil, nil
	}
	rel := cand.Release
	u.mu.Lock()
	u.pending = cand
	u.mu.Unlock()
	u.set(func(s *update.Status) {
		s.State, s.Available, s.Progress, s.Error = update.StateAvailable, &rel, nil, ""
	})
	out := rel
	return &out, nil
}

// Forget implements port.Updater.
func (u *Updater) Forget() {
	u.set(func(s *update.Status) {
		// An install under way (or already handed over) owns the candidate.
		if u.busy || s.State == update.StateRestarting {
			return
		}
		u.pending = nil
		s.Available, s.Progress = nil, nil
		if s.State == update.StateAvailable {
			s.State = update.StateIdle
		}
	})
}

// Install implements port.Updater.
func (u *Updater) Install(ctx context.Context) error {
	if err := u.begin(); err != nil {
		return err
	}
	defer u.end()

	u.mu.Lock()
	cand := u.pending
	u.mu.Unlock()
	if cand == nil {
		return update.New(update.CodeNothingToInstall, "")
	}

	fail := func(err error, fallback update.Code) error {
		ue := classify(ctx, err, fallback)
		u.cfg.Logger.Warn("update install failed", "version", cand.Release.Version, "code", ue.Code, "cause", ue.Err)
		u.set(func(s *update.Status) { s.State, s.Progress, s.Error = update.StateFailed, nil, ue.Code })
		return ue
	}

	dir, err := u.newStagingDir()
	if err != nil {
		return fail(err, update.CodeDownloadFailed)
	}
	// Whatever happens, this process's own copy of the artefact is not needed
	// once the Backend has taken it (or the install has failed).
	defer func() { _ = os.RemoveAll(dir) }()

	path, err := u.download(ctx, cand, dir)
	if err != nil {
		return fail(err, update.CodeDownloadFailed)
	}

	// The checksum is compared again on the file as it now sits on disk, so
	// what the Backend unpacks is what was verified, not what was streamed.
	u.set(func(s *update.Status) { s.State, s.Progress = update.StateVerifying, nil })
	if err := verifyFile(path, cand.SHA256); err != nil {
		return fail(err, update.CodeChecksumMismatch)
	}

	art := Artefact{Path: path, Name: cand.Asset, Version: cand.Release.Version, SHA256: cand.SHA256}
	u.set(func(s *update.Status) { s.State = update.StateInstalling })
	if err := u.cfg.Backend.Stage(ctx, art); err != nil {
		return fail(u.rollback(ctx, err), update.CodeInstallFailed)
	}

	u.set(func(s *update.Status) { s.State = update.StateRestarting })
	if err := u.cfg.Backend.Apply(ctx); err != nil {
		return fail(u.rollback(ctx, err), update.CodeInstallFailed)
	}
	return nil
}

// rollback undoes a failed install: the Backend drops what it staged. When that
// fails too the result is update.CodeRollbackFailed, which tells the user the
// installation may need to be reinstalled by hand.
func (u *Updater) rollback(ctx context.Context, cause error) error {
	// A canceled context must not stop the cleanup.
	if err := u.cfg.Backend.Discard(context.WithoutCancel(ctx)); err != nil {
		return update.Wrap(update.CodeRollbackFailed, errors.Join(cause, err))
	}
	return cause
}

// classify turns an error into an *update.Error: one that already is classified
// stays, cancellation is its own code, anything else gets the fallback.
func classify(ctx context.Context, err error, fallback update.Code) *update.Error {
	// Cancellation wins over whatever the interrupted step reported.
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return update.Wrap(update.CodeCanceled, err)
	}
	var ue *update.Error
	if errors.As(err, &ue) {
		return ue
	}
	switch {
	case errors.Is(err, ErrDigestMismatch):
		return update.Wrap(update.CodeChecksumMismatch, err)
	case errors.Is(err, ErrKeyMissing):
		return update.Wrap(update.CodeKeyMissing, err)
	}
	return update.Wrap(fallback, err)
}

func (u *Updater) newStagingDir() (string, error) {
	if u.cfg.StagingDir == "" {
		return "", errors.New("updater: no staging directory configured")
	}
	if err := os.MkdirAll(u.cfg.StagingDir, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(u.cfg.StagingDir, stagingPrefix+"*")
}

// stagingPrefix names the per-install directories inside StagingDir.
const stagingPrefix = "update-"

// CleanStaging removes the downloads an earlier process left behind (it was
// killed mid-download). Call it once at start-up, before any Check.
func (u *Updater) CleanStaging() error {
	if u.cfg.StagingDir == "" {
		return nil
	}
	entries, err := os.ReadDir(u.cfg.StagingDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), stagingPrefix) {
			if err := os.RemoveAll(filepath.Join(u.cfg.StagingDir, e.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// download streams the artefact into dir, hashing it on the way, and fails on a
// wrong digest before the file is ever handed on.
func (u *Updater) download(ctx context.Context, c *Candidate, dir string) (string, error) {
	u.set(func(s *update.Status) {
		s.State, s.Error = update.StateDownloading, ""
		s.Progress = &update.Progress{Total: c.Release.Size}
	})
	// A download has no overall time limit (its length depends on the
	// connection) but must not hang: the guard cancels it when no byte arrives
	// for StallTimeout.
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard := newStallGuard(u.stallTimeout(), cancel)
	defer guard.stop()

	body, size, err := u.cfg.Feed.Open(dctx, c)
	if err != nil {
		return "", guard.explain(ctx, err)
	}
	defer func() { _ = body.Close() }()
	if size > u.max {
		return "", update.Wrap(update.CodeDownloadFailed, fmt.Errorf("updater: the artefact announces %d bytes, more than the %d allowed", size, u.max))
	}
	total := size
	if total <= 0 {
		total = c.Release.Size
	}

	path := filepath.Join(dir, c.Asset)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{w: io.MultiWriter(f, h), total: total, report: func(written, total int64) {
		u.set(func(s *update.Status) { s.Progress = &update.Progress{Written: written, Total: total} })
	}}
	n, copyErr := io.Copy(pw, io.LimitReader(guard.wrap(ctxReader{dctx, body}), u.max+1))
	closeErr := f.Close()
	if copyErr != nil {
		return "", guard.explain(ctx, copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > u.max {
		return "", update.Wrap(update.CodeDownloadFailed, fmt.Errorf("updater: the artefact is larger than the %d bytes allowed", u.max))
	}
	var got [sha256.Size]byte
	copy(got[:], h.Sum(nil))
	if !digestEqual(got, c.SHA256) {
		return "", ErrDigestMismatch
	}
	return path, nil
}

func (u *Updater) stallTimeout() time.Duration {
	if u.cfg.StallTimeout > 0 {
		return u.cfg.StallTimeout
	}
	return DefaultStallTimeout
}

// stallGuard cancels a download that stopped delivering bytes.
type stallGuard struct {
	timer   *time.Timer
	d       time.Duration
	stalled atomic.Bool
}

func newStallGuard(d time.Duration, cancel context.CancelFunc) *stallGuard {
	g := &stallGuard{d: d}
	g.timer = time.AfterFunc(d, func() {
		g.stalled.Store(true)
		cancel()
	})
	return g
}

func (g *stallGuard) stop() { g.timer.Stop() }

// wrap restarts the timer on every read that delivers bytes.
func (g *stallGuard) wrap(r io.Reader) io.Reader { return stallReader{g, r} }

type stallReader struct {
	g *stallGuard
	r io.Reader
}

func (s stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.g.timer.Reset(s.g.d)
	}
	return n, err
}

// explain turns the error of a download cut short by the guard into a download
// failure: the cancellation was ours, not the caller's.
func (g *stallGuard) explain(parent context.Context, err error) error {
	if g.stalled.Load() && parent.Err() == nil {
		return update.Wrap(update.CodeDownloadFailed, fmt.Errorf("updater: the download stalled: %v", err))
	}
	return err
}

// verifyFile hashes the file at path and compares it with want.
func verifyFile(path string, want [sha256.Size]byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	var got [sha256.Size]byte
	copy(got[:], h.Sum(nil))
	if !digestEqual(got, want) {
		return ErrDigestMismatch
	}
	return nil
}

// ctxReader makes a read stop when the context ends, even if the body does not
// notice.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// progressWriter reports how much was written, at most every progressEvery.
type progressWriter struct {
	w       io.Writer
	total   int64
	written int64
	last    time.Time
	report  func(written, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	if now := time.Now(); now.Sub(p.last) >= progressEvery {
		p.last = now
		p.report(p.written, p.total)
	}
	return n, err
}
