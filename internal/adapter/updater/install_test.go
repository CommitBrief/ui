// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// fakeBackend is the sham installer: it records the calls, reads the staged
// file as the real one would and fails where told to.
type fakeBackend struct {
	mu         sync.Mutex
	calls      []string
	staged     []byte
	stagedName string
	stageErr   error
	applyErr   error
	discardErr error
}

func (b *fakeBackend) Stage(_ context.Context, a Artefact) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, "stage")
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return err
	}
	b.staged, b.stagedName = data, a.Name
	return b.stageErr
}

func (b *fakeBackend) Apply(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, "apply")
	return b.applyErr
}

func (b *fakeBackend) Discard(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, "discard")
	return b.discardErr
}

func (b *fakeBackend) callList() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.calls, ",")
}

type harness struct {
	t       *testing.T
	feed    *testFeed
	backend *fakeBackend
	up      *Updater
	staging string

	mu     sync.Mutex
	states []update.State
}

func (h *harness) seen() []update.State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]update.State(nil), h.states...)
}

// newHarness serves one signed release v0.2.0 with payload and builds an
// updater running 0.1.0 against it.
func newHarness(t *testing.T, payload []byte, mutate func(r *testRelease)) *harness {
	t.Helper()
	rel := newRelease(t, "v0.2.0", payload)
	if mutate != nil {
		mutate(rel)
	}
	h := &harness{t: t, feed: newTestFeed(t, rel), backend: &fakeBackend{}, staging: filepath.Join(t.TempDir(), "updates")}
	h.up = New(Config{
		Feed:       h.feed.source(),
		Backend:    h.backend,
		Current:    "0.1.0",
		StagingDir: h.staging,
		Observer: func(s update.Status) {
			h.mu.Lock()
			if n := len(h.states); n == 0 || h.states[n-1] != s.State {
				h.states = append(h.states, s.State)
			}
			h.mu.Unlock()
		},
	})
	return h
}

