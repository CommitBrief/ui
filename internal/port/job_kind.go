// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"encoding/json"

	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
)

// JobKind is one type of job. The run service, its state machine, the
// scheduler and the history screen never look inside a job: everything that
// depends on the type lives behind this interface, so a new kind (the
// review-requested bot, in phase 12) is a new implementation and a form in the
// frontend, nothing else.
//
// An implementation is safe for concurrent use: Execute runs on several worker
// goroutines at once.
type JobKind interface {
	// Kind is the name the kind is registered under, for example
	// "review.local".
	Kind() job.Kind
	// Validate checks the kind-owned spec of a job definition. It returns a
	// *job.Error.
	Validate(spec json.RawMessage) error
	// Plan turns a job into the tasks of one run: it resolves the targets
	// (stored repositories, later pull requests found on GitHub) and sets each
	// task's spec and lock keys. It performs no review. It returns a *job.Error
	// for a bad definition or a *run.Error (repository not found, no local
	// checkout, ...) for a target it cannot use.
	Plan(ctx context.Context, j job.Job) ([]run.Task, error)
	// Execute runs one planned task and reports what came out of it. progress
	// receives the CLI's stderr line by line while it runs; it may be called
	// from another goroutine. Every error it returns is an *errcode.Error. A
	// non-nil outcome may accompany an error (a degraded review keeps its raw
	// model text); the run service stores whatever the outcome holds.
	Execute(ctx context.Context, t run.Task, progress func(line string)) (run.Outcome, error)
}

// EventEmitter delivers events to the frontend. The Wails adapter implements
// it; the application core only names the events (app/runs_events.go). Emit
// never blocks on a slow listener and never fails the caller.
type EventEmitter interface {
	Emit(name string, payload any)
}
