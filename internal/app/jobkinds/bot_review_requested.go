// SPDX-License-Identifier: GPL-3.0-or-later

package jobkinds

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"sort"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// trackedSource is the value of tracked_prs.source for pull requests the bot
// found through the review-requested search.
const trackedSource = "review_requested"

// warningSecretsDetected is the posting report's warning code for a secret scan
// hit (CLI ADR-0048); the CLI warns and posts anyway in posting mode.
const warningSecretsDetected = "secrets_detected"

// BotDeps are what the bot kind needs.
type BotDeps struct {
	Repos   port.RepoStore
	Tracked port.TrackedPRStore
	// Jobs is read right before a post task runs: the mode and the consent are
	// the job's as they are now, not as they were when the task was planned.
	Jobs port.JobStore
	// Source finds and watches the pull requests (gh).
	Source port.PRSource
	Engine port.ReviewEngine
	// Budget is asked right before a paid task starts; nil means no limit.
	Budget port.BudgetGate
	Clock  port.Clock
	// Logger receives planning diagnostics; nil means slog.Default().
	Logger *slog.Logger
}

// BotReviewRequested is the "review what is requested from me" bot (UI-0005,
// UI-0008). Planning is discovery: one combined GitHub search for the pull
// requests whose review is requested from the user, a tracked set that keeps
// following each of them until it closes (GitHub drops the user from the
// requested reviewers once a review is submitted), and one task for every
// tracked open pull request whose head moved since its last successful review.
// Unchanged heads create no task and never reach the CLI.
//
// Executing a task is a preview (`remote pr N --no-post --json`, nothing leaves
// the machine) or, only for a job with recorded consent, a post
// (`--since-last-review --max-verdict comment`: incremental, and never an
// approval).
type BotReviewRequested struct {
	d    BotDeps
	goos string
}

var _ port.JobKind = (*BotReviewRequested)(nil)

// NewBotReviewRequested wires the kind.
func NewBotReviewRequested(d BotDeps) *BotReviewRequested {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &BotReviewRequested{d: d, goos: runtime.GOOS}
}

// Kind implements port.JobKind.
func (*BotReviewRequested) Kind() job.Kind { return job.KindBotReviewRequested }

// Validate implements port.JobKind.
func (*BotReviewRequested) Validate(spec json.RawMessage) error {
	_, err := job.ParseBotReviewRequestedSpec(spec)
	return err
}

// botTaskSpec is what Plan hands to Execute.
type botTaskSpec struct {
	// JobID is the stored job that planned the task; the reviewed head is
	// recorded against it, and a post task re-reads it before it posts.
	JobID  int64    `json:"jobId,omitempty"`
	Number int      `json:"number"`
	Mode   job.Mode `json:"mode"`
	// HeadOID is the head Plan saw; a preview records it as reviewed (the report
	// of a preview has no head).
	HeadOID string `json:"headOid,omitempty"`
	// LocalPath is the checkout the working-tree lock was taken for.
	LocalPath string `json:"localPath,omitempty"`
	// Failure, when set, makes the task a report: planning could not do its
	// work (gh is not logged in, ...) and the task ends with this error code, so
	// the failure is recorded in the history like any other and the scheduler
	// classifies it the same way.
	Failure errcode.Code `json:"failure,omitempty"`
}

func (k *BotReviewRequested) failureTask(code errcode.Code) (run.Task, error) {
	spec, err := json.Marshal(botTaskSpec{Failure: code})
	if err != nil {
		return run.Task{}, err
	}
	return run.Task{Kind: job.KindBotReviewRequested, Spec: spec, Status: run.StatusQueued}, nil
}

// fatalDiscovery reports whether a discovery failure is one that makes every
// further gh call pointless.
func fatalDiscovery(code errcode.Code) bool {
	return code == errcode.GHNotFound || code == errcode.GHNotAuthenticated
}