func (h *harness) stagingEntries() []string {
	entries, err := os.ReadDir(h.staging)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestUpdateInstallValidChecksum(t *testing.T) {
	payload := []byte(strings.Repeat("new application bytes ", 5000))
	h := newHarness(t, payload, nil)
	ctx := context.Background()

	rel, err := h.up.Check(ctx, update.ChannelStable)
	if err != nil || rel == nil || rel.Version != "0.2.0" {
		t.Fatalf("Check = %+v, %v", rel, err)
	}
	if st := h.up.Status(); st.State != update.StateAvailable || st.Available == nil || st.Current != "0.1.0" {
		t.Fatalf("status after check = %+v", st)
	}

	if err := h.up.Install(ctx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := h.backend.callList(); got != "stage,apply" {
		t.Errorf("backend calls = %q, want stage,apply", got)
	}
	if string(h.backend.staged) != string(payload) || h.backend.stagedName != "commitbrief-ui_0.2.0_linux_amd64.AppImage" {
		t.Errorf("the backend was handed %q (%d bytes)", h.backend.stagedName, len(h.backend.staged))
	}
	if e := h.stagingEntries(); len(e) != 0 {
		t.Errorf("staging left behind: %v", e)
	}
	want := []update.State{update.StateChecking, update.StateAvailable, update.StateDownloading, update.StateVerifying, update.StateInstalling, update.StateRestarting}
	if got := h.seen(); !equalStates(got, want) {
		t.Errorf("states = %v, want %v", got, want)
	}
	// Once the restart is handed over nothing else may start.
	if err := h.up.Install(ctx); codeOf(err) != update.CodeBusy {
		t.Errorf("a second Install while restarting: %v", err)
	}
	if _, err := h.up.Check(ctx, update.ChannelStable); codeOf(err) != update.CodeBusy {
		t.Errorf("a Check while restarting: %v", err)
	}
}

func equalStates(a, b []update.State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUpdateInstallRejectsAnArtefactThatDoesNotMatchItsChecksum(t *testing.T) {
	h := newHarness(t, []byte("the genuine artefact"), nil)
	ctx := context.Background()
	if rel, err := h.up.Check(ctx, update.ChannelStable); err != nil || rel == nil {
		t.Fatalf("Check = %v, %v", rel, err)
	}
	// After the check passed, the server swaps the artefact for another one of
	// the same size (a compromised mirror).
	name := artefactFor(t, "v0.2.0")
	h.feed.mu.Lock()
	h.feed.releases[0].files[name] = []byte("the substituted payload")
	h.feed.mu.Unlock()

	err := h.up.Install(ctx)
	if codeOf(err) != update.CodeChecksumMismatch {
		t.Fatalf("Install = %v, want %q", err, update.CodeChecksumMismatch)
	}
	if got := h.backend.callList(); got != "" {
		t.Errorf("the backend was reached with a bad artefact: %q", got)
	}
	if e := h.stagingEntries(); len(e) != 0 {
		t.Errorf("the rejected artefact was left on disk: %v", e)
	}
	st := h.up.Status()
	if st.State != update.StateFailed || st.Error != update.CodeChecksumMismatch || st.Current != "0.1.0" {
		t.Errorf("status = %+v", st)
	}
}

func TestUpdateInstallRejectsAnArtefactSwappedBeforeTheCheck(t *testing.T) {
	// The release list says the artefact is the right size, checksums.txt
	// is for the genuine file, and the server serves something else.
	genuine := []byte("0123456789")
	h := newHarness(t, genuine, func(r *testRelease) {
		r.files[artefactFor(t, "v0.2.0")] = []byte("9876543210")
		r.listedSize = map[string]int64{artefactFor(t, "v0.2.0"): int64(len(genuine))}
	})
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	if err := h.up.Install(ctx); codeOf(err) != update.CodeChecksumMismatch {
		t.Errorf("Install = %v", err)
	}
	if h.backend.callList() != "" {
		t.Error("the backend was reached")
	}
}

func TestUpdateCheckRejectsAReleaseWithoutUsableChecksums(t *testing.T) {
	cases := map[string]func(r *testRelease){
		"no checksums.txt": func(r *testRelease) { delete(r.files, ChecksumsName) },
		"artefact not listed": func(r *testRelease) {
			r.files[ChecksumsName] = []byte(strings.Repeat("a", 64) + "  something_else.zip\n")
		},
		"malformed checksums": func(r *testRelease) { r.files[ChecksumsName] = []byte("not a checksums file\n") },
		"empty checksums":     func(r *testRelease) { r.files[ChecksumsName] = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, []byte("app"), mutate)
			rel, err := h.up.Check(context.Background(), update.ChannelStable)
			if rel != nil || codeOf(err) != update.CodeChecksumMissing {
				t.Fatalf("Check = %+v, %v; want %q", rel, err, update.CodeChecksumMissing)
			}
			if st := h.up.Status(); st.State != update.StateFailed || st.Error != update.CodeChecksumMissing || st.Available != nil {
				t.Errorf("status = %+v", st)
			}
			// Nothing was found, so nothing can be installed.
			if err := h.up.Install(context.Background()); codeOf(err) != update.CodeNothingToInstall {
				t.Errorf("Install = %v", err)
			}
			if h.backend.callList() != "" {
				t.Error("the backend was reached")
			}
		})
	}
}

func TestUpdateInstallRollsBackWhenTheInstallerFails(t *testing.T) {
	tests := []struct {
		name       string
		stageErr   error
		applyErr   error
		discardErr error
		wantCode   update.Code
		wantCalls  string
	}{
		{"stage fails", errors.New("unpack failed"), nil, nil, update.CodeInstallFailed, "stage,discard"},
		{"apply fails", nil, errors.New("helper did not start"), nil, update.CodeInstallFailed, "stage,apply,discard"},
		{"stage fails and the rollback fails too", errors.New("unpack failed"), nil, errors.New("cannot clean"), update.CodeRollbackFailed, "stage,discard"},
		{"apply fails and the rollback fails too", nil, errors.New("swap failed"), errors.New("cannot restore"), update.CodeRollbackFailed, "stage,apply,discard"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []byte("app payload"), nil)
			h.backend.stageErr, h.backend.applyErr, h.backend.discardErr = tc.stageErr, tc.applyErr, tc.discardErr
			ctx := context.Background()
			if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
				t.Fatal(err)
			}
			err := h.up.Install(ctx)
			if codeOf(err) != tc.wantCode {
				t.Fatalf("Install = %v, want %q", err, tc.wantCode)
			}
			if got := h.backend.callList(); got != tc.wantCalls {
				t.Errorf("calls = %q, want %q", got, tc.wantCalls)
			}
			if e := h.stagingEntries(); len(e) != 0 {
				t.Errorf("staging left behind: %v", e)
			}
			st := h.up.Status()
			if st.State != update.StateFailed || st.Error != tc.wantCode || st.Current != "0.1.0" {
				t.Errorf("status = %+v", st)
			}
			// The update is still on offer, so the user can try again.
			if st.Available == nil {
				t.Error("the pending release was forgotten after a failed install")
			}
			// The cause stays in the error chain for the log.
			var ue *update.Error
			if !errors.As(err, &ue) || ue.Err == nil {
				t.Error("the cause was lost")
			}
		})
	}
}

