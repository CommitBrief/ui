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

// ReviewLocal reviews the staged, unstaged or ranged changes of a local
// checkout: `commitbrief --json [--staged|--unstaged|diff <range>]` with the
// checkout as working directory. One job targets exactly one repository, which
// must have a local path.
type ReviewLocal struct {
	repos  port.RepoStore
	engine port.ReviewEngine
	// goos picks the case rules of the path lock key; runtime.GOOS outside tests.
	goos string
}

var _ port.JobKind = (*ReviewLocal)(nil)

// NewReviewLocal wires the kind.
func NewReviewLocal(repos port.RepoStore, engine port.ReviewEngine) *ReviewLocal {
	return &ReviewLocal{repos: repos, engine: engine, goos: runtime.GOOS}
}

// localTaskSpec is what Plan hands to Execute: the review spec plus the path the
// working-tree lock was taken for.
type localTaskSpec struct {
	Mode  string   `json:"mode"`
	Range []string `json:"range,omitempty"`
	Path  string   `json:"path"`
}

func parseLocalTaskSpec(raw json.RawMessage) (job.LocalReviewSpec, string, error) {
	var ts localTaskSpec
	if err := json.Unmarshal(raw, &ts); err != nil {
		return job.LocalReviewSpec{}, "", err
	}
	spec := job.LocalReviewSpec{Mode: ts.Mode, Range: ts.Range}
	return spec, ts.Path, spec.Validate()
}

// Kind implements port.JobKind.
func (*ReviewLocal) Kind() job.Kind { return job.KindReviewLocal }

// Validate implements port.JobKind.
func (*ReviewLocal) Validate(spec json.RawMessage) error {
	_, err := job.ParseLocalReviewSpec(spec)
	return err
}

// Plan implements port.JobKind: one task for the target repository. It holds
// the working-tree lock, so two reviews inside one checkout never overlap.
func (k *ReviewLocal) Plan(ctx context.Context, j job.Job) ([]run.Task, error) {
	spec, err := job.ParseLocalReviewSpec(j.Spec)
	if err != nil {
		return nil, err
	}
	r, err := lookupTarget(ctx, k.repos, j)
	if err != nil {
		return nil, err
	}
	if r.LocalPath == "" {
		return nil, run.NewError(run.CodeLocalPathMissing, "targets")
	}
	taskSpec, err := json.Marshal(localTaskSpec{Mode: spec.Mode, Range: spec.Range, Path: r.LocalPath})
	if err != nil {
		return nil, err
	}
	return []run.Task{{
		Kind:         job.KindReviewLocal,
		RepositoryID: r.ID,
		Spec:         taskSpec,
		Status:       run.StatusQueued,
		LockKeys:     []string{run.LockKeyForPath(k.goos, r.LocalPath)},
	}}, nil
}

// Execute implements port.JobKind.
func (k *ReviewLocal) Execute(ctx context.Context, t run.Task, progress func(string)) (run.Outcome, error) {
	spec, planned, err := parseLocalTaskSpec(t.Spec)
	if err != nil || planned == "" {
		return run.Outcome{}, errcode.Wrap(errcode.Unknown, "the task spec is not a local review", err)
	}
	r, err := executeRepo(ctx, k.repos, t, planned)
	if err != nil {
		return run.Outcome{}, err
	}
	if r.LocalPath == "" {
		return run.Outcome{}, errcode.New(errcode.NotARepo, "the repository has no local path")
	}
	req := port.LocalReviewRequest{
		RepoPath: r.LocalPath,
		Mode:     localMode(spec.Mode),
		Range:    spec.Range,
		Options:  port.RunOptions{OnProgress: progress},
	}
	rep, err := k.engine.ReviewLocal(ctx, req)
	return finish(ctx, k.engine, rep, err)
}

func localMode(m string) port.LocalMode {
	switch m {
	case job.LocalUnstaged:
		return port.LocalUnstaged
	case job.LocalRange:
		return port.LocalRange
	default:
		return port.LocalStaged
	}
}