// Plan implements port.JobKind. See the type comment for what it decides; this
// is the only place the bot talks to GitHub besides the review itself.
func (k *BotReviewRequested) Plan(ctx context.Context, j job.Job) ([]run.Task, error) {
	spec, err := job.ParseBotReviewRequestedSpec(j.Spec)
	if err != nil {
		return nil, err
	}
	if err := spec.ValidateFor(j.Mode); err != nil {
		return nil, err // post without the recorded consent never runs
	}
	if err := j.Validate(); err != nil {
		return nil, err
	}

	watched, err := k.watchedRepos(ctx, j)
	if err != nil {
		return nil, err
	}
	if len(watched) == 0 {
		return nil, nil
	}

	// A post job on a CLI that cannot post is a user-fixable state, and nothing
	// should be discovered (or tracked as seen) before it is fixed.
	if j.Mode == job.ModePost {
		caps, err := k.d.Engine.Capabilities(ctx)
		switch {
		case err != nil:
			return k.single(k.failureTask(codeOr(err, errcode.Unknown)))
		case !caps.Posting:
			return k.single(k.failureTask(errcode.CapabilityMissing))
		}
	}

	slugs := make([]string, 0, len(watched))
	bySlug := make(map[string]repo.Repository, len(watched))
	ids := make([]int64, 0, len(watched))
	for _, r := range watched {
		slugs = append(slugs, r.Slug())
		bySlug[strings.ToLower(r.Slug())] = r
		ids = append(ids, r.ID)
	}

	now := k.d.Clock.Now()
	var searchErr error
	refs, err := k.d.Source.SearchReviewRequested(ctx, slugs)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		code := codeOr(err, errcode.Unknown)
		k.d.Logger.Warn("bot: the review-requested search failed", "code", code, "error", err)
		if fatalDiscovery(code) {
			return k.single(k.failureTask(code))
		}
		// The tracked set does not depend on the search: keep following what is
		// already known, and report the failure at the end.
		searchErr = err
	}
	for _, ref := range refs {
		r, ok := bySlug[strings.ToLower(ref.Repo)]
		if !ok || (ref.IsDraft && !spec.IncludeDrafts) {
			continue
		}
		if err := k.d.Tracked.Track(ctx, r.ID, ref.Number, trackedSource, now); err != nil {
			return nil, err
		}
	}

	tracked, err := k.d.Tracked.ListOpen(ctx, j.ID, string(j.Mode), ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]repo.Repository, len(watched))
	for _, r := range watched {
		byID[r.ID] = r
	}
	var tasks []run.Task
	for _, tp := range tracked {
		r := byID[tp.RepositoryID]
		info, err := k.d.Source.ViewPR(ctx, r.Slug(), tp.Number)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			code := codeOr(err, errcode.Unknown)
			k.d.Logger.Warn("bot: reading a pull request failed", "repo", r.Slug(), "pr", tp.Number, "code", code, "error", err)
			if fatalDiscovery(code) {
				return k.single(k.failureTask(code))
			}
			continue // this one is tried again at the next slot
		}
		if info.State != port.PRStateOpen {
			if err := k.d.Tracked.SetState(ctx, r.ID, tp.Number, info.State); err != nil {
				return nil, err
			}
			continue // closed or merged: no longer followed
		}
		if info.IsDraft && !spec.IncludeDrafts {
			continue
		}
		if info.HeadOID == tp.LastHeadOID {
			continue // already reviewed at this head: no task, the CLI is not called
		}
		t, err := k.reviewTask(j, r, tp.Number, info.HeadOID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	sort.SliceStable(tasks, func(a, b int) bool {
		if tasks[a].RepositoryID != tasks[b].RepositoryID {
			return tasks[a].RepositoryID < tasks[b].RepositoryID
		}
		return tasks[a].PRNumber < tasks[b].PRNumber
	})

	if searchErr != nil {
		ft, err := k.failureTask(codeOr(searchErr, errcode.Unknown))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, ft)
	}
	return tasks, nil
}

func (k *BotReviewRequested) single(t run.Task, err error) ([]run.Task, error) {
	if err != nil {
		return nil, err
	}
	return []run.Task{t}, nil
}