func TestUpdateInstallCanBeRetriedAfterAFailure(t *testing.T) {
	h := newHarness(t, []byte("app payload"), nil)
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	h.backend.applyErr = errors.New("transient")
	if err := h.up.Install(ctx); codeOf(err) != update.CodeInstallFailed {
		t.Fatalf("first Install = %v", err)
	}
	h.backend.applyErr = nil
	if err := h.up.Install(ctx); err != nil {
		t.Fatalf("retry = %v", err)
	}
	if st := h.up.Status(); st.State != update.StateRestarting || st.Error != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestUpdateInstallWithoutACheck(t *testing.T) {
	h := newHarness(t, []byte("x"), nil)
	if err := h.up.Install(context.Background()); codeOf(err) != update.CodeNothingToInstall {
		t.Errorf("Install = %v", err)
	}
	// Up to date drops a pending release.
	h2 := newHarness(t, []byte("x"), nil)
	h2.feed.releases = []*testRelease{newRelease(t, "v0.1.0", []byte("same"))}
	h2.up = New(Config{Feed: h2.feed.source(), Backend: h2.backend, Current: "0.1.0", StagingDir: h2.staging})
	if rel, err := h2.up.Check(context.Background(), update.ChannelStable); rel != nil || err != nil {
		t.Fatalf("Check = %v, %v", rel, err)
	}
	if st := h2.up.Status(); st.State != update.StateUpToDate || st.Available != nil {
		t.Errorf("status = %+v", st)
	}
}

func TestDelegatedInstallationsDoNotUpdate(t *testing.T) {
	feed := newTestFeed(t, newRelease(t, "v0.2.0", []byte("x")))
	cases := map[string]Config{
		"package manager": {Current: "0.1.0", Delegation: update.DelegationPackageManager},
		"development":     {Current: update.DevVersion},
		"empty version":   {Current: ""},
		"not a version":   {Current: "banana"},
		"dev suffix":      {Current: "0.3.0-dev"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			cfg.Feed, cfg.Backend, cfg.StagingDir = feed.source(), &fakeBackend{}, t.TempDir()
			up := New(cfg)
			if up.Delegation() == update.DelegationNone {
				t.Fatal("not delegated")
			}
			if up.Status().Delegation != up.Delegation() {
				t.Error("the status does not carry the delegation")
			}
			before := feed.requests.Load()
			if _, err := up.Check(context.Background(), update.ChannelStable); codeOf(err) != update.CodeDelegated {
				t.Errorf("Check = %v", err)
			}
			if err := up.Install(context.Background()); codeOf(err) != update.CodeDelegated {
				t.Errorf("Install = %v", err)
			}
			if feed.requests.Load() != before {
				t.Error("a delegated installation talked to the network")
			}
		})
	}
}

