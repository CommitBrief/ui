// SPDX-License-Identifier: GPL-3.0-or-later

package jobkinds

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

var botLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// fakeTracked is an in-memory TrackedPRStore.
type fakeTracked struct {
	mu   sync.Mutex
	rows map[[2]int64]*port.TrackedPR
	// heads is what each job reviewed last, per mode.
	heads map[headKey]reviewed
	// markErr makes MarkReviewed fail.
	markErr error
}

type headKey struct {
	job  int64
	mode string
	repo int64
	num  int
}

type reviewed struct {
	head string
	at   time.Time
}

func newFakeTracked() *fakeTracked {
	return &fakeTracked{rows: map[[2]int64]*port.TrackedPR{}, heads: map[headKey]reviewed{}}
}

func (f *fakeTracked) Track(_ context.Context, repoID int64, number int, source string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := [2]int64{repoID, int64(number)}
	if row, ok := f.rows[k]; ok {
		row.State = port.PRStateOpen
		return nil
	}
	f.rows[k] = &port.TrackedPR{RepositoryID: repoID, Number: number, FirstSeenAt: at, State: port.PRStateOpen, Source: source}
	return nil
}

func (f *fakeTracked) ListOpen(_ context.Context, jobID int64, mode string, ids []int64) ([]port.TrackedPR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []port.TrackedPR
	for _, row := range f.rows {
		for _, id := range ids {
			if row.RepositoryID == id && row.State == port.PRStateOpen {
				p := *row
				if h, ok := f.heads[headKey{jobID, mode, row.RepositoryID, row.Number}]; ok {
					at := h.at
					p.LastHeadOID, p.LastRunAt = h.head, &at
				}
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RepositoryID != out[j].RepositoryID {
			return out[i].RepositoryID < out[j].RepositoryID
		}
		return out[i].Number < out[j].Number
	})
	return out, nil
}

func (f *fakeTracked) SetState(_ context.Context, repoID int64, number int, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[[2]int64{repoID, int64(number)}]
	if !ok {
		return port.ErrNotFound
	}
	row.State = state
	return nil
}

func (f *fakeTracked) MarkReviewed(_ context.Context, jobID int64, mode string, repoID int64, number int, head string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	row, ok := f.rows[[2]int64{repoID, int64(number)}]
	if !ok {
		return port.ErrNotFound
	}
	_ = row
	f.heads[headKey{jobID, mode, repoID, number}] = reviewed{head: head, at: at}
	return nil
}

// row returns the tracked pull request with the head job 5 (the one botJob
// builds) reviewed last, in either mode.
func (f *fakeTracked) row(repoID int64, number int) port.TrackedPR {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[[2]int64{repoID, int64(number)}]
	if !ok {
		return port.TrackedPR{}
	}
	p := *row
	for _, mode := range []string{"preview", "post"} {
		if h, ok := f.heads[headKey{5, mode, repoID, number}]; ok {
			at := h.at
			p.LastHeadOID, p.LastRunAt = h.head, &at
		}
	}
	return p
}

// headOf is what the job reviewed last in the mode; empty when nothing.
func (f *fakeTracked) headOf(jobID int64, mode string, repoID int64, number int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heads[headKey{jobID, mode, repoID, number}].head
}

// fakeJobs serves the stored job a post task re-reads.
type fakeJobs struct {
	port.JobStore
	mu  sync.Mutex
	rec *port.JobRecord // nil: the job was deleted
}

func (f *fakeJobs) GetJob(_ context.Context, id int64) (port.JobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rec == nil || f.rec.ID != id {
		return port.JobRecord{}, port.ErrNotFound
	}
	return *f.rec, nil
}

func (f *fakeJobs) set(rec *port.JobRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec = rec
}

func storedJob(mode job.Mode, spec job.BotReviewRequestedSpec) *port.JobRecord {
	return &port.JobRecord{ID: 5, Kind: string(job.KindBotReviewRequested), Name: "bot", Spec: spec.Marshal(), Mode: string(mode), Enabled: true}
}

// fakeSource is a PRSource that answers as scripted and counts its calls.
type fakeSource struct {
	port.PRSource
	mu        sync.Mutex
	search    []port.PRRef
	searchErr error
	views     map[string]port.PRInfo // "owner/name#n" -> state
	viewErrs  map[string]error
	searches  [][]string
	viewed    []string
}

func (f *fakeSource) SearchReviewRequested(_ context.Context, repos []string) ([]port.PRRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, append([]string(nil), repos...))
	return f.search, f.searchErr
}

