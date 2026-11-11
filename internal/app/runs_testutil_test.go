// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// memDB is an in-memory job, run and settings store that also acts as the
// transaction runner. It mirrors the SQLite semantics the service relies on.
type memDB struct {
	mu       sync.Mutex
	jobs     map[int64]port.JobRecord
	runs     map[int64]port.RunRecord
	tasks    map[int64]port.TaskRecord
	settings map[string]string
	nextJob  int64
	nextRun  int64
	nextTask int64
}

func newMemDB() *memDB {
	return &memDB{
		jobs: map[int64]port.JobRecord{}, runs: map[int64]port.RunRecord{}, tasks: map[int64]port.TaskRecord{},
		settings: map[string]string{},
	}
}

func (m *memDB) InTx(_ context.Context, fn func(port.Tx) error) error { return fn(memDBTx{m}) }

type memDBTx struct{ m *memDB }

func (t memDBTx) Repos() port.RepoStore        { return nil }
func (t memDBTx) Settings() port.SettingsStore { return t.m }
func (t memDBTx) Jobs() port.JobStore          { return t.m }
func (t memDBTx) Runs() port.RunStore          { return t.m }

// SettingsStore.

func (m *memDB) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[key]
	return v, ok, nil
}

func (m *memDB) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

func (m *memDB) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.settings, key)
	return nil
}

func (m *memDB) All(_ context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.settings))
	for k, v := range m.settings {
		out[k] = v
	}
	return out, nil
}

// JobStore.

func (m *memDB) CreateJob(_ context.Context, j port.JobRecord) (port.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextJob++
	j.ID = m.nextJob
	m.jobs[j.ID] = j
	return j, nil
}

func (m *memDB) GetJob(_ context.Context, id int64) (port.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return port.JobRecord{}, port.ErrNotFound
	}
	return j, nil
}

func (m *memDB) ListJobs(context.Context) ([]port.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]port.JobRecord, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memDB) UpdateJob(_ context.Context, j port.JobRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.jobs[j.ID]
	if !ok {
		return port.ErrNotFound
	}
	cur.Name, cur.Spec, cur.SpecVersion, cur.Mode, cur.Enabled = j.Name, j.Spec, j.SpecVersion, j.Mode, j.Enabled
	cur.RepositoryIDs, cur.Windows = j.RepositoryIDs, j.Windows
	m.jobs[j.ID] = cur
	return nil
}

func (m *memDB) DeleteJob(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[id]; !ok {
		return port.ErrNotFound
	}
	delete(m.jobs, id)
	return nil
}

func (m *memDB) SetAttention(_ context.Context, id int64, code string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return port.ErrNotFound
	}
	j.AttentionCode, j.AttentionAt = code, &at
	m.jobs[id] = j
	return nil
}

func (m *memDB) ClearAttention(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return port.ErrNotFound
	}
	j.AttentionCode, j.AttentionAt = "", nil
	m.jobs[id] = j
	return nil
}

// CostSince is the port.CostMeter of the in-memory store: the summed cost of the
// tasks that finished at or after since.
func (m *memDB) CostSince(_ context.Context, since time.Time) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum float64
	for _, t := range m.tasks {
		if t.FinishedAt != nil && !t.FinishedAt.Before(since) {
			sum += t.CostUSD
		}
	}
	return sum, nil
}

// RunStore.

func (m *memDB) CreateRun(_ context.Context, r port.RunRecord) (port.RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextRun++
	r.ID = m.nextRun
	m.runs[r.ID] = r
	return r, nil
}

func (m *memDB) UpdateRun(_ context.Context, r port.RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.runs[r.ID]
	if !ok {
		return port.ErrNotFound
	}
	cur.Status, cur.ErrorCode, cur.CostUSD, cur.FinishedAt = r.Status, r.ErrorCode, r.CostUSD, r.FinishedAt
	m.runs[r.ID] = cur
	return nil
}

func (m *memDB) GetRun(_ context.Context, id int64) (port.RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return port.RunRecord{}, port.ErrNotFound
	}
	return r, nil
}

func (m *memDB) ListRuns(_ context.Context, jobID int64, limit int) ([]port.RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []port.RunRecord
	for _, r := range m.runs {
		if jobID == 0 || r.JobID == jobID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memDB) CreateTask(_ context.Context, t port.TaskRecord) (port.TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextTask++
	t.ID = m.nextTask
	m.tasks[t.ID] = t
	return t, nil
}

func (m *memDB) UpdateTask(_ context.Context, t port.TaskRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[t.ID]; !ok {
		return port.ErrNotFound
	}
	m.tasks[t.ID] = t
	return nil
}

func (m *memDB) GetTask(_ context.Context, id int64) (port.TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return port.TaskRecord{}, port.ErrNotFound
	}
	return withCount(t), nil
}