func TestUpdateCheckRejectsAnUnknownChannel(t *testing.T) {
	h := newHarness(t, []byte("x"), nil)
	if _, err := h.up.Check(context.Background(), update.Channel("nightly")); codeOf(err) != update.CodeInvalid {
		t.Errorf("Check = %v", err)
	}
}

func TestUpdateAFailedCheckKeepsAnEarlierFind(t *testing.T) {
	h := newHarness(t, []byte("app"), nil)
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		http.Error(w, "down", http.StatusBadGateway)
		return true
	}
	if _, err := h.up.Check(ctx, update.ChannelStable); codeOf(err) != update.CodeCheckFailed {
		t.Fatalf("Check = %v", err)
	}
	st := h.up.Status()
	if st.State != update.StateFailed || st.Available == nil {
		t.Errorf("status = %+v; the earlier find must survive a failed look", st)
	}
	h.feed.hook = nil
	if err := h.up.Install(ctx); err != nil {
		t.Errorf("Install after a failed check = %v", err)
	}
}

func TestUpdateInstallIsBusyWhileDownloading(t *testing.T) {
	h := newHarness(t, []byte(strings.Repeat("x", 1<<16)), nil)
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, ".AppImage") {
			close(started)
			<-release
		}
		return false
	}
	done := make(chan error, 1)
	go func() { done <- h.up.Install(ctx) }()
	<-started
	if err := h.up.Install(ctx); codeOf(err) != update.CodeBusy {
		t.Errorf("a second Install: %v", err)
	}
	if _, err := h.up.Check(ctx, update.ChannelStable); codeOf(err) != update.CodeBusy {
		t.Errorf("a Check during the install: %v", err)
	}
	// feed.mu is held by the handler while it waits; unblock it.
	h.feed.hook = nil
	close(release)
	if err := <-done; err != nil {
		t.Errorf("the first Install: %v", err)
	}
}

func TestUpdateInstallCanceledMidDownload(t *testing.T) {
	h := newHarness(t, []byte(strings.Repeat("x", 1<<16)), nil)
	if _, err := h.up.Check(context.Background(), update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, ".AppImage") {
			w.Header().Set("Content-Length", "65536")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			return true
		}
		return false
	}
	go func() { <-started; cancel() }()
	err := h.up.Install(ctx)
	if codeOf(err) != update.CodeCanceled {
		t.Fatalf("Install = %v, want %q", err, update.CodeCanceled)
	}
	if h.backend.callList() != "" {
		t.Error("the backend was reached")
	}
	if e := h.stagingEntries(); len(e) != 0 {
		t.Errorf("staging left behind: %v", e)
	}
}