func (f *fakeSource) ViewPR(_ context.Context, repoSlug string, number int) (port.PRInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := repoSlug + "#" + itoa(number)
	f.viewed = append(f.viewed, key)
	if err := f.viewErrs[key]; err != nil {
		return port.PRInfo{}, err
	}
	info, ok := f.views[key]
	if !ok {
		return port.PRInfo{}, errcode.New(errcode.Unknown, "no such pull request")
	}
	return info, nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type fixedBudget struct {
	over bool
	err  error
}

func (b fixedBudget) Exceeded(context.Context) (bool, error) { return b.over, b.err }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// botEngine is a ReviewEngine with scripted capabilities on top of fakeEngine.
type botEngine struct {
	*fakeEngine
	caps    port.Capabilities
	capsErr error
}

func (b botEngine) Capabilities(context.Context) (port.Capabilities, error) { return b.caps, b.capsErr }

type botRig struct {
	t       *testing.T
	kind    *BotReviewRequested
	repos   fakeRepos
	tracked *fakeTracked
	source  *fakeSource
	engine  *fakeEngine
	budget  *fixedBudget
	caps    *botEngine
	// jobs holds job 5 as a post job with recorded consent; a test changes or
	// removes it to see what a queued task does.
	jobs *fakeJobs
}

func newBot(t *testing.T) *botRig {
	t.Helper()
	rs := fakeRepos{rows: map[int64]repo.Repository{
		1: {ID: 1, Owner: "Acme", Name: "App", LocalPath: appPath(t), Enabled: true},
		2: {ID: 2, Owner: "acme", Name: "docs", Enabled: true}, // no checkout
		3: {ID: 3, Owner: "acme", Name: "off", Enabled: false},
	}}
	eng := &fakeEngine{report: sampleReport(), versionOK: true}
	be := &botEngine{fakeEngine: eng, caps: port.Capabilities{Base: true, PostingInCLI: true, Posting: true}}
	src := &fakeSource{views: map[string]port.PRInfo{}, viewErrs: map[string]error{}}
	tr := newFakeTracked()
	bud := &fixedBudget{}
	jobs := &fakeJobs{rec: storedJob(job.ModePost, consented())}
	k := NewBotReviewRequested(BotDeps{
		Repos: rs, Tracked: tr, Jobs: jobs, Source: src, Engine: be, Budget: bud,
		Clock: fixedClock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}, Logger: botLogger,
	})
	k.goos = "linux"
	return &botRig{t: t, kind: k, repos: rs, tracked: tr, source: src, engine: eng, budget: bud, caps: be, jobs: jobs}
}

func botJob(mode job.Mode, spec job.BotReviewRequestedSpec, targets ...int64) job.Job {
	return job.Job{ID: 5, Kind: job.KindBotReviewRequested, Name: "bot", Spec: spec.Marshal(), Mode: mode, Enabled: true, Targets: targets}
}

func consented() job.BotReviewRequestedSpec {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	return job.BotReviewRequestedSpec{PostConsentAt: &at}
}

func ref(slug string, n int, draft bool) port.PRRef {
	return port.PRRef{Repo: slug, Number: n, IsDraft: draft}
}

func open(head string) port.PRInfo { return port.PRInfo{State: port.PRStateOpen, HeadOID: head} }

