// SPDX-License-Identifier: GPL-3.0-or-later

package jobkinds

import (
	"context"
	"encoding/json"
	"runtime"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// prTaskSpec is what Plan hands to Execute: the pull request and whether the
// result is only kept (preview) or posted.
type prTaskSpec struct {
	Number int      `json:"number"`
	Mode   job.Mode `json:"mode"`
	// LocalPath is the checkout the working-tree lock was taken for; empty when
	// the repository had none (and no path lock was taken).
	LocalPath string `json:"localPath,omitempty"`
}

// ReviewPR reviews one pull request: `commitbrief remote pr N --repo o/r
// --no-post --json` in preview mode. It never posts: posting needs the recorded
// consent of UI-0008, which only the bot job carries, so a post job is refused
// in Plan (and again in Execute). One job targets exactly one
// repository; a local path, when the repository has one, is the working
// directory so the project's own rules apply.
type ReviewPR struct {
	repos  port.RepoStore
	engine port.ReviewEngine
	goos   string
}

var _ port.JobKind = (*ReviewPR)(nil)

// NewReviewPR wires the kind.
func NewReviewPR(repos port.RepoStore, engine port.ReviewEngine) *ReviewPR {
	return &ReviewPR{repos: repos, engine: engine, goos: runtime.GOOS}
}

// Kind implements port.JobKind.
func (*ReviewPR) Kind() job.Kind { return job.KindReviewPR }

// Validate implements port.JobKind.
func (*ReviewPR) Validate(spec json.RawMessage) error {
	_, err := job.ParsePRReviewSpec(spec)
	return err
}

// Plan implements port.JobKind: one task for the pull request. It holds the
// pull request lock (the same pull request is never reviewed twice at once) and,
// when the repository has a checkout, the working-tree lock as well.
func (k *ReviewPR) Plan(ctx context.Context, j job.Job) ([]run.Task, error) {
	spec, err := job.ParsePRReviewSpec(j.Spec)
	if err != nil {
		return nil, err
	}
	if err := spec.ValidateFor(j.Mode); err != nil {
		return nil, err
	}
	r, err := lookupTarget(ctx, k.repos, j)
	if err != nil {
		return nil, err
	}
	taskSpec, err := json.Marshal(prTaskSpec{Number: spec.Number, Mode: j.Mode, LocalPath: r.LocalPath})
	if err != nil {
		return nil, err
	}
	keys := []string{run.LockKeyForPR(r.Slug(), spec.Number)}
	if r.LocalPath != "" {
		keys = append(keys, run.LockKeyForPath(k.goos, r.LocalPath))
	}
	return []run.Task{{
		Kind:         job.KindReviewPR,
		RepositoryID: r.ID,
		PRNumber:     spec.Number,
		Spec:         taskSpec,
		Status:       run.StatusQueued,
		LockKeys:     keys,
	}}, nil
}

// Execute implements port.JobKind.
func (k *ReviewPR) Execute(ctx context.Context, t run.Task, progress func(string)) (run.Outcome, error) {
	var spec prTaskSpec
	if err := json.Unmarshal(t.Spec, &spec); err != nil || (job.PRReviewSpec{Number: spec.Number}).Validate() != nil || !spec.Mode.Valid() {
		return run.Outcome{}, errcode.New(errcode.Unknown, "the task spec is not a pull request review")
	}
	if (job.PRReviewSpec{}).ValidateFor(spec.Mode) != nil {
		return run.Outcome{}, errcode.New(errcode.Unknown, "a pull request review is a preview; posting needs the bot job's consent")
	}
	r, err := executeRepo(ctx, k.repos, t, spec.LocalPath)
	if err != nil {
		return run.Outcome{}, err
	}
	req := port.PRRequest{
		Repo:      r.Slug(),
		Number:    spec.Number,
		LocalPath: r.LocalPath,
		Options:   port.RunOptions{OnProgress: progress},
	}
	rep, err := k.engine.PreviewPR(ctx, req)
	return finish(ctx, k.engine, rep, err)
}