func TestUpdateStalledDownloadIsAbandoned(t *testing.T) {
	h := newHarness(t, []byte(strings.Repeat("x", 1<<16)), nil)
	h.up.cfg.StallTimeout = 150 * time.Millisecond
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, ".AppImage") {
			w.Header().Set("Content-Length", "65536")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("some bytes, then nothing"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return true
		}
		return false
	}
	done := make(chan error, 1)
	go func() { done <- h.up.Install(ctx) }()
	select {
	case err := <-done:
		// Not "canceled": the user did not cancel, the connection stalled.
		if codeOf(err) != update.CodeDownloadFailed {
			t.Errorf("Install = %v, want %q", err, update.CodeDownloadFailed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled download was never abandoned")
	}
	if e := h.stagingEntries(); len(e) != 0 {
		t.Errorf("staging left behind: %v", e)
	}
	if h.backend.callList() != "" {
		t.Error("the backend was reached")
	}
}

func TestUpdateDownloadSizeCap(t *testing.T) {
	payload := []byte(strings.Repeat("y", 4096))
	build := func(t *testing.T, listed int64, hook func(w http.ResponseWriter, r *http.Request) bool) (*Updater, *testFeed, string) {
		rel := newRelease(t, "v0.2.0", payload)
		if listed > 0 {
			rel.listedSize = map[string]int64{artefactFor(t, "v0.2.0"): listed}
		}
		feed := newTestFeed(t, rel)
		feed.hook = hook
		staging := t.TempDir()
		up := New(Config{Feed: feed.source(), Backend: &fakeBackend{}, Current: "0.1.0", StagingDir: staging, MaxArtefactBytes: 1024})
		return up, feed, staging
	}

	t.Run("announced size over the cap", func(t *testing.T) {
		up, _, staging := build(t, 0, nil)
		// The release list is accepted (the cap here is smaller than the payload,
		// but the list check uses the global bound), the announced Content-Length
		// is not.
		if _, err := up.Check(context.Background(), update.ChannelStable); err != nil {
			t.Fatal(err)
		}
		if err := up.Install(context.Background()); codeOf(err) != update.CodeDownloadFailed {
			t.Errorf("Install = %v", err)
		}
		if entries, _ := os.ReadDir(staging); len(entries) != 0 {
			t.Errorf("staging left behind: %v", entries)
		}
	})
	t.Run("stream longer than announced", func(t *testing.T) {
		up, _, staging := build(t, 0, func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, ".AppImage") {
				// No Content-Length: chunked, and longer than the cap.
				w.WriteHeader(http.StatusOK)
				for i := 0; i < 8; i++ {
					_, _ = w.Write(make([]byte, 512))
					w.(http.Flusher).Flush()
				}
				return true
			}
			return false
		})
		if _, err := up.Check(context.Background(), update.ChannelStable); err != nil {
			t.Fatal(err)
		}
		if err := up.Install(context.Background()); codeOf(err) != update.CodeDownloadFailed {
			t.Errorf("Install = %v", err)
		}
		if entries, _ := os.ReadDir(staging); len(entries) != 0 {
			t.Errorf("staging left behind: %v", entries)
		}
	})
}