func TestBotPlanFollowsTheDiscoveredPullRequests(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false), ref("acme/docs", 3, false), ref("other/else", 9, false), ref("Acme/App", 8, true)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA), "acme/docs#3": open(headA)}

	tasks, err := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.source.searches) != 1 {
		t.Fatalf("searches = %v, want one combined query", b.source.searches)
	}
	if got := b.source.searches[0]; len(got) != 2 || got[0] != "Acme/App" || got[1] != "acme/docs" {
		t.Errorf("searched %v: only the enabled, watched repositories", got)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %+v", tasks)
	}
	t0 := tasks[0]
	if t0.RepositoryID != 1 || t0.PRNumber != 7 || t0.Kind != job.KindBotReviewRequested || t0.HeadOID != headA || t0.Status != run.StatusQueued {
		t.Errorf("task 0 = %+v", t0)
	}
	// The pull request lock and, for a repository with a checkout, the working
	// tree lock; a repository without one has only the pull request lock.
	if len(t0.LockKeys) != 2 || t0.LockKeys[0] != run.LockKeyForPR("Acme/App", 7) || t0.LockKeys[1] != run.LockKeyForPath("linux", b.repos.rows[1].LocalPath) {
		t.Errorf("lock keys = %v", t0.LockKeys)
	}
	if len(tasks[1].LockKeys) != 1 {
		t.Errorf("a repository without a checkout holds only the pull request lock: %v", tasks[1].LockKeys)
	}
	// The draft was skipped (never tracked), the unrequested repository ignored.
	if b.tracked.row(1, 8).Number != 0 || b.tracked.row(1, 7).Number != 7 {
		t.Error("tracked set wrong: drafts must not be tracked unless the job includes them")
	}
	if len(b.engine.prReqs()) != 0 {
		t.Error("planning must not review anything")
	}
}

func TestBotPlanNeverCreatesATaskForAnUnchangedHead(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	j := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)

	tasks, _ := b.kind.Plan(ctx, j)
	if len(tasks) != 1 {
		t.Fatalf("first plan: %+v", tasks)
	}
	out, err := b.kind.Execute(ctx, tasks[0], func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if out.HeadOID != headA {
		t.Errorf("the outcome names head %q, want the one planned", out.HeadOID)
	}
	if got := b.tracked.row(1, 7).LastHeadOID; got != headA {
		t.Fatalf("last head = %q, want %q recorded after a successful review", got, headA)
	}

	// Same head: no task, and the CLI is not called.
	reviews := len(b.engine.prReqs())
	tasks, err = b.kind.Plan(ctx, j)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("same head: tasks = %+v, %v", tasks, err)
	}
	if len(b.engine.prReqs()) != reviews {
		t.Error("the CLI was called for an unchanged head")
	}

	// A push moves the head: reviewed again.
	b.source.views["Acme/App#7"] = open(headB)
	tasks, _ = b.kind.Plan(ctx, j)
	if len(tasks) != 1 || tasks[0].HeadOID != headB {
		t.Errorf("after a push: %+v", tasks)
	}
}

// GitHub removes the user from the requested reviewers when a review is
// submitted, so the search stops returning the pull request; it keeps being
// followed from the tracked set.
func TestBotPlanKeepsFollowingAPullRequestDroppedFromTheRequestedList(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	j := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	tasks, _ := b.kind.Plan(ctx, j)
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); err != nil {
		t.Fatal(err)
	}

	// The review request is gone from the search, a new commit arrives.
	b.source.search = nil
	b.source.views["Acme/App#7"] = open(headB)
	tasks, err := b.kind.Plan(ctx, j)
	if err != nil || len(tasks) != 1 || tasks[0].PRNumber != 7 || tasks[0].HeadOID != headB {
		t.Fatalf("a dropped pull request is no longer followed: %+v, %v", tasks, err)
	}

	// Merged: tracked state is updated and nothing is planned, then it is never
	// read again.
	b.source.views["Acme/App#7"] = port.PRInfo{State: port.PRStateMerged, HeadOID: headB}
	tasks, _ = b.kind.Plan(ctx, j)
	if len(tasks) != 0 || b.tracked.row(1, 7).State != port.PRStateMerged {
		t.Errorf("merged: tasks = %+v, state = %q", tasks, b.tracked.row(1, 7).State)
	}
	b.source.viewed = nil
	_, _ = b.kind.Plan(ctx, j)
	if len(b.source.viewed) != 0 {
		t.Errorf("a merged pull request was read again: %v", b.source.viewed)
	}
}

