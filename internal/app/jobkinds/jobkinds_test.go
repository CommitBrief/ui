// SPDX-License-Identifier: GPL-3.0-or-later

package jobkinds

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// fakeRepos is a RepoStore holding a fixed set of repositories.
type fakeRepos struct {
	port.RepoStore
	rows map[int64]repo.Repository
}

func (f fakeRepos) Get(_ context.Context, id int64) (repo.Repository, error) {
	r, ok := f.rows[id]
	if !ok {
		return repo.Repository{}, port.ErrNotFound
	}
	return r, nil
}

// fakeEngine records the request it was given and answers as scripted.
type fakeEngine struct {
	port.ReviewEngine
	localReq  *port.LocalReviewRequest
	prReq     *port.PRRequest
	postReq   *port.PRRequest
	report    review.Report
	err       error
	progress  []string
	versionOK bool
}

func (f *fakeEngine) Version(context.Context) (port.CLIVersion, error) {
	if !f.versionOK {
		return port.CLIVersion{}, errors.New("no cli")
	}
	return port.CLIVersion{Major: 1, Minor: 21, Patch: 2, Raw: "1.21.2"}, nil
}

func (f *fakeEngine) ReviewLocal(_ context.Context, req port.LocalReviewRequest) (review.Report, error) {
	f.localReq = &req
	for _, l := range f.progress {
		req.Options.OnProgress(l)
	}
	return f.report, f.err
}

func (f *fakeEngine) PreviewPR(_ context.Context, req port.PRRequest) (review.Report, error) {
	f.prReq = &req
	for _, l := range f.progress {
		req.Options.OnProgress(l)
	}
	return f.report, f.err
}

func (f *fakeEngine) PostPR(_ context.Context, req port.PRRequest) (port.PostResult, error) {
	f.postReq = &req
	return f.report, f.err
}

func sampleReport() review.Report {
	return review.Report{
		Findings: []review.Finding{{Severity: review.SeverityHigh, File: "a.go", Line: 3, Title: "nil deref"}},
		Meta:     review.Meta{Provider: "anthropic", CostUSD: 0.0632},
	}
}

// appPath is a local path every operating system accepts (an absolute path of
// this machine); the kinds validate stored paths with the same allowlist as the
// repository form.
func appPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(t.TempDir(), "App"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func repos(t *testing.T) fakeRepos {
	t.Helper()
	return fakeRepos{rows: map[int64]repo.Repository{
		1: {ID: 1, Owner: "Acme", Name: "App", LocalPath: appPath(t)},
		2: {ID: 2, Owner: "acme", Name: "docs"}, // no checkout
	}}
}

func localJob(repoID int64, spec job.LocalReviewSpec) job.Job {
	return job.Job{Kind: job.KindReviewLocal, Name: "j", Spec: spec.Marshal(), Mode: job.ModePreview, Targets: []int64{repoID}}
}

func prJob(repoID int64, n int, mode job.Mode) job.Job {
	return job.Job{Kind: job.KindReviewPR, Name: "j", Spec: job.PRReviewSpec{Number: n}.Marshal(), Mode: mode, Targets: []int64{repoID}}
}

func runErr(err error) *run.Error {
	var re *run.Error
	if errors.As(err, &re) {
		return re
	}
	return nil
}

func TestReviewLocalPlan(t *testing.T) {
	rs := repos(t)
	k := NewReviewLocal(rs, &fakeEngine{})
	k.goos = "linux"
	if k.Kind() != job.KindReviewLocal {
		t.Fatalf("Kind = %q", k.Kind())
	}

	tasks, err := k.Plan(context.Background(), localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged}))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Plan = %+v, %v", tasks, err)
	}
	tk := tasks[0]
	if tk.Kind != job.KindReviewLocal || tk.RepositoryID != 1 || tk.PRNumber != 0 || tk.Status != run.StatusQueued ||
		len(tk.LockKeys) != 1 || tk.LockKeys[0] != run.LockKeyForPath("linux", rs.rows[1].LocalPath) {
		t.Errorf("planned task = %+v", tk)
	}
	if spec, path, err := parseLocalTaskSpec(tk.Spec); err != nil || spec.Mode != job.LocalStaged || path != rs.rows[1].LocalPath {
		t.Errorf("task spec = %s (%v)", tk.Spec, err)
	}

	// No checkout: refused with its own code.
	_, err = k.Plan(context.Background(), localJob(2, job.LocalReviewSpec{Mode: job.LocalStaged}))
	if re := runErr(err); re == nil || re.Code != run.CodeLocalPathMissing {
		t.Errorf("repository without a path: %v", err)
	}
	// Unknown repository.
	_, err = k.Plan(context.Background(), localJob(99, job.LocalReviewSpec{Mode: job.LocalStaged}))
	if re := runErr(err); re == nil || re.Code != run.CodeRepositoryNotFound {
		t.Errorf("unknown repository: %v", err)
	}
	// A bad spec is a job error with the field.
	bad := localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged})
	bad.Spec = json.RawMessage(`{"mode":"range","range":["--output=x"]}`)
	var je *job.Error
	if _, err = k.Plan(context.Background(), bad); !errors.As(err, &je) || je.Field != "spec.range" {
		t.Errorf("bad spec: %v", err)
	}
	if err := k.Validate(bad.Spec); !errors.As(err, &je) {
		t.Errorf("Validate(bad spec) = %v", err)
	}
	// A job with two targets is not a local review job.
	two := localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged})
	two.Targets = []int64{1, 2}
	if _, err = k.Plan(context.Background(), two); !errors.As(err, &je) || je.Field != "targets" {
		t.Errorf("two targets: %v", err)
	}
}