func TestUpdateDownloadReportsProgress(t *testing.T) {
	payload := []byte(strings.Repeat("p", 3<<20))
	feed := newTestFeed(t, newRelease(t, "v0.2.0", payload))
	var mu sync.Mutex
	var last update.Progress
	up := New(Config{Feed: feed.source(), Backend: &fakeBackend{}, Current: "0.1.0", StagingDir: t.TempDir(),
		Observer: func(s update.Status) {
			if s.Progress != nil {
				mu.Lock()
				last = *s.Progress
				mu.Unlock()
			}
		}})
	if _, err := up.Check(context.Background(), update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	if err := up.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if last.Total != int64(len(payload)) {
		t.Errorf("progress total = %d, want %d", last.Total, len(payload))
	}
}

func TestUpdateCleanStaging(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "updates")
	for _, d := range []string{"update-abc", "update-def", "keep-me"} {
		if err := os.MkdirAll(filepath.Join(staging, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, "update-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	up := New(Config{Current: "0.1.0", StagingDir: staging})
	if err := up.CleanStaging(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(staging)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "keep-me,update-file" {
		t.Errorf("left %v; only the update-* directories go", names)
	}
	if err := New(Config{Current: "0.1.0", StagingDir: filepath.Join(staging, "missing")}).CleanStaging(); err != nil {
		t.Errorf("a missing staging directory is not an error: %v", err)
	}
	if err := New(Config{Current: "0.1.0"}).CleanStaging(); err != nil {
		t.Errorf("no staging directory configured: %v", err)
	}
}

func TestUpdateInstallWithoutAStagingDirectory(t *testing.T) {
	h := newHarness(t, []byte("x"), nil)
	h.up.cfg.StagingDir = ""
	if _, err := h.up.Check(context.Background(), update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	if err := h.up.Install(context.Background()); codeOf(err) != update.CodeDownloadFailed {
		t.Errorf("Install = %v", err)
	}
}

func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var want [32]byte
	if err := verifyFile(p, want); !errors.Is(err, ErrDigestMismatch) {
		t.Errorf("verifyFile = %v", err)
	}
	if err := verifyFile(filepath.Join(dir, "missing"), want); err == nil || errors.Is(err, ErrDigestMismatch) {
		t.Errorf("a missing file: %v", err)
	}
}

func TestUpdateStatusIsACopy(t *testing.T) {
	h := newHarness(t, []byte("x"), nil)
	if _, err := h.up.Check(context.Background(), update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	st := h.up.Status()
	st.Available.Version = "tampered"
	if h.up.Status().Available.Version == "tampered" {
		t.Error("Status exposes internal state")
	}
}

func TestUpdateObserverNeverRunsUnderTheLock(t *testing.T) {
	// The observer reads Status; if set held the lock while calling it, this
	// would deadlock.
	h := newHarness(t, []byte("x"), nil)
	done := make(chan struct{})
	var up *Updater
	up = New(Config{Feed: h.up.cfg.Feed, Backend: h.backend, Current: "0.1.0", StagingDir: h.staging,
		Observer: func(update.Status) { _ = up.Status() }})
	go func() {
		_, _ = up.Check(context.Background(), update.ChannelStable)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: the observer ran under the updater's lock")
	}
}

func TestUpdateCheckOnAnUnavailableFeedFailsAndSaysSo(t *testing.T) {
	h := newHarness(t, []byte("app"), nil)
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		http.NotFound(w, r)
		return true
	}
	rel, err := h.up.Check(context.Background(), update.ChannelStable)
	if rel != nil || codeOf(err) != update.CodeFeedUnavailable {
		t.Fatalf("Check = %+v, %v; want %q", rel, err, update.CodeFeedUnavailable)
	}
	st := h.up.Status()
	if st.State != update.StateFailed || st.Error != update.CodeFeedUnavailable {
		t.Errorf("status = %+v; an unavailable feed must read as a failure, not as up to date", st)
	}
}

func TestUpdateForgetDropsTheFoundUpdate(t *testing.T) {
	h := newHarness(t, []byte("app"), nil)
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	h.up.Forget()
	st := h.up.Status()
	if st.Available != nil || st.State != update.StateIdle {
		t.Errorf("status after Forget = %+v", st)
	}
	if err := h.up.Install(ctx); codeOf(err) != update.CodeNothingToInstall {
		t.Errorf("Install after Forget = %v, want %q", err, update.CodeNothingToInstall)
	}
	if got := h.backend.callList(); got != "" {
		t.Errorf("backend calls after a refused install = %q", got)
	}
	// A new check finds it again.
	if rel, err := h.up.Check(ctx, update.ChannelStable); err != nil || rel == nil {
		t.Errorf("Check after Forget = %+v, %v", rel, err)
	}
}

func TestUpdateForgetDuringAnInstallChangesNothing(t *testing.T) {
	h := newHarness(t, []byte("app"), nil)
	ctx := context.Background()
	if _, err := h.up.Check(ctx, update.ChannelStable); err != nil {
		t.Fatal(err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	h.feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, testSuffix) {
			close(reached)
			<-release
		}
		return false
	}
	done := make(chan error, 1)
	go func() { done <- h.up.Install(ctx) }()
	<-reached
	h.up.Forget()
	if st := h.up.Status(); st.Available == nil {
		t.Errorf("Forget cleared the update an install is using: %+v", st)
	}
	close(release)
	if err := <-done; err != nil {
		t.Errorf("Install = %v", err)
	}
}
