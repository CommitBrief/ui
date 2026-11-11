// SPDX-License-Identifier: GPL-3.0-or-later

package port

import "context"

// RemoteRef is the repository a git remote URL points at. Host is lower-case
// and carries no credentials or port.
type RemoteRef struct {
	Host  string
	Owner string
	Name  string
}

// LocalRepoInspector looks at a local checkout without running git.
//
// Both methods return *repo.Error values: repo_local_path_invalid,
// repo_local_path_missing and repo_local_path_not_git for the path itself,
// repo_no_remote and repo_remote_unsupported for the origin lookup.
type LocalRepoInspector interface {
	// CheckRoot verifies that path is an absolute path to an existing
	// directory that is the root of a git work tree.
	CheckRoot(ctx context.Context, path string) error
	// Origin returns the repository the checkout's "origin" remote points at.
	Origin(ctx context.Context, path string) (RemoteRef, error)
}
