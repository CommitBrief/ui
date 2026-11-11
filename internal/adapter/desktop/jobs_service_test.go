// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/run"
)

// planOnlyKind plans like review.local (one task for the target repository) and
// finishes at once with a fixed outcome.
type planOnlyKind struct{ kind job.Kind }

func (k planOnlyKind) Kind() job.Kind                    { return k.kind }
func (planOnlyKind) Validate(spec json.RawMessage) error { return nil }
func (k planOnlyKind) Plan(_ context.Context, j job.Job) ([]run.Task, error) {
	return []run.Task{{Kind: k.kind, RepositoryID: j.Targets[0], Spec: json.RawMessage(`{}`)}}, nil
}
func (planOnlyKind) Execute(_ context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
	progress("hello")
	return run.Outcome{Findings: json.RawMessage(`[]`), Report: json.RawMessage(`{}`)}, nil
}

func jobsFixture(t *testing.T) (*JobsService, *sqlite.Store, repo.Repository) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "CommitBrief UI", "ui.db"), sqlite.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r, err := store.Repos().Create(ctx, repo.Repository{Owner: "Acme", Name: "App", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds, err := app.NewKindRegistry(planOnlyKind{kind: job.KindReviewLocal}, planOnlyKind{kind: job.KindReviewPR})
	if err != nil {
		t.Fatal(err)
	}
	runs := app.NewRuns(app.RunsDeps{
		Kinds: kinds, Jobs: store.Jobs(), Runs: store.Runs(), Tx: store, Settings: store.Settings(),
		Logger: quiet, LogDir: t.TempDir(),
	})
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runs.Close(c)
	})
	return NewJobsService(runs, quiet), store, r
}

func TestJobsServiceStartsAndReadsARun(t *testing.T) {
	svc, _, r := jobsFixture(t)
	ctx := context.Background()

	started, err := svc.StartLocalReview(ctx, app.LocalReviewInput{RepositoryID: r.ID, Mode: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var got run.Run
	for time.Now().Before(deadline) {
		if got, err = svc.GetRun(ctx, started.ID); err != nil {
			t.Fatal(err)
		}
		if got.Status.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != run.StatusSucceeded || len(got.Tasks) != 1 {
		t.Fatalf("run = %+v", got)
	}

	pr, err := svc.StartPRPreview(ctx, app.PRPreviewInput{RepositoryID: r.ID, Number: 12})
	if err != nil || len(pr.Tasks) != 1 {
		t.Fatalf("StartPRPreview = %+v, %v", pr, err)
	}
	if list, err := svc.ListRuns(ctx, 10); err != nil || len(list) != 2 {
		t.Errorf("ListRuns = %d runs, %v", len(list), err)
	}
	view, err := svc.TaskLog(ctx, got.Tasks[0].ID)
	if err != nil || view.Text != "hello\n" {
		t.Errorf("TaskLog = %+v, %v", view, err)
	}

	// The wire shape the frontend reads: camelCase keys, no log path.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	_ = json.Unmarshal(b, &generic)
	for _, k := range []string{"id", "jobId", "trigger", "status", "errorCode", "costUsd", "startedAt", "finishedAt", "tasks"} {
		if _, ok := generic[k]; !ok {
			t.Errorf("run JSON lacks %q: %s", k, b)
		}
	}
	task := generic["tasks"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "runId", "kind", "repositoryId", "prNumber", "status", "errorCode", "exitCode", "cliVersion",
		"headOid", "verdict", "findings", "report", "findingCount", "hasLog", "costUsd", "queuedAt", "startedAt", "finishedAt"} {
		if _, ok := task[k]; !ok {
			t.Errorf("task JSON lacks %q: %s", k, b)
		}
	}
	for _, k := range []string{"logPath", "LogPath", "lockKeys", "LockKeys"} {
		if _, ok := task[k]; ok {
			t.Errorf("task JSON exposes %q", k)
		}
	}
}

// Every error leaves the service as a *run.Error with a code and a field and
// nothing else: no cause, no path.
func TestJobsServiceErrorsCarryOnlyACode(t *testing.T) {
	svc, store, _ := jobsFixture(t)
	ctx := context.Background()

	check := func(err error, code run.Code, field string) {
		t.Helper()
		var re *run.Error
		if !errors.As(err, &re) || re.Code != code || re.Field != field || re.Err != nil {
			t.Errorf("error = %#v, want {%s %s} without a cause", err, code, field)
			return
		}
		raw, _ := json.Marshal(re)
		want := `{"code":"` + string(code) + `"`
		if !strings.HasPrefix(string(raw), want) || strings.Contains(string(raw), "Err") {
			t.Errorf("serialized error = %s", raw)
		}
	}

	_, err := svc.StartLocalReview(ctx, app.LocalReviewInput{RepositoryID: 9999, Mode: "staged"})
	check(err, run.CodeRepositoryNotFound, "targets")
	_, err = svc.StartLocalReview(ctx, app.LocalReviewInput{RepositoryID: 0, Mode: "staged"})
	check(err, run.CodeJobInvalid, "targets")
	check(svc.CancelTask(ctx, 4242), run.CodeTaskNotFound, "")
	check(svc.CancelRun(ctx, 4242), run.CodeRunNotFound, "")
	_, err = svc.GetRun(ctx, 4242)
	check(err, run.CodeRunNotFound, "")
	_, err = svc.TaskLog(ctx, 4242)
	check(err, run.CodeTaskNotFound, "")

	// A failing store is "internal"; the driver's text stays in the log.
	_ = store.Close()
	_, err = svc.ListRuns(ctx, 10)
	check(err, run.CodeInternal, "")
	if strings.Contains(err.Error(), "sqlite") || strings.Contains(err.Error(), "closed") {
		t.Errorf("the cause leaked into the error text: %q", err.Error())
	}
}

func TestEventNamesMatchTheApplication(t *testing.T) {
	// The registrations in init use literals; they must be the names the run
	// service emits.
	if app.EventTaskLog != "task:log" || app.EventTaskStatus != "task:status" || app.EventRunStatus != "run:status" {
		t.Errorf("event names changed: %q %q %q; update the RegisterEvent calls in jobs_service.go",
			app.EventTaskLog, app.EventTaskStatus, app.EventRunStatus)
	}
}

// Without a running Wails application the emitter drops the event instead of
// failing.
func TestEventEmitterWithoutAnApplicationDoesNothing(t *testing.T) {
	NewEventEmitter().Emit(app.EventTaskLog, app.TaskLogEvent{RunID: 1, TaskID: 2, Line: "x"})
}