func TestBotPlanDraftsFollowTheJobSetting(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, true)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": {State: port.PRStateOpen, HeadOID: headA, IsDraft: true}}

	if tasks, _ := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)); len(tasks) != 0 {
		t.Errorf("a draft was planned: %+v", tasks)
	}
	if tasks, _ := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{IncludeDrafts: true}, 1)); len(tasks) != 1 {
		t.Errorf("includeDrafts: %+v", tasks)
	}

	// A tracked pull request that turns from draft to ready is reviewed.
	b2 := newBot(t)
	_ = b2.tracked.Track(ctx, 1, 9, trackedSource, time.Now())
	b2.source.views = map[string]port.PRInfo{"Acme/App#9": {State: port.PRStateOpen, HeadOID: headA, IsDraft: true}}
	j := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)
	if tasks, _ := b2.kind.Plan(ctx, j); len(tasks) != 0 {
		t.Errorf("a tracked draft was planned: %+v", tasks)
	}
	b2.source.views["Acme/App#9"] = open(headA)
	if tasks, _ := b2.kind.Plan(ctx, j); len(tasks) != 1 {
		t.Errorf("a draft turned ready was not planned: %+v", tasks)
	}
}

func TestBotPostJobWithoutConsentIsRejected(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}

	_, err := b.kind.Plan(ctx, botJob(job.ModePost, job.BotReviewRequestedSpec{}, 1))
	var je *job.Error
	if !errors.As(err, &je) || je.Code != job.CodeSpecInvalid || je.Field != "spec.postConsentAt" {
		t.Fatalf("Plan = %v, want job_spec_invalid on spec.postConsentAt", err)
	}
	if len(b.source.searches) != 0 || len(b.engine.postReqs()) != 0 {
		t.Error("a post job without consent must touch nothing")
	}
}

func TestBotPostJobNeedsACLIThatCanPost(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.caps.caps.Posting = false
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}

	tasks, err := b.kind.Plan(ctx, botJob(job.ModePost, consented(), 1))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Plan = %+v, %v", tasks, err)
	}
	if len(b.source.searches) != 0 {
		t.Error("nothing is discovered (or tracked as seen) before the CLI can post")
	}
	_, err = b.kind.Execute(ctx, tasks[0], func(string) {})
	if !errcode.Is(err, errcode.CapabilityMissing) {
		t.Fatalf("Execute = %v, want capability_missing", err)
	}
	if status, _ := run.Classify(err); status != run.StatusAttention {
		t.Errorf("capability_missing ended %s, want attention", status)
	}
	if len(b.engine.postReqs()) != 0 {
		t.Error("PostPR was called without the capability")
	}

	// A CLI that cannot even be found is reported the same way, with its class.
	b.caps.capsErr = errcode.New(errcode.CLINotFound, "")
	tasks, _ = b.kind.Plan(ctx, botJob(job.ModePost, consented(), 1))
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); !errcode.Is(err, errcode.CLINotFound) {
		t.Errorf("no CLI: %v", err)
	}
}

func TestBotPostExecutePostsIncrementallyAndRecordsTheReportedHead(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	b.engine.report = sampleReport()
	b.engine.report.Remote = &review.Remote{Status: review.RemoteSubmitted, HeadOID: headB, Verdict: review.VerdictComment}

	tasks, err := b.kind.Plan(ctx, botJob(job.ModePost, consented(), 1))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Plan = %+v, %v", tasks, err)
	}
	out, err := b.kind.Execute(ctx, tasks[0], func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	posts := b.engine.postReqs()
	if len(posts) != 1 || len(b.engine.prReqs()) != 0 {
		t.Fatalf("posts = %d, previews = %d; a post job posts", len(posts), len(b.engine.prReqs()))
	}
	if !posts[0].Options.Unattended || posts[0].Repo != "Acme/App" || posts[0].Number != 7 || posts[0].LocalPath != b.repos.rows[1].LocalPath {
		t.Errorf("post request = %+v", posts[0])
	}
	if out.HeadOID != headB || out.Verdict != "comment" {
		t.Errorf("outcome = %+v; the head is the one the review was produced against", out)
	}
	if got := b.tracked.row(1, 7).LastHeadOID; got != headB {
		t.Errorf("tracked head = %q, want the reported %q", got, headB)
	}
}

// In posting mode the CLI warns and posts anyway on a secret scan hit; the task
// is flagged for the user, and the review is still recorded.
func TestBotPostWithASecretWarningIsFlagged(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	b.engine.report = sampleReport()
	b.engine.report.Remote = &review.Remote{
		Status: review.RemoteSubmitted, HeadOID: headA, Verdict: review.VerdictComment,
		Warnings: []review.Warning{{Code: "secrets_detected", Count: 2}},
	}
	tasks, _ := b.kind.Plan(ctx, botJob(job.ModePost, consented(), 1))
	out, err := b.kind.Execute(ctx, tasks[0], func(string) {})
	if !errcode.Is(err, errcode.SecretDetected) || len(out.Findings) == 0 {
		t.Fatalf("Execute = %+v, %v; want the outcome kept and secret_detected", out, err)
	}
	if b.tracked.row(1, 7).LastHeadOID != headA {
		t.Error("the posted review was not recorded; it would be posted again")
	}
}