func TestReviewLocalExecute(t *testing.T) {
	eng := &fakeEngine{report: sampleReport(), versionOK: true, progress: []string{"a", "b"}}
	rs := repos(t)
	k := NewReviewLocal(rs, eng)
	tasks, err := k.Plan(context.Background(), localJob(1, job.LocalReviewSpec{Mode: job.LocalRange, Range: []string{"main..HEAD"}}))
	if err != nil {
		t.Fatal(err)
	}

	var lines []string
	out, err := k.Execute(context.Background(), tasks[0], func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if eng.localReq == nil || eng.localReq.RepoPath != rs.rows[1].LocalPath || eng.localReq.Mode != port.LocalRange ||
		len(eng.localReq.Range) != 1 || eng.localReq.Range[0] != "main..HEAD" {
		t.Errorf("engine request = %+v", eng.localReq)
	}
	if eng.localReq.Options.Unattended {
		t.Error("a manual review must not run unattended")
	}
	if len(lines) != 2 {
		t.Errorf("progress lines = %v", lines)
	}
	if out.ExitCode == nil || *out.ExitCode != 0 || out.CLIVersion != "1.21.2" || out.CostUSD != 0.0632 || out.HeadOID != "" || out.Verdict != "" {
		t.Errorf("outcome = %+v", out)
	}
	var findings []review.Finding
	if err := json.Unmarshal(out.Findings, &findings); err != nil || len(findings) != 1 || findings[0].Title != "nil deref" {
		t.Errorf("findings = %s (%v)", out.Findings, err)
	}
	var rep review.Report
	if err := json.Unmarshal(out.Report, &rep); err != nil || rep.Meta.Provider != "anthropic" {
		t.Errorf("report = %s (%v)", out.Report, err)
	}

	// The mode reaches the engine for each local mode.
	for mode, want := range map[string]port.LocalMode{job.LocalStaged: port.LocalStaged, job.LocalUnstaged: port.LocalUnstaged} {
		tk, _ := k.Plan(context.Background(), localJob(1, job.LocalReviewSpec{Mode: mode}))
		if _, err := k.Execute(context.Background(), tk[0], func(string) {}); err != nil || eng.localReq.Mode != want {
			t.Errorf("mode %s -> %v (%v)", mode, eng.localReq.Mode, err)
		}
	}
}

func TestReviewLocalExecuteErrors(t *testing.T) {
	eng := &fakeEngine{versionOK: true}
	rs := repos(t)
	k := NewReviewLocal(rs, eng)
	tasks, _ := k.Plan(context.Background(), localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged}))
	tk := tasks[0]

	// A failure carries no outcome.
	eng.err = errcode.New(errcode.ProviderError, "")
	out, err := k.Execute(context.Background(), tk, func(string) {})
	if !errcode.Is(err, errcode.ProviderError) || out.Report != nil || out.ExitCode != nil {
		t.Errorf("failure = %+v, %v", out, err)
	}

	// A degraded review returns its report together with the error, and no
	// exit code.
	eng.err = errcode.New(errcode.Degraded, "")
	eng.report = review.Report{Content: "raw model text", Findings: []review.Finding{}, Meta: review.Meta{DegradeReason: "parse", CostUSD: 0.1}}
	out, err = k.Execute(context.Background(), tk, func(string) {})
	if !errcode.Is(err, errcode.Degraded) || len(out.Report) == 0 || out.ExitCode != nil || out.CostUSD != 0.1 || string(out.Findings) != "[]" {
		t.Errorf("degraded = %+v, %v", out, err)
	}

	// The repository vanished after queuing.
	gone := tk
	gone.RepositoryID = 99
	if _, err := k.Execute(context.Background(), gone, func(string) {}); !errcode.Is(err, errcode.NotARepo) {
		t.Errorf("deleted repository: %v", err)
	}
	// The checkout was removed from the repository after queuing.
	noPath := tk
	noPath.RepositoryID = 2
	if _, err := k.Execute(context.Background(), noPath, func(string) {}); !errcode.Is(err, errcode.NotARepo) {
		t.Errorf("repository without a path: %v", err)
	}
	// A garbled task spec.
	broken := tk
	broken.Spec = json.RawMessage(`{"mode":"nonsense"}`)
	if _, err := k.Execute(context.Background(), broken, func(string) {}); !errcode.Is(err, errcode.Unknown) {
		t.Errorf("bad task spec: %v", err)
	}
}