func (m *memDB) ListTasks(_ context.Context, runID int64, withPayload bool) ([]port.TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []port.TaskRecord
	for _, t := range m.tasks {
		if t.RunID != runID {
			continue
		}
		t = withCount(t)
		if !withPayload {
			t.FindingsJSON, t.ReportJSON = nil, nil
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func withCount(t port.TaskRecord) port.TaskRecord {
	var arr []json.RawMessage
	if json.Unmarshal(t.FindingsJSON, &arr) == nil {
		t.FindingCount = len(arr)
	}
	return t
}

func (m *memDB) AbortUnfinished(_ context.Context, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, t := range m.tasks {
		if t.Status == "queued" || t.Status == "running" {
			t.Status, t.ErrorCode, t.FinishedAt = "canceled", "canceled", &at
			m.tasks[id] = t
			n++
		}
	}
	for id, r := range m.runs {
		if r.Status == "queued" || r.Status == "running" {
			r.Status, r.ErrorCode, r.FinishedAt = "canceled", "canceled", &at
			m.runs[id] = r
		}
	}
	return n, nil
}

func (m *memDB) ExpiredLogs(_ context.Context, before time.Time, keepNewest int) ([]port.TaskLogRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.runs))
	for id := range m.runs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.runs[ids[i]], m.runs[ids[j]]
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.After(b.StartedAt)
		}
		return a.ID > b.ID
	})
	kept := map[int64]bool{}
	for i, id := range ids {
		if i < keepNewest {
			kept[id] = true
		}
	}
	var out []port.TaskLogRef
	for _, t := range m.tasks {
		r := m.runs[t.RunID]
		if t.LogPath == "" || t.Status == "queued" || t.Status == "running" {
			continue
		}
		if r.StartedAt.Before(before) || !kept[r.ID] {
			out = append(out, port.TaskLogRef{TaskID: t.ID, Path: t.LogPath})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}

func (m *memDB) ClearLog(_ context.Context, taskID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return port.ErrNotFound
	}
	t.LogPath = ""
	m.tasks[taskID] = t
	return nil
}

// task returns the stored task.
func (m *memDB) task(t *testing.T, id int64) port.TaskRecord {
	t.Helper()
	rec, err := m.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("task %d: %v", id, err)
	}
	return rec
}

func (m *memDB) run(t *testing.T, id int64) port.RunRecord {
	t.Helper()
	rec, err := m.GetRun(context.Background(), id)
	if err != nil {
		t.Fatalf("run %d: %v", id, err)
	}
	return rec
}

// recorder collects the events the service emits.
type recorder struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	Name    string
	Payload any
}

func (r *recorder) Emit(name string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recorded{name, payload})
}

func (r *recorder) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.events...)
}

// taskStatuses lists the statuses emitted for a task, in order.
func (r *recorder) taskStatuses(taskID int64) []run.Status {
	var out []run.Status
	for _, e := range r.all() {
		if ev, ok := e.Payload.(TaskStatusEvent); ok && e.Name == EventTaskStatus && ev.TaskID == taskID {
			out = append(out, ev.Status)
		}
	}
	return out
}

func (r *recorder) logLines(taskID int64) []string {
	var out []string
	for _, e := range r.all() {
		if ev, ok := e.Payload.(TaskLogEvent); ok && e.Name == EventTaskLog && ev.TaskID == taskID {
			out = append(out, ev.Line)
		}
	}
	return out
}

// fakeKind is a job kind whose behaviour a test supplies. Plan defaults to one
// task that holds the lock "repo:<first target>".
type fakeKind struct {
	name job.Kind
	plan func(ctx context.Context, j job.Job) ([]run.Task, error)
	exec func(ctx context.Context, t run.Task, progress func(string)) (run.Outcome, error)
}

func (k *fakeKind) Kind() job.Kind { return k.name }

func (k *fakeKind) Validate(spec json.RawMessage) error { return nil }

func (k *fakeKind) Plan(ctx context.Context, j job.Job) ([]run.Task, error) {
	if k.plan != nil {
		return k.plan(ctx, j)
	}
	return []run.Task{{
		Kind: k.name, RepositoryID: j.Targets[0], Spec: json.RawMessage(`{}`),
		LockKeys: []string{"repo:" + strconv.FormatInt(j.Targets[0], 10)},
	}}, nil
}

func (k *fakeKind) Execute(ctx context.Context, t run.Task, progress func(string)) (run.Outcome, error) {
	return k.exec(ctx, t, progress)
}

// newRuns builds a service over a memory store with one fake kind registered as
// review.local, a quiet logger and a temporary log directory.
func newRuns(t *testing.T, kind *fakeKind, mutate ...func(*RunsDeps)) (*Runs, *memDB, *recorder) {
	t.Helper()
	db := newMemDB()
	rec := &recorder{}
	kind.name = job.KindReviewLocal
	reg, err := NewKindRegistry(kind)
	if err != nil {
		t.Fatal(err)
	}
	d := RunsDeps{
		Kinds: reg, Jobs: db, Runs: db, Tx: db, Settings: db, Events: rec,
		Logger: quietLogger, LogDir: t.TempDir(),
	}
	for _, m := range mutate {
		m(&d)
	}
	r := NewRuns(d)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	return r, db, rec
}

// local starts a manual local review of repository id.
func local(t *testing.T, r *Runs, repoID int64) run.Run {
	t.Helper()
	out, err := r.StartLocalReview(context.Background(), LocalReviewInput{RepositoryID: repoID, Mode: job.LocalStaged})
	if err != nil {
		t.Fatalf("StartLocalReview: %v", err)
	}
	return out
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func okOutcome() run.Outcome {
	zero := 0
	return run.Outcome{
		ExitCode: &zero, CLIVersion: "1.21.2", CostUSD: 0.25, HeadOID: "abc", Verdict: "comment",
		Findings: json.RawMessage(`[{"title":"a"},{"title":"b"}]`),
		Report:   json.RawMessage(`{"content":"","findings":[{"title":"a"},{"title":"b"}]}`),
	}
}