func TestBotPreviewNeverPostsAndRunsUnattended(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("acme/docs", 3, false)}
	b.source.views = map[string]port.PRInfo{"acme/docs#3": open(headA)}
	tasks, _ := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 2))
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); err != nil {
		t.Fatal(err)
	}
	prs := b.engine.prReqs()
	if len(b.engine.postReqs()) != 0 || len(prs) != 1 {
		t.Fatalf("posts = %d, previews = %d", len(b.engine.postReqs()), len(prs))
	}
	if !prs[0].Options.Unattended || prs[0].LocalPath != "" || prs[0].Repo != "acme/docs" {
		t.Errorf("preview request = %+v", prs[0])
	}
}

func TestBotDiscoveryFailuresAreRecordedAsTasks(t *testing.T) {
	ctx := context.Background()

	t.Run("gh not logged in", func(t *testing.T) {
		b := newBot(t)
		b.source.searchErr = errcode.New(errcode.GHNotAuthenticated, "")
		_ = b.tracked.Track(ctx, 1, 7, trackedSource, time.Now())
		b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}

		tasks, err := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1))
		if err != nil || len(tasks) != 1 || tasks[0].PRNumber != 0 || tasks[0].RepositoryID != 0 || len(tasks[0].LockKeys) != 0 {
			t.Fatalf("tasks = %+v, %v; a login problem is one failure task, nothing else", tasks, err)
		}
		if len(b.source.viewed) != 0 {
			t.Error("pull requests were read although gh has no login")
		}
		_, err = b.kind.Execute(ctx, tasks[0], func(string) {})
		if !errcode.Is(err, errcode.GHNotAuthenticated) {
			t.Fatalf("Execute = %v", err)
		}
		if status, code := run.Classify(err); status != run.StatusAttention || code != errcode.GHNotAuthenticated {
			t.Errorf("classified %s %s", status, code)
		}
		if len(b.engine.prReqs()) != 0 {
			t.Error("the CLI ran for a failure task")
		}
	})

	t.Run("a search that timed out still follows the tracked set", func(t *testing.T) {
		b := newBot(t)
		b.source.searchErr = errcode.New(errcode.Timeout, "")
		_ = b.tracked.Track(ctx, 1, 7, trackedSource, time.Now())
		b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headB)}

		tasks, err := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1))
		if err != nil || len(tasks) != 2 {
			t.Fatalf("tasks = %+v, %v; want the tracked review and the failure", tasks, err)
		}
		if tasks[0].PRNumber != 7 {
			t.Errorf("first task = %+v", tasks[0])
		}
		_, err = b.kind.Execute(ctx, tasks[1], func(string) {})
		if status, code := run.Classify(err); status != run.StatusFailed || code != errcode.Timeout {
			t.Errorf("failure task classified %s %s, want failed timeout (retried at the next slot)", status, code)
		}
	})

	t.Run("one unreadable pull request does not stop the others", func(t *testing.T) {
		b := newBot(t)
		_ = b.tracked.Track(ctx, 1, 7, trackedSource, time.Now())
		_ = b.tracked.Track(ctx, 1, 8, trackedSource, time.Now())
		b.source.viewErrs["Acme/App#7"] = errcode.New(errcode.Unknown, "gone")
		b.source.views = map[string]port.PRInfo{"Acme/App#8": open(headA)}
		tasks, err := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1))
		if err != nil || len(tasks) != 1 || tasks[0].PRNumber != 8 {
			t.Errorf("tasks = %+v, %v", tasks, err)
		}
	})

	t.Run("a canceled plan is not a failure task", func(t *testing.T) {
		b := newBot(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		b.source.searchErr = context.Canceled
		if _, err := b.kind.Plan(cctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)); err == nil {
			t.Error("a canceled plan returned tasks")
		}
	})
}

