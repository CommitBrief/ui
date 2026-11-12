// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wupdater "github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/adapter/updater"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/update"
	"github.com/CommitBrief/ui/internal/port"
)

// fakeWails is the Wails updater stand-in: it drives the provider the way the
// real one does and records what it was asked.
type fakeWails struct {
	cfg       wupdater.Config
	initErr   error
	checkErr  error
	installFn func(p wupdater.Provider) error
	restart   error
	path      string
	calls     []string
	copied    []byte
	release   *wupdater.Release
}

func (f *fakeWails) Init(cfg wupdater.Config) error {
	f.calls = append(f.calls, "init")
	if f.initErr == nil && f.cfg.Providers != nil {
		return wupdater.ErrAlreadyConfigured
	}
	if f.initErr == nil {
		f.cfg = cfg
	}
	return f.initErr
}

func (f *fakeWails) Check(ctx context.Context) (*wupdater.Release, error) {
	f.calls = append(f.calls, "check")
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	rel, err := f.cfg.Providers[0].Check(ctx, wupdater.CheckRequest{})
	f.release = rel
	return rel, err
}

func (f *fakeWails) DownloadAndInstall(ctx context.Context) error {
	f.calls = append(f.calls, "download")
	if f.installFn != nil {
		return f.installFn(f.cfg.Providers[0])
	}
	var buf bytes.Buffer
	if err := f.cfg.Providers[0].Download(ctx, f.release, &buf, func(int64, int64) {}); err != nil {
		return err
	}
	f.copied = buf.Bytes()
	return nil
}

func (f *fakeWails) Restart(context.Context) error {
	f.calls = append(f.calls, "restart")
	return f.restart
}

func (f *fakeWails) DownloadedPath() string { return f.path }

func artefactFile(t *testing.T, payload []byte) updater.Artefact {
	t.Helper()
	p := filepath.Join(t.TempDir(), "commitbrief-ui_0.2.0_linux_amd64.AppImage")
	if err := os.WriteFile(p, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return updater.Artefact{Path: p, Name: filepath.Base(p), Version: "0.2.0", SHA256: sha256.Sum256(payload)}
}

func TestUpdateBackendStagesTheVerifiedFileThroughTheWailsUpdater(t *testing.T) {
	payload := bytes.Repeat([]byte("app"), 100000)
	art := artefactFile(t, payload)
	b := NewUpdateBackend("0.1.0", nil)
	f := &fakeWails{}

	if err := b.stage(context.Background(), f, art); err != nil {
		t.Fatal(err)
	}
	// The Wails updater is configured once, headless, with no provider but ours.
	if f.cfg.CurrentVersion != "0.1.0" || len(f.cfg.Providers) != 1 || f.cfg.Providers[0].Name() != providerName {
		t.Errorf("config = %+v", f.cfg)
	}
	if f.cfg.Window != wupdater.WindowNone {
		t.Error("the Wails update window must stay off")
	}
	if len(f.cfg.PublicKey) != 0 {
		t.Error("the Wails updater must not be given a trust anchor of its own: there is no signature, the digest is checked on checksums.txt before staging")
	}
	// What the Wails updater is told to verify is the digest from checksums.txt.
	v := f.release.Verification
	if v == nil || v.DigestAlgo != "sha256" || !bytes.Equal(v.Digest, art.SHA256[:]) || len(v.Signature) != 0 {
		t.Errorf("verification = %+v", v)
	}
	if f.release.Version != "0.2.0" || f.release.Artifact.Filename != art.Name || f.release.Artifact.Size != int64(len(payload)) {
		t.Errorf("release = %+v", f.release)
	}
	if !bytes.Equal(f.copied, payload) {
		t.Error("the provider did not serve the staged file")
	}

	// A second stage (a retry) does not configure the updater twice.
	if err := b.stage(context.Background(), f, art); err != nil {
		t.Fatal(err)
	}
	inits := 0
	for _, c := range f.calls {
		if c == "init" {
			inits++
		}
	}
	if inits != 2 || f.cfg.Providers == nil {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestUpdateBackendStageFailures(t *testing.T) {
	art := artefactFile(t, []byte("x"))
	boom := errors.New("boom")
	tests := map[string]*fakeWails{
		"init":     {initErr: boom},
		"check":    {checkErr: boom},
		"download": {installFn: func(wupdater.Provider) error { return boom }},
	}
	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			b := NewUpdateBackend("0.1.0", nil)
			if err := b.stage(context.Background(), f, art); err == nil {
				t.Fatal("a failure was swallowed")
			}
			// Nothing staged, so nothing may be applied.
			if err := b.apply(context.Background(), f); err == nil {
				t.Error("apply went ahead after a failed stage")
			}
		})
	}

	// A file that vanished between verification and staging.
	b := NewUpdateBackend("0.1.0", nil)
	gone := art
	gone.Path = filepath.Join(t.TempDir(), "missing")
	if err := b.stage(context.Background(), &fakeWails{}, gone); err == nil {
		t.Error("a missing staged file was accepted")
	}
}

