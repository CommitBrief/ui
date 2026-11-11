// SPDX-License-Identifier: GPL-3.0-or-later

package run

// Code is a member of the closed set of error codes the run use cases hand to
// the frontend. The backend never sends text; the frontend owns the wording.
// The failure of a task is not one of these: it is stored on the task as an
// errcode.Code.
type Code string

const (
	// CodeJobInvalid: the job definition (or its spec) is not acceptable; the
	// Field names the offending part.
	CodeJobInvalid Code = "job_invalid"
	// CodeJobNotFound: no stored job has this ID.
	CodeJobNotFound Code = "job_not_found"
	// CodeJobKindUnknown: no implementation is registered for the kind.
	CodeJobKindUnknown Code = "job_kind_unknown"
	// CodeRepositoryNotFound: the target repository does not exist.
	CodeRepositoryNotFound Code = "run_repository_not_found"
	// CodeLocalPathMissing: a local review needs a repository with a local
	// checkout.
	CodeLocalPathMissing Code = "run_local_path_missing"
	// CodeQueueFull: too many tasks are waiting; try again later.
	CodeQueueFull Code = "run_queue_full"
	// CodeRunNotFound / CodeTaskNotFound: no such run or task.
	CodeRunNotFound  Code = "run_not_found"
	CodeTaskNotFound Code = "task_not_found"
	// CodeLogUnavailable: the task has no log (not started yet, or removed by
	// the retention policy).
	CodeLogUnavailable Code = "task_log_unavailable"
	// CodeShuttingDown: the application is quitting and takes no new work.
	CodeShuttingDown Code = "run_shutting_down"
	// CodeCanceled: the call was canceled by the frontend.
	CodeCanceled Code = "canceled"
	// CodeInternal: anything unexpected; details are in the log only.
	CodeInternal Code = "internal"
)

// Error is a classified run-service error, serialized as {code, field}. Err
// (the cause, which may hold paths) never leaves the process.
type Error struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
	Err   error  `json:"-"`
}

// Error returns only the code and the field.
func (e *Error) Error() string {
	if e.Field != "" {
		return string(e.Code) + " (" + e.Field + ")"
	}
	return string(e.Code)
}

// Unwrap exposes the cause for logs.
func (e *Error) Unwrap() error { return e.Err }

// Is matches by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// NewError builds an *Error for a code and an optional field.
func NewError(code Code, field string) *Error { return &Error{Code: code, Field: field} }

// WrapError builds an *Error that keeps cause for logs.
func WrapError(code Code, field string, cause error) *Error {
	return &Error{Code: code, Field: field, Err: cause}
}