func TestReviewPRPlan(t *testing.T) {
	rs := repos(t)
	k := NewReviewPR(rs, &fakeEngine{})
	k.goos = "linux"
	if k.Kind() != job.KindReviewPR {
		t.Fatalf("Kind = %q", k.Kind())
	}

	tasks, err := k.Plan(context.Background(), prJob(1, 7, job.ModePreview))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Plan = %+v, %v", tasks, err)
	}
	tk := tasks[0]
	if tk.RepositoryID != 1 || tk.PRNumber != 7 || tk.Kind != job.KindReviewPR {
		t.Errorf("planned task = %+v", tk)
	}
	want := []string{run.LockKeyForPR("Acme/App", 7), run.LockKeyForPath("linux", rs.rows[1].LocalPath)}
	if len(tk.LockKeys) != 2 || tk.LockKeys[0] != want[0] || tk.LockKeys[1] != want[1] {
		t.Errorf("lock keys = %v, want %v", tk.LockKeys, want)
	}

	// Two plans for the same pull request share a lock, whatever the case of
	// the repository name; a repository without a checkout takes only the
	// pull request lock.
	again, _ := k.Plan(context.Background(), prJob(1, 7, job.ModePreview))
	if again[0].LockKeys[0] != tk.LockKeys[0] {
		t.Error("the same pull request got two different lock keys")
	}
	noPath, err := k.Plan(context.Background(), prJob(2, 7, job.ModePreview))
	if err != nil || len(noPath[0].LockKeys) != 1 || noPath[0].LockKeys[0] != run.LockKeyForPR("acme/docs", 7) {
		t.Errorf("no-checkout plan = %+v, %v", noPath, err)
	}

	for _, bad := range []job.Job{
		prJob(1, 0, job.ModePreview),
		{Kind: job.KindReviewPR, Name: "j", Spec: json.RawMessage(`{"number":1,"x":2}`), Mode: job.ModePreview, Targets: []int64{1}},
	} {
		var je *job.Error
		if _, err := k.Plan(context.Background(), bad); !errors.As(err, &je) {
			t.Errorf("bad job accepted: %v", err)
		}
	}
	if _, err := k.Plan(context.Background(), prJob(99, 7, job.ModePreview)); runErr(err) == nil || runErr(err).Code != run.CodeRepositoryNotFound {
		t.Errorf("unknown repository: %v", err)
	}
}