func TestUpdateBackendApplyHandsOverToTheRestartHelper(t *testing.T) {
	art := artefactFile(t, []byte("x"))
	b := NewUpdateBackend("0.1.0", nil)
	f := &fakeWails{}
	if err := b.apply(context.Background(), f); err == nil {
		t.Fatal("apply without a stage must fail")
	}
	if err := b.stage(context.Background(), f, art); err != nil {
		t.Fatal(err)
	}
	if err := b.apply(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if f.calls[len(f.calls)-1] != "restart" {
		t.Errorf("calls = %v", f.calls)
	}
	// A restart the helper refused is an error the engine rolls back from.
	f.restart = wupdater.ErrHelperNotReady
	if err := b.apply(context.Background(), f); err == nil {
		t.Error("a refused restart was reported as success")
	}
}

func TestUpdateBackendDiscardRemovesOnlyWailsStagingDirectories(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "wails-update-12345")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(staging, "commitbrief-ui")
	if err := os.WriteFile(payload, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	b := NewUpdateBackend("0.1.0", nil)
	f := &fakeWails{path: payload}
	if err := b.discard(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("the staged payload is still there: %v", err)
	}

	// A path that is not inside one of the updater's own directories is never
	// deleted, whatever it is.
	keep := filepath.Join(root, "precious")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keep, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.path = filepath.Join(keep, "data")
	if err := b.discard(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keep, "data")); err != nil {
		t.Errorf("a foreign directory was removed: %v", err)
	}
	f.path = ""
	if err := b.discard(f); err != nil {
		t.Errorf("nothing staged: %v", err)
	}
	// Discard also forgets the stage: nothing can be applied afterwards.
	if err := b.apply(context.Background(), f); err == nil {
		t.Error("apply after discard must fail")
	}
}

func TestUpdateWailsStagingDir(t *testing.T) {
	tests := map[string]string{
		"":                                    "",
		"/x/wails-update-1/app":               "/x/wails-update-1",
		"/x/other/app":                        "",
		"/x/wails-update-1":                   "",
		"/wails-update-1/..":                  "",
		"/x/not-wails-update-1/app":           "",
		"/x/wails-update-123/CommitBrief.app": "/x/wails-update-123",
	}
	for in, want := range tests {
		if got := wailsStagingDir(filepath.FromSlash(in)); got != filepath.FromSlash(want) {
			t.Errorf("wailsStagingDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpdateStagedProviderWithoutAnArtefact(t *testing.T) {
	p := &stagedProvider{}
	if _, err := p.Check(context.Background(), wupdater.CheckRequest{}); err == nil {
		t.Error("Check without an artefact")
	}
	if err := p.Download(context.Background(), nil, &bytes.Buffer{}, func(int64, int64) {}); err == nil {
		t.Error("Download without an artefact")
	}
	art := artefactFile(t, []byte("abc"))
	p.set(art)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Download(ctx, nil, &bytes.Buffer{}, func(int64, int64) {}); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled Download = %v", err)
	}
	p.clear()
	if _, err := p.Check(context.Background(), wupdater.CheckRequest{}); err == nil {
		t.Error("Check after clear")
	}
}

func TestUpdateEventNameMatchesTheApplication(t *testing.T) {
	if app.EventUpdateStatus != "updates:status" {
		t.Errorf("the event name changed to %q; update the RegisterEvent call in updates_service.go", app.EventUpdateStatus)
	}
}

type stubUpdater struct {
	checkErr error
}

func (s stubUpdater) Current() string               { return "0.1.0" }
func (s stubUpdater) Delegation() update.Delegation { return update.DelegationNone }
func (s stubUpdater) Status() update.Status {
	return update.Status{State: update.StateIdle, Current: "0.1.0"}
}
func (s stubUpdater) Check(context.Context, update.Channel) (*update.Release, error) {
	return nil, s.checkErr
}
func (s stubUpdater) Install(context.Context) error { return s.checkErr }
func (s stubUpdater) Forget()                       {}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func TestUpdateServiceShapesErrors(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "state", "ui.db"), sqlite.WithLogger(testLogger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	build := func(up port.Updater) *UpdatesService {
		return NewUpdatesService(app.NewUpdates(app.UpdatesDeps{Updater: up, Settings: store.Settings(), Clock: systemClock{}, Logger: testLogger}), testLogger)
	}
	ctx := context.Background()

	svc := build(stubUpdater{})
	v, err := svc.View(ctx)
	if err != nil || v.Channel != "stable" || !v.AutoCheck {
		t.Fatalf("View = %+v, %v", v, err)
	}
	v, err = svc.SetPreferences(ctx, "prerelease", false)
	if err != nil || v.Channel != "prerelease" || v.AutoCheck {
		t.Fatalf("SetPreferences = %+v, %v", v, err)
	}
	if _, err := svc.Check(ctx); err != nil {
		t.Errorf("Check = %v", err)
	}

	check := func(err error, code update.Code, field string) {
		t.Helper()
		var ue *update.Error
		if !errors.As(err, &ue) || ue.Code != code || ue.Field != field {
			t.Errorf("error = %v, want %s (%s)", err, code, field)
			return
		}
		if ue.Err != nil {
			t.Errorf("the cause crossed the boundary: %v", ue.Err)
		}
	}
	_, err = svc.SetPreferences(ctx, "nightly", true)
	check(err, update.CodeInvalid, "channel")

	// A classified failure keeps its code and loses its cause (paths, URLs).
	secret := errors.New("GET https://example.invalid/token=abc failed in /Users/me/.cache")
	cause := build(stubUpdater{checkErr: update.Wrap(update.CodeChecksumMismatch, secret)})
	_, err = cause.Check(ctx)
	check(err, update.CodeChecksumMismatch, "")
	if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "/Users") {
		t.Errorf("the cause leaked into the error text: %q", err.Error())
	}
	check(cause.Install(ctx), update.CodeChecksumMismatch, "")

	// Anything that is not classified is "internal".
	_, err = build(stubUpdater{checkErr: errors.New("disk on fire")}).Check(ctx)
	check(err, update.CodeInternal, "")

	// A failing store is "internal" too.
	_ = store.Close()
	_, err = svc.View(ctx)
	check(err, update.CodeInternal, "")
}