func TestBotExecuteStopsWhenTheBudgetIsSpent(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	tasks, _ := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1))

	b.budget.over = true
	_, err := b.kind.Execute(ctx, tasks[0], func(string) {})
	if !errcode.Is(err, errcode.CostAborted) || len(b.engine.prReqs()) != 0 {
		t.Fatalf("Execute = %v; the CLI must not run with the budget spent", err)
	}
	if b.tracked.row(1, 7).LastHeadOID != "" {
		t.Error("an aborted task must not count as reviewed")
	}

	// Not knowing the spend does not stop the work; it is logged.
	b.budget.over, b.budget.err = false, errors.New("db busy")
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); err != nil {
		t.Errorf("Execute with an unreadable budget = %v", err)
	}
}

func TestBotFailedReviewsAreRetriedAtTheNextSlot(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	j := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)

	for _, code := range []errcode.Code{errcode.Timeout, errcode.TooVolatile, errcode.Degraded, errcode.ProviderError} {
		tasks, _ := b.kind.Plan(ctx, j)
		if len(tasks) != 1 {
			t.Fatalf("%s: tasks = %+v", code, tasks)
		}
		b.engine.err = errcode.New(code, "")
		if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); !errcode.Is(err, code) {
			t.Fatalf("%s: Execute = %v", code, err)
		}
		if b.tracked.row(1, 7).LastHeadOID != "" {
			t.Errorf("%s: a failed review was recorded as done", code)
		}
	}
}

// A pull request the user authored cannot be reviewed; its head is recorded so a
// resumed job does not run into it again until it changes.
func TestBotSelfPullRequestIsNotRetriedAtTheSameHead(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	j := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)
	tasks, _ := b.kind.Plan(ctx, j)
	b.engine.err = errcode.New(errcode.SelfPR, "")
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); !errcode.Is(err, errcode.SelfPR) {
		t.Fatal(err)
	}
	if tasks, _ := b.kind.Plan(ctx, j); len(tasks) != 0 {
		t.Errorf("planned again at the same head: %+v", tasks)
	}
	b.source.views["Acme/App#7"] = open(headB)
	if tasks, _ := b.kind.Plan(ctx, j); len(tasks) != 1 {
		t.Errorf("not planned after a push: %+v", tasks)
	}
}

func TestBotPlanWithNothingToWatch(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	// Target 3 is switched off, 99 was deleted.
	tasks, err := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 3, 99))
	if err != nil || len(tasks) != 0 {
		t.Fatalf("tasks = %+v, %v", tasks, err)
	}
	if len(b.source.searches) != 0 {
		t.Error("gh was called with no repository to watch")
	}
}

func TestBotRefusesAGarbledTaskSpecAndAChangedPath(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	for _, spec := range []string{`nope`, `{}`, `{"number":7}`, `{"number":7,"mode":"yolo"}`, `{"failure":"made_up"}`} {
		tk := run.Task{Kind: job.KindBotReviewRequested, RepositoryID: 1, Spec: json.RawMessage(spec)}
		if _, err := b.kind.Execute(ctx, tk, func(string) {}); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("spec %s: %v", spec, err)
		}
	}
	if len(b.engine.prReqs()) != 0 {
		t.Error("the CLI ran for a garbled task")
	}

	// A repository whose path was edited while the task waited is not reviewed in
	// the new path (it holds the lock of the old one).
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
	tasks, _ := b.kind.Plan(ctx, botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1))
	moved := b.repos.rows[1]
	moved.LocalPath = appPath(t)
	b.repos.rows[1] = moved
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); !errcode.Is(err, errcode.NotARepo) {
		t.Errorf("moved path: %v", err)
	}
}

func TestBotKindRegistersUnderItsName(t *testing.T) {
	b := newBot(t)
	if b.kind.Kind() != job.KindBotReviewRequested {
		t.Errorf("Kind = %q", b.kind.Kind())
	}
	if err := b.kind.Validate(json.RawMessage(`{"includeDrafts":true}`)); err != nil {
		t.Error(err)
	}
	if err := b.kind.Validate(json.RawMessage(`{"nope":1}`)); err == nil {
		t.Error("an unknown spec field was accepted")
	}
}