func TestReviewPRExecutePreview(t *testing.T) {
	eng := &fakeEngine{report: sampleReport(), versionOK: true}
	rs := repos(t)
	k := NewReviewPR(rs, eng)
	tasks, _ := k.Plan(context.Background(), prJob(1, 7, job.ModePreview))

	out, err := k.Execute(context.Background(), tasks[0], func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if eng.prReq == nil || eng.prReq.Repo != "Acme/App" || eng.prReq.Number != 7 || eng.prReq.LocalPath != rs.rows[1].LocalPath {
		t.Errorf("preview request = %+v", eng.prReq)
	}
	if eng.postReq != nil {
		t.Error("a preview must never call PostPR")
	}
	if out.ExitCode == nil || out.CLIVersion != "1.21.2" || len(out.Findings) == 0 {
		t.Errorf("outcome = %+v", out)
	}

	// A repository without a checkout runs with no local path.
	tasks, _ = k.Plan(context.Background(), prJob(2, 9, job.ModePreview))
	if _, err := k.Execute(context.Background(), tasks[0], func(string) {}); err != nil || eng.prReq.LocalPath != "" || eng.prReq.Repo != "acme/docs" {
		t.Errorf("no-checkout request = %+v (%v)", eng.prReq, err)
	}

	eng.err = errcode.New(errcode.GHNotFound, "")
	if _, err := k.Execute(context.Background(), tasks[0], func(string) {}); !errcode.Is(err, errcode.GHNotFound) {
		t.Errorf("gh missing: %v", err)
	}
}

// Post mode goes through PostPR, which answers capability_missing until phase 12;
// the run then ends as attention. A successful post carries the remote's head
// and verdict into the outcome.
func TestReviewPRExecutePost(t *testing.T) {
	eng := &fakeEngine{versionOK: true, err: errcode.New(errcode.CapabilityMissing, "")}
	rs := repos(t)
	k := NewReviewPR(rs, eng)
	tasks, _ := k.Plan(context.Background(), prJob(1, 7, job.ModePost))

	_, err := k.Execute(context.Background(), tasks[0], func(string) {})
	if !errcode.Is(err, errcode.CapabilityMissing) || eng.postReq == nil || eng.prReq != nil {
		t.Errorf("post without capability: %v (post %v, preview %v)", err, eng.postReq, eng.prReq)
	}
	if status, _ := run.Classify(err); status != run.StatusAttention {
		t.Errorf("capability_missing ended %s, want attention", status)
	}

	eng.err = nil
	eng.report = sampleReport()
	eng.report.Remote = &review.Remote{Status: review.RemoteSubmitted, HeadOID: "abc123", Verdict: review.VerdictRequestChanges}
	out, err := k.Execute(context.Background(), tasks[0], func(string) {})
	if err != nil || out.HeadOID != "abc123" || out.Verdict != "request-changes" {
		t.Errorf("posted outcome = %+v, %v", out, err)
	}
}

func TestReviewPRExecuteRefusesAGarbledSpec(t *testing.T) {
	rs := repos(t)
	k := NewReviewPR(rs, &fakeEngine{})
	for _, spec := range []string{`{}`, `{"number":7}`, `{"number":7,"mode":"yolo"}`, `nope`} {
		tk := run.Task{Kind: job.KindReviewPR, RepositoryID: 1, Spec: json.RawMessage(spec)}
		if _, err := k.Execute(context.Background(), tk, func(string) {}); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("spec %s: %v", spec, err)
		}
	}
}

