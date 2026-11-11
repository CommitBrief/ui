// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/app/jobkinds"
	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/run"
)

// These tests wire the whole manual-jobs stack: the run service, the real job
// kinds, this adapter and a SQLite store, with the fake CLI standing in for
// commitbrief. They are here because only this package may execute processes,
// and the point of the first one is a process tree.

type stack struct {
	runs  *app.Runs
	store *sqlite.Store
	repo  repo.Repository
}

func newStack(t *testing.T, h *harness) *stack {
	t.Helper()
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "CommitBrief UI", "ui.db"), sqlite.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	r, err := store.Repos().Create(ctx, repo.Repository{Owner: "Acme", Name: "App", LocalPath: repoDir(t), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds, err := app.NewKindRegistry(jobkinds.NewReviewLocal(store.Repos(), h.engine), jobkinds.NewReviewPR(store.Repos(), h.engine))
	if err != nil {
		t.Fatal(err)
	}
	runs := app.NewRuns(app.RunsDeps{
		Kinds: kinds, Jobs: store.Jobs(), Runs: store.Runs(), Tx: store, Settings: store.Settings(),
		Logger: quiet, LogDir: t.TempDir(),
	})
	if err := runs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = runs.Close(c)
	})
	return &stack{runs: runs, store: store, repo: r}
}

func (s *stack) waitRun(t *testing.T, id int64, want run.Status) run.Run {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.runs.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == want {
			return got
		}
		if got.Status.Terminal() {
			t.Fatalf("run %d ended %s (%s), want %s; tasks %+v", id, got.Status, got.ErrorCode, want, got.Tasks)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("run %d did not reach %s", id, want)
	return run.Run{}
}

// Canceling a running review ends the task canceled *and* kills what the CLI
// started: the whole process tree, through the real runner (UI-0003 section 4).
func TestCancelStopsTheCLIProcessTree(t *testing.T) {
	h := newHarness(t, false)
	pidFile := filepath.Join(t.TempDir(), "pids")
	h.set("FAKECLI_SPAWN_GRANDCHILD", "1", "FAKECLI_SLEEP_MS", "60000", "FAKECLI_PIDFILE", pidFile)
	s := newStack(t, h)

	started, err := s.runs.StartLocalReview(context.Background(), app.LocalReviewInput{RepositoryID: s.repo.ID, Mode: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	grandchild := waitForPIDs(t, pidFile, 1)[0]
	if !processAlive(grandchild) {
		t.Fatalf("the CLI's child %d is not running before the cancel", grandchild)
	}
	s.waitRun(t, started.ID, run.StatusRunning)

	if err := s.runs.CancelRun(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	got := s.waitRun(t, started.ID, run.StatusCanceled)
	if len(got.Tasks) != 1 || got.Tasks[0].Status != run.StatusCanceled || got.Tasks[0].ErrorCode != errcode.Canceled || got.Tasks[0].FinishedAt == nil {
		t.Errorf("task after cancel = %+v", got.Tasks)
	}
	waitDead(t, grandchild, 5*time.Second)
}

// A review that runs to the end: argv and working directory reach the CLI, the
// stderr stream becomes the task log, and findings, cost and version are stored.
func TestManualLocalReviewEndToEnd(t *testing.T) {
	h := newHarness(t, false)
	h.stdoutFromTestdata("v1_findings.golden")
	h.set("FAKECLI_STDERR_LINES", "3")
	s := newStack(t, h)

	started, err := s.runs.StartLocalReview(context.Background(), app.LocalReviewInput{RepositoryID: s.repo.ID, Mode: "range", Range: []string{"main..HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	got := s.waitRun(t, started.ID, run.StatusSucceeded)

	call := h.lastCall()
	if !contains(call.Args, "--json") || !contains(call.Args, "diff") || !contains(call.Args, "main..HEAD") {
		t.Errorf("argv = %v", call.Args)
	}
	sameDir(t, call.Cwd, s.repo.LocalPath)

	task, err := s.store.Runs().GetTask(context.Background(), got.Tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "succeeded" || task.FindingCount == 0 || task.CLIVersion != "1.21.2" || task.ExitCode == nil || *task.ExitCode != 0 || task.Kind != "review.local" {
		t.Errorf("stored task = %+v", task)
	}
	var findings []map[string]any
	if err := json.Unmarshal(task.FindingsJSON, &findings); err != nil || len(findings) != task.FindingCount {
		t.Errorf("stored findings = %s (%v)", task.FindingsJSON, err)
	}
	if len(findings) > 0 {
		if _, ok := findings[0]["severity"]; !ok || findings[0]["ruleId"] == nil {
			t.Errorf("stored finding keys = %v", findings[0])
		}
	}
	logView, err := s.runs.TaskLog(context.Background(), task.ID)
	if err != nil || !strings.Contains(logView.Text, "progress line 3") {
		t.Errorf("task log = %q (%v)", logView.Text, err)
	}
}

// A CLI that is too old is not a failure of the review: the task needs the
// user's attention.
func TestOldCLIEndsAsAttention(t *testing.T) {
	h := newHarness(t, false)
	h.set("FAKECLI_VERSION", "commitbrief 1.0.0 (commit abc, built 2020-01-01T00:00:00Z)")
	s := newStack(t, h)

	started, err := s.runs.StartLocalReview(context.Background(), app.LocalReviewInput{RepositoryID: s.repo.ID, Mode: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	got := s.waitRun(t, started.ID, run.StatusAttention)
	if got.ErrorCode != errcode.CLITooOld || got.Tasks[0].ErrorCode != errcode.CLITooOld {
		t.Errorf("run = %+v", got)
	}
}

// The history screen reads what the run left in the database: counts in a
// listing, payloads on demand, and it survives the repository.
func TestStackRoundTripsThroughTheDatabase(t *testing.T) {
	h := newHarness(t, false)
	h.stdoutFromTestdata("v1_findings.golden")
	s := newStack(t, h)
	ctx := context.Background()

	started, err := s.runs.StartLocalReview(ctx, app.LocalReviewInput{RepositoryID: s.repo.ID, Mode: "unstaged"})
	if err != nil {
		t.Fatal(err)
	}
	s.waitRun(t, started.ID, run.StatusSucceeded)

	list, err := s.runs.ListRuns(ctx, 10)
	if err != nil || len(list) != 1 || list[0].ID != started.ID || len(list[0].Tasks) != 1 {
		t.Fatalf("ListRuns = %+v, %v", list, err)
	}
	if len(list[0].Tasks[0].Findings) != 0 || list[0].Tasks[0].FindingCount == 0 {
		t.Errorf("a listing must carry the count, not the payload: %+v", list[0].Tasks[0])
	}
	full, err := s.runs.GetRun(ctx, started.ID)
	if err != nil || len(full.Tasks[0].Findings) == 0 || len(full.Tasks[0].Report) == 0 {
		t.Errorf("GetRun = %+v, %v", full, err)
	}
	// The repository can be deleted; the history stays.
	if err := s.store.Repos().Delete(ctx, s.repo.ID); err != nil {
		t.Fatal(err)
	}
	if after, err := s.runs.GetRun(ctx, started.ID); err != nil || after.Tasks[0].RepositoryID != 0 || after.Status != run.StatusSucceeded {
		t.Errorf("run after repository delete = %+v, %v", after, err)
	}
	if _, err := os.Stat(s.repo.LocalPath); err != nil {
		t.Errorf("the checkout is untouched: %v", err)
	}
}