// What was reviewed is the job's own, per mode. A preview that looked at a head
// must not stop a post job from posting on it (and the other way round), and a
// job switched from preview to post starts from a clean slate.
func TestBotReviewedHeadBelongsToTheJobAndTheMode(t *testing.T) {
	ctx := context.Background()
	b := newBot(t)
	b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
	b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}

	// A preview job (id 5) reviews #7 at headA.
	previewJob := botJob(job.ModePreview, job.BotReviewRequestedSpec{}, 1)
	tasks, _ := b.kind.Plan(ctx, previewJob)
	if len(tasks) != 1 {
		t.Fatalf("preview plan: %+v", tasks)
	}
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := b.kind.Plan(ctx, previewJob); len(tasks) != 0 {
		t.Fatalf("the preview job plans the same head again: %+v", tasks)
	}

	// A post job on the same repository (id 6) still has to post on headA.
	postJob := botJob(job.ModePost, consented(), 1)
	postJob.ID = 6
	b.jobs.set(&port.JobRecord{ID: 6, Kind: string(job.KindBotReviewRequested), Name: "post", Spec: consented().Marshal(), Mode: "post", Enabled: true})
	tasks, err := b.kind.Plan(ctx, postJob)
	if err != nil || len(tasks) != 1 || tasks[0].HeadOID != headA {
		t.Fatalf("post job after a preview of the same head: %+v, %v", tasks, err)
	}
	if _, err := b.kind.Execute(ctx, tasks[0], func(string) {}); err != nil {
		t.Fatal(err)
	}
	if n := len(b.engine.postReqs()); n != 1 {
		t.Fatalf("posts = %d, want the post job to post once", n)
	}
	if tasks, _ := b.kind.Plan(ctx, postJob); len(tasks) != 0 {
		t.Errorf("after posting, the same head is done for the post job: %+v", tasks)
	}

	// The preview job is not silenced by the post either.
	b.source.views["Acme/App#7"] = open(headB)
	if tasks, _ := b.kind.Plan(ctx, previewJob); len(tasks) != 1 || tasks[0].HeadOID != headB {
		t.Errorf("preview after a push: %+v", tasks)
	}

	// The same job switched from preview to post: nothing it previewed counts.
	b.source.views["Acme/App#7"] = open(headA)
	switched := botJob(job.ModePost, consented(), 1) // id 5, as the preview job was
	b.jobs.set(storedJob(job.ModePost, consented()))
	tasks, err = b.kind.Plan(ctx, switched)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("job 5 switched to post: %+v, %v", tasks, err)
	}
	if got := b.tracked.headOf(5, "preview", 1, 7); got != headA {
		t.Errorf("the preview record of job 5 = %q; it is kept, but it is not the post record", got)
	}
}

// A post task waits in the queue behind others. The mode and the consent are
// the job's as they are when the task runs.
func TestBotQueuedPostTaskIsCanceledWhenTheJobNoLongerPosts(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		after func(b *botRig)
	}{
		{"switched to preview", func(b *botRig) { b.jobs.set(storedJob(job.ModePreview, job.BotReviewRequestedSpec{})) }},
		{"consent withdrawn", func(b *botRig) { b.jobs.set(storedJob(job.ModePost, job.BotReviewRequestedSpec{})) }},
		{"deleted", func(b *botRig) { b.jobs.set(nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBot(t)
			b.source.search = []port.PRRef{ref("Acme/App", 7, false)}
			b.source.views = map[string]port.PRInfo{"Acme/App#7": open(headA)}
			tasks, err := b.kind.Plan(ctx, botJob(job.ModePost, consented(), 1))
			if err != nil || len(tasks) != 1 {
				t.Fatalf("Plan = %+v, %v", tasks, err)
			}
			tc.after(b)
			_, err = b.kind.Execute(ctx, tasks[0], func(string) {})
			if !errcode.Is(err, errcode.Canceled) {
				t.Fatalf("Execute = %v, want canceled", err)
			}
			if status, _ := run.Classify(err); status != run.StatusCanceled {
				t.Errorf("status = %s, want canceled (no attention)", status)
			}
			if len(b.engine.postReqs()) != 0 {
				t.Error("the review was posted although the job no longer allows it")
			}
			if b.tracked.row(1, 7).LastHeadOID != "" {
				t.Error("a task that did not run must not mark the head as reviewed")
			}
		})
	}
}