// watchedRepos resolves the job's targets: repositories that were deleted or are
// switched off are not watched, and a stored path the allowlist refuses is never
// used (the repository is skipped rather than reviewed without its rules).
func (k *BotReviewRequested) watchedRepos(ctx context.Context, j job.Job) ([]repo.Repository, error) {
	var out []repo.Repository
	for _, id := range j.Targets {
		r, err := k.d.Repos.Get(ctx, id)
		switch {
		case errors.Is(err, port.ErrNotFound):
			continue
		case err != nil:
			return nil, err
		}
		if !r.Enabled {
			continue
		}
		if err := repo.ValidateLocalPath(r.LocalPath); err != nil {
			k.d.Logger.Warn("bot: skipping a repository whose stored local path is not acceptable", "repo", r.Slug())
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (k *BotReviewRequested) reviewTask(j job.Job, r repo.Repository, number int, head string) (run.Task, error) {
	spec, err := json.Marshal(botTaskSpec{JobID: j.ID, Number: number, Mode: j.Mode, HeadOID: head, LocalPath: r.LocalPath})
	if err != nil {
		return run.Task{}, err
	}
	keys := []string{run.LockKeyForPR(r.Slug(), number)}
	if r.LocalPath != "" {
		keys = append(keys, run.LockKeyForPath(k.goos, r.LocalPath))
	}
	return run.Task{
		Kind: job.KindBotReviewRequested, RepositoryID: r.ID, PRNumber: number, HeadOID: head,
		Spec: spec, Status: run.StatusQueued, LockKeys: keys,
	}, nil
}

// Execute implements port.JobKind.
func (k *BotReviewRequested) Execute(ctx context.Context, t run.Task, progress func(string)) (run.Outcome, error) {
	var spec botTaskSpec
	if err := json.Unmarshal(t.Spec, &spec); err != nil {
		return run.Outcome{}, errcode.New(errcode.Unknown, "the task spec is not a bot task")
	}
	if spec.Failure != "" {
		if !spec.Failure.Valid() {
			spec.Failure = errcode.Unknown
		}
		return run.Outcome{}, errcode.New(spec.Failure, "the bot could not discover pull requests")
	}
	if (job.PRReviewSpec{Number: spec.Number}).Validate() != nil || !spec.Mode.Valid() {
		return run.Outcome{}, errcode.New(errcode.Unknown, "the task spec is not a pull request review")
	}

	// The budget may have run out while the task waited in the queue.
	if k.d.Budget != nil {
		if over, err := k.d.Budget.Exceeded(ctx); err != nil {
			k.d.Logger.Warn("bot: reading the daily budget failed; running without it", "error", err)
		} else if over {
			return run.Outcome{}, errcode.New(errcode.CostAborted, "the daily budget is spent")
		}
	}

	if spec.Mode == job.ModePost {
		if err := k.postStillAllowed(ctx, spec.JobID); err != nil {
			return run.Outcome{}, err
		}
	}

	r, err := executeRepo(ctx, k.d.Repos, t, spec.LocalPath)
	if err != nil {
		return run.Outcome{}, err
	}
	req := port.PRRequest{
		Repo:      r.Slug(),
		Number:    spec.Number,
		LocalPath: r.LocalPath,
		// Nobody is there to answer a prompt; the UI enforces the budget itself.
		Options: port.RunOptions{Unattended: true, OnProgress: progress},
	}
	var rep review.Report
	if spec.Mode == job.ModePost {
		rep, err = k.d.Engine.PostPR(ctx, req)
	} else {
		rep, err = k.d.Engine.PreviewPR(ctx, req)
	}
	out, err := finish(ctx, k.d.Engine, rep, err)

	if out.HeadOID == "" {
		out.HeadOID = spec.HeadOID // a preview report names no head
	}
	switch {
	case err == nil:
		k.markReviewed(ctx, spec, r, out.HeadOID)
		if hasWarning(rep, warningSecretsDetected) {
			// The CLI posted anyway (UI-0008 risk): the review is recorded, and
			// the task is flagged so the user looks at it.
			return out, errcode.New(errcode.SecretDetected, "the secret scan flagged the diff; the review was posted")
		}
	case errcode.Is(err, errcode.SelfPR):
		// Never retried at this head: resuming the job must not loop on it.
		k.markReviewed(ctx, spec, r, spec.HeadOID)
	}
	return out, err
}

// postStillAllowed re-reads the job a queued post task belongs to. Plan checked
// the mode and the consent, but a run can wait in the queue for a long time, and
// switching the job to preview (which drops the consent), disabling its consent
// or deleting it must stop what is still queued: the remaining tasks end as
// canceled instead of posting under the user's name.
func (k *BotReviewRequested) postStillAllowed(ctx context.Context, jobID int64) error {
	denied := errcode.New(errcode.Canceled, "the job no longer allows posting; the review was not posted")
	if jobID == 0 || k.d.Jobs == nil {
		return denied // a post task always belongs to a stored job
	}
	rec, err := k.d.Jobs.GetJob(ctx, jobID)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return denied
	case err != nil:
		return errcode.Wrap(errcode.Unknown, "reading the job before posting", err)
	}
	if job.Mode(rec.Mode) != job.ModePost {
		return denied
	}
	spec, err := job.ParseBotReviewRequestedSpec(rec.Spec)
	if err != nil || spec.ValidateFor(job.ModePost) != nil {
		return denied
	}
	return nil
}

func (k *BotReviewRequested) markReviewed(ctx context.Context, spec botTaskSpec, r repo.Repository, head string) {
	number := spec.Number
	if head == "" {
		return
	}
	// The task context may be canceled by now (quitting); the row is still wanted.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbWait)
	defer cancel()
	if err := k.d.Tracked.MarkReviewed(wctx, spec.JobID, string(spec.Mode), r.ID, number, head, k.d.Clock.Now()); err != nil {
		k.d.Logger.Warn("bot: recording a reviewed head failed; the pull request will be reviewed again", "repo", r.Slug(), "pr", number, "error", err)
	}
}

func hasWarning(rep review.Report, code string) bool {
	if rep.Remote == nil {
		return false
	}
	for _, w := range rep.Remote.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// codeOr returns the failure class of err, or fallback when it carries none.
func codeOr(err error, fallback errcode.Code) errcode.Code {
	if c := errcode.CodeOf(err); c != "" && c != errcode.Unknown {
		return c
	}
	return fallback
}
