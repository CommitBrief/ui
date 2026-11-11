// SPDX-License-Identifier: GPL-3.0-or-later

// Package jobkinds holds the job kinds that exist so far: a review of a local
// checkout and a review of one pull request. Each implements port.JobKind on
// top of port.ReviewEngine; nothing in the run service knows about them.
package jobkinds

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// lookupTarget loads the single target repository of a job. A repository that
// does not exist is a *run.Error the frontend can word.
func lookupTarget(ctx context.Context, repos port.RepoStore, j job.Job) (repo.Repository, error) {
	if err := j.Validate(); err != nil {
		return repo.Repository{}, err
	}
	r, err := repos.Get(ctx, j.Targets[0])
	switch {
	case errors.Is(err, port.ErrNotFound):
		return repo.Repository{}, run.NewError(run.CodeRepositoryNotFound, "targets")
	case err != nil:
		return repo.Repository{}, err
	}
	// A stored path that is not an acceptable local path (a row from before the
	// allowlist, a hand edit) is refused here, before it becomes a lock key.
	if err := repo.ValidateLocalPath(r.LocalPath); err != nil {
		return repo.Repository{}, run.NewError(run.CodeLocalPathMissing, "targets")
	}
	return r, nil
}

// executeRepo reloads the repository of a planned task. plannedPath is the local
// path Plan saw (empty for none): the task holds the exclusion lock of that path,
// so it runs in that path or not at all. A repository that was deleted, or whose
// path was edited, while the task waited ends as not_a_repo (attention): the
// user has to look. The path is also checked with the same allowlist that guards
// every write (repo.ValidateLocalPath) before the adapter touches the file
// system, so a damaged row can never make the machine reach a network path.
func executeRepo(ctx context.Context, repos port.RepoStore, t run.Task, plannedPath string) (repo.Repository, error) {
	r, err := repos.Get(ctx, t.RepositoryID)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return repo.Repository{}, errcode.New(errcode.NotARepo, "the repository was deleted after the task was queued")
	case err != nil:
		return repo.Repository{}, errcode.Wrap(errcode.Unknown, "reading the repository", err)
	}
	if r.LocalPath != plannedPath {
		return repo.Repository{}, errcode.New(errcode.NotARepo, "the repository's local path changed after the task was queued")
	}
	if err := repo.ValidateLocalPath(r.LocalPath); err != nil {
		return repo.Repository{}, errcode.Wrap(errcode.NotARepo, "the repository's local path is not acceptable", err)
	}
	return r, nil
}

// outcomeFrom turns a review report into the outcome stored on the task. exit0
// says the CLI finished normally (the report is then a success); a degraded
// review keeps its report but not an exit code. The CLI version is read
// best-effort from the engine (it is cached there).
func outcomeFrom(ctx context.Context, engine port.ReviewEngine, rep review.Report, exit0 bool) (run.Outcome, error) {
	if rep.Findings == nil {
		rep.Findings = []review.Finding{}
	}
	findings, err := json.Marshal(rep.Findings)
	if err != nil {
		return run.Outcome{}, errcode.Wrap(errcode.Unknown, "encoding the findings", err)
	}
	report, err := json.Marshal(rep)
	if err != nil {
		return run.Outcome{}, errcode.Wrap(errcode.Unknown, "encoding the report", err)
	}
	out := run.Outcome{
		Findings: findings,
		Report:   report,
		CostUSD:  rep.Meta.CostUSD,
	}
	if exit0 {
		zero := 0
		out.ExitCode = &zero
	}
	if rep.Remote != nil {
		out.HeadOID = rep.Remote.HeadOID
		out.Verdict = string(rep.Remote.Verdict)
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v, err := engine.Version(vctx); err == nil {
		out.CLIVersion = v.String()
	}
	return out, nil
}

// finish is the common tail of Execute: a report with no error is a success; a
// degraded review returns its report together with the error; any other error
// carries no report.
func finish(ctx context.Context, engine port.ReviewEngine, rep review.Report, err error) (run.Outcome, error) {
	switch {
	case err == nil:
		return outcomeFrom(ctx, engine, rep, true)
	case errcode.Is(err, errcode.Degraded):
		out, oerr := outcomeFrom(ctx, engine, rep, false)
		if oerr != nil {
			return run.Outcome{}, oerr
		}
		return out, err
	default:
		return run.Outcome{}, err
	}
}
