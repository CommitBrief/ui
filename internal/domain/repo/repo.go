// SPDX-License-Identifier: GPL-3.0-or-later

// Package repo is the repository domain: the GitHub repositories the user
// wants reviewed, their validation rules and the closed set of error codes the
// frontend maps to localized text.
package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Limits on user supplied values.
const (
	MaxOwnerLen     = 100
	MaxNameLen      = 100
	MaxLocalPathLen = 4096
)

// ownerRe and nameRe are the shapes the CLI adapter accepts for `--repo
// owner/name` (clicmd/argv.go); a repository the UI stores must be one the
// adapter can later pass on.
var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Repository is one GitHub repository the UI reviews.
type Repository struct {
	ID    int64  `json:"id"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
	// LocalPath is the absolute path of a local checkout's root, or empty
	// when there is none. Without one a review runs with `--repo` and without
	// the project's own rules (the UI warns about it).
	LocalPath string    `json:"localPath"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

// Slug renders the repository as owner/name.
func (r Repository) Slug() string { return r.Owner + "/" + r.Name }

// Validate checks the shape of every user supplied field. It does not touch
// the file system: whether LocalPath exists and is a git root is checked by
// the application layer through its inspector port.
func (r Repository) Validate() error {
	if err := ValidateOwner(r.Owner); err != nil {
		return err
	}
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	if r.LocalPath != "" {
		return ValidateLocalPath(r.LocalPath)
	}
	return nil
}

// ValidateOwner checks a GitHub owner (user or organisation) login.
func ValidateOwner(owner string) error {
	if owner == "" || len(owner) > MaxOwnerLen || !ownerRe.MatchString(owner) {
		return &Error{Code: CodeOwnerInvalid, Field: "owner"}
	}
	return nil
}

// ValidateName checks a GitHub repository name.
func ValidateName(name string) error {
	if name == "" || len(name) > MaxNameLen || !nameRe.MatchString(name) || name == "." || name == ".." {
		return &Error{Code: CodeNameInvalid, Field: "name"}
	}
	return nil
}

// ValidateLocalPath checks the shape of a local checkout path: absolute, no
// NUL byte, bounded length. It returns nil for the empty string (no path).
func ValidateLocalPath(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > MaxLocalPathLen || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return &Error{Code: CodeLocalPathInvalid, Field: "localPath"}
	}
	return nil
}

// NormalizeLocalPath cleans a path that passed ValidateLocalPath
// (redundant separators, dot segments, trailing separator) without touching the
// file system. The empty string stays empty.
func NormalizeLocalPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

// Code is a member of the closed set of repository error codes. The backend
// hands only the code (and the offending field) to the frontend; the frontend
// owns the wording.
type Code string

const (
	CodeOwnerInvalid      Code = "repo_owner_invalid"
	CodeNameInvalid       Code = "repo_name_invalid"
	CodeLocalPathInvalid  Code = "repo_local_path_invalid"
	CodeLocalPathMissing  Code = "repo_local_path_missing"
	CodeLocalPathNotGit   Code = "repo_local_path_not_git"
	CodeDuplicate         Code = "repo_duplicate"
	CodeNotFound          Code = "repo_not_found"
	CodeNoRemote          Code = "repo_no_remote"
	CodeRemoteUnsupported Code = "repo_remote_unsupported"
	CodeImportInvalid     Code = "repo_import_invalid"
	CodeImportSchema      Code = "repo_import_unsupported_schema"
	CodeImportTooLarge    Code = "repo_import_too_large"
	CodeCanceled          Code = "canceled"
	CodeInternal          Code = "internal"
)

// Error is a classified repository error. It is what the Wails service hands
// to the frontend, serialized as {code, field, entry}; Err (the cause, which
// may contain local paths or driver text) never leaves the process.
type Error struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
	// Entry is the 1-based position of the offending record in an import
	// file, 0 when not applicable.
	Entry int   `json:"entry,omitempty"`
	Err   error `json:"-"`
}

// Error returns only the code (and field and entry), so that error text that
// crosses the process boundary is stable and free of paths.
func (e *Error) Error() string {
	s := string(e.Code)
	if e.Field != "" {
		s += " (" + e.Field + ")"
	}
	if e.Entry > 0 {
		s += fmt.Sprintf(" [entry %d]", e.Entry)
	}
	return s
}

// Unwrap exposes the cause for logs.
func (e *Error) Unwrap() error { return e.Err }

// Is matches by code: errors.Is(err, &repo.Error{Code: repo.CodeDuplicate}).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// New builds an *Error for a code.
func New(code Code) *Error { return &Error{Code: code} }

// Wrap builds an *Error for a code that keeps cause for logs.
func Wrap(code Code, cause error) *Error { return &Error{Code: code, Err: cause} }
