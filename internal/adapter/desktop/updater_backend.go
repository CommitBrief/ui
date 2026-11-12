// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	wupdater "github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/CommitBrief/ui/internal/adapter/updater"
)

// providerName names the staged-file provider in the Wails updater's logs and
// events.
const providerName = "commitbrief-staged"

// wailsStagingPrefix is the prefix of the directories the Wails updater stages
// into, next to the install target.
const wailsStagingPrefix = "wails-update-"

// UpdateBackend is the updater.Backend on top of the Wails v3 updater. By the time
// Stage runs the artefact has been checked: the file hashes to its line in the
// release's checksums.txt. This type hands that file to the Wails updater through a provider that serves the staged file, so
// the framework does what it is good at (unpacking the archive with zip-slip and
// symlink checks, validating the payload, and, after this process has quit, the
// swap with a backup that is restored when the swap or the first launch fails)
// and nothing in it ever talks to the network.
//
// The Wails updater also compares the digest again while it copies, with the
// same one from checksums.txt; no public key is configured on it (there is no
// signature to check).
type UpdateBackend struct {
	current string
	log     *slog.Logger

	mu       sync.Mutex
	provider *stagedProvider
	ready    bool // Stage succeeded and nothing has been applied or discarded since
}

var _ updater.Backend = (*UpdateBackend)(nil)

// NewUpdateBackend returns the backend. current is the running version ("1.2.3",
// no "v"). Nothing touches Wails until the first Stage.
func NewUpdateBackend(current string, log *slog.Logger) *UpdateBackend {
	if log == nil {
		log = slog.Default()
	}
	return &UpdateBackend{current: current, log: log, provider: &stagedProvider{}}
}

// updaterHandle is the part of the Wails updater this type uses; the real one is
// application.Get().Updater.
type updaterHandle interface {
	Init(cfg wupdater.Config) error
	Check(ctx context.Context) (*wupdater.Release, error)
	DownloadAndInstall(ctx context.Context) error
	Restart(ctx context.Context) error
	DownloadedPath() string
}

func (b *UpdateBackend) handle() (updaterHandle, error) {
	app := application.Get()
	if app == nil || app.Updater == nil {
		return nil, errors.New("the desktop application is not running")
	}
	return app.Updater, nil
}

// initOnce configures the Wails updater the first time it is needed.
func (b *UpdateBackend) initOnce(h updaterHandle) error {
	err := h.Init(wupdater.Config{
		CurrentVersion: b.current,
		Providers:      []wupdater.Provider{b.provider},
		// Headless: our own Updates screen is the only interface.
		Window: wupdater.WindowNone,
	})
	if err != nil && !errors.Is(err, wupdater.ErrAlreadyConfigured) {
		return err
	}
	return nil
}

// Stage implements updater.Backend.
func (b *UpdateBackend) Stage(ctx context.Context, a updater.Artefact) error {
	h, err := b.handle()
	if err != nil {
		return err
	}
	return b.stage(ctx, h, a)
}

func (b *UpdateBackend) stage(ctx context.Context, h updaterHandle, a updater.Artefact) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.initOnce(h); err != nil {
		return fmt.Errorf("configuring the updater: %w", err)
	}
	b.ready = false
	b.provider.set(a)
	rel, err := h.Check(ctx)
	if err != nil {
		return fmt.Errorf("preparing the update: %w", err)
	}
	if rel == nil {
		return errors.New("the update provider reported no release")
	}
	if err := h.DownloadAndInstall(ctx); err != nil {
		return fmt.Errorf("staging the update: %w", err)
	}
	b.ready = true
	return nil
}

// Apply implements updater.Backend: the Wails helper takes over, this process
// is asked to quit, and the helper swaps the payload in once it has exited.
func (b *UpdateBackend) Apply(ctx context.Context) error {
	h, err := b.handle()
	if err != nil {
		return err
	}
	return b.apply(ctx, h)
}

func (b *UpdateBackend) apply(ctx context.Context, h updaterHandle) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ready {
		return errors.New("no update has been staged")
	}
	if err := h.Restart(ctx); err != nil {
		return fmt.Errorf("handing the update to the restart helper: %w", err)
	}
	return nil
}

// Discard implements updater.Backend: it removes the staged payload. The running
// installation was never touched (the swap happens only after Apply has handed
// over and this process has quit), so dropping the staging is the whole
// rollback; a swap that fails after the quit is rolled back by the Wails helper
// itself from the backup it takes first.
func (b *UpdateBackend) Discard(context.Context) error {
	h, err := b.handle()
	if err != nil {
		return err
	}
	return b.discard(h)
}

func (b *UpdateBackend) discard(h updaterHandle) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ready = false
	b.provider.clear()
	dir := wailsStagingDir(h.DownloadedPath())
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing the staged update: %w", err)
	}
	return nil
}

// wailsStagingDir returns the directory the Wails updater staged payload into,
// or "" when there is nothing to remove. It only ever names a directory the
// updater itself created (by its prefix), so a wrong path never becomes a
// recursive delete of something else.
func wailsStagingDir(payload string) string {
	if payload == "" || filepath.Clean(payload) != payload {
		return ""
	}
	dir := filepath.Dir(payload)
	if !strings.HasPrefix(filepath.Base(dir), wailsStagingPrefix) {
		return ""
	}
	return dir
}

// stagedProvider is the Wails update provider that serves the one artefact the
// engine already downloaded and verified.
type stagedProvider struct {
	mu    sync.Mutex
	art   updater.Artefact
	isSet bool
}

var _ wupdater.Provider = (*stagedProvider)(nil)

func (p *stagedProvider) Name() string { return providerName }

func (p *stagedProvider) set(a updater.Artefact) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.art, p.isSet = a, true
}

func (p *stagedProvider) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.art, p.isSet = updater.Artefact{}, false
}

func (p *stagedProvider) get() (updater.Artefact, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.art, p.isSet
}

// Check reports the staged artefact as the available release, with the
// digest the Wails updater compares while it copies.
func (p *stagedProvider) Check(_ context.Context, _ wupdater.CheckRequest) (*wupdater.Release, error) {
	a, ok := p.get()
	if !ok {
		return nil, errors.New("no staged artefact")
	}
	info, err := os.Stat(a.Path)
	if err != nil {
		return nil, err
	}
	digest := make([]byte, len(a.SHA256))
	copy(digest, a.SHA256[:])
	return &wupdater.Release{
		Version:  a.Version,
		Artifact: wupdater.Artifact{Filename: a.Name, Size: info.Size()},
		Verification: &wupdater.Verification{
			DigestAlgo: "sha256",
			Digest:     digest,
		},
	}, nil
}

// Download copies the staged file to dst.
func (p *stagedProvider) Download(ctx context.Context, _ *wupdater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	a, ok := p.get()
	if !ok {
		return errors.New("no staged artefact")
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var written int64
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			onProgress(written, info.Size())
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