// The task holds the lock of the path Plan saw. If the repository's path is
// edited while the task waits, running in the new path would let two reviews
// share a working tree under different locks: the task ends instead.
func TestAPathChangedWhileQueuedIsNotFollowed(t *testing.T) {
	ctx := context.Background()
	rs := repos(t)
	eng := &fakeEngine{report: sampleReport(), versionOK: true}
	local := NewReviewLocal(rs, eng)
	pr := NewReviewPR(rs, eng)
	localTasks, err := local.Plan(ctx, localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged}))
	if err != nil {
		t.Fatal(err)
	}
	prTasks, err := pr.Plan(ctx, prJob(1, 7, job.ModePreview))
	if err != nil {
		t.Fatal(err)
	}
	noPathTasks, err := pr.Plan(ctx, prJob(2, 7, job.ModePreview))
	if err != nil {
		t.Fatal(err)
	}

	// Same repository, other checkout; and a repository that gained a checkout.
	moved := rs.rows[1]
	moved.LocalPath = appPath(t)
	rs.rows[1] = moved
	gained := rs.rows[2]
	gained.LocalPath = appPath(t)
	rs.rows[2] = gained

	cases := map[string]error{}
	_, cases["local, path moved"] = local.Execute(ctx, localTasks[0], func(string) {})
	_, cases["pr, path moved"] = pr.Execute(ctx, prTasks[0], func(string) {})
	_, cases["pr, checkout added"] = pr.Execute(ctx, noPathTasks[0], func(string) {})
	for name, err := range cases {
		if !errcode.Is(err, errcode.NotARepo) {
			t.Errorf("%s: %v, want not_a_repo", name, err)
		}
	}
	if eng.localReq != nil || eng.prReq != nil {
		t.Errorf("the engine ran although the path changed: %+v %+v", eng.localReq, eng.prReq)
	}
}

// A stored path the allowlist refuses (a row from before the check, a hand
// edit, a restored backup) never reaches the adapter, which would stat it: on
// Windows that is a network authentication to the named host.
func TestAStoredNetworkPathNeverReachesTheEngine(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{`\\evil-host\share\repo`, `relative/path`, "/work/\x00bad"} {
		rs := fakeRepos{rows: map[int64]repo.Repository{1: {ID: 1, Owner: "Acme", Name: "App", LocalPath: bad}}}
		eng := &fakeEngine{report: sampleReport(), versionOK: true}
		local, pr := NewReviewLocal(rs, eng), NewReviewPR(rs, eng)

		// Plan refuses it before it becomes a lock key.
		if _, err := local.Plan(ctx, localJob(1, job.LocalReviewSpec{Mode: job.LocalStaged})); runErr(err) == nil || runErr(err).Code != run.CodeLocalPathMissing {
			t.Errorf("%q: local Plan = %v", bad, err)
		}
		if _, err := pr.Plan(ctx, prJob(1, 7, job.ModePreview)); runErr(err) == nil || runErr(err).Code != run.CodeLocalPathMissing {
			t.Errorf("%q: pr Plan = %v", bad, err)
		}

		// A task that was planned with a good path and whose row was damaged
		// afterwards (path equal to the planned one is impossible, so plan the
		// task by hand with the bad path, as an older build would have stored it).
		spec, _ := json.Marshal(localTaskSpec{Mode: job.LocalStaged, Path: bad})
		lt := run.Task{Kind: job.KindReviewLocal, RepositoryID: 1, Spec: spec}
		if _, err := local.Execute(ctx, lt, func(string) {}); !errcode.Is(err, errcode.NotARepo) {
			t.Errorf("%q: local Execute = %v", bad, err)
		}
		pspec, _ := json.Marshal(prTaskSpec{Number: 7, Mode: job.ModePreview, LocalPath: bad})
		pt := run.Task{Kind: job.KindReviewPR, RepositoryID: 1, PRNumber: 7, Spec: pspec}
		if _, err := pr.Execute(ctx, pt, func(string) {}); !errcode.Is(err, errcode.NotARepo) {
			t.Errorf("%q: pr Execute = %v", bad, err)
		}
		if eng.localReq != nil || eng.prReq != nil {
			t.Errorf("%q reached the engine", bad)
		}
	}
}
