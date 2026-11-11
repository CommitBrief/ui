// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"time"
)

// GitHubHost is the only host the UI supports (UI-0005): repositories and pull
// requests live on github.com.
const GitHubHost = "github.com"

// Reasons an installed gh is not usable (AuthStatus.Problem).
const (
	// AuthProblemNotLoggedIn: gh has no account for the host.
	AuthProblemNotLoggedIn = "not_logged_in"
	// AuthProblemTokenInvalid: gh has an account, but its token no longer
	// works (revoked or expired).
	AuthProblemTokenInvalid = "token_invalid"
	// AuthProblemUnreachable: gh has an account but could not reach GitHub to
	// check it (offline, proxy, timeout), so whether the token works is not
	// known.
	AuthProblemUnreachable = "unreachable"
)

// AuthStatus is what `gh auth status` says about GitHubHost (UI-0005 section
// 1). The UI stores no GitHub token and never reads one: the status carries
// the account name and the token's scopes, nothing secret.
type AuthStatus struct {
	// Installed is false when no gh executable was found on the discovered
	// PATH; every other field is then empty.
	Installed bool `json:"installed"`
	// Path is the absolute path of the gh executable that was found.
	Path string `json:"path"`
	// Authenticated is true when the active account's token works.
	Authenticated bool `json:"authenticated"`
	// Host is the host the status is about.
	Host string `json:"host"`
	// Account is the login of the active account; empty when there is none.
	Account string `json:"account"`
	// Scopes are the OAuth scopes of the active account's token, when gh
	// printed them.
	Scopes []string `json:"scopes"`
	// Problem is AuthProblemNotLoggedIn, AuthProblemTokenInvalid or
	// AuthProblemUnreachable when gh is installed but not authenticated, else
	// empty.
	Problem string `json:"problem"`
}

// Pull request states as the tracked set and `gh pr view` report them.
const (
	PRStateOpen   = "open"
	PRStateClosed = "closed"
	PRStateMerged = "merged"
)

// PRRef is one pull request found by the review-requested search.
type PRRef struct {
	// Repo is "owner/name" of the base repository.
	Repo    string
	Number  int
	IsDraft bool
	// UpdatedAt is when GitHub last changed the pull request.
	UpdatedAt time.Time
	URL       string
}

// PRInfo is the current state of one pull request (`gh pr view`).
type PRInfo struct {
	// State is PRStateOpen, PRStateClosed or PRStateMerged.
	State string
	// HeadOID is the commit the pull request's head branch points at.
	HeadOID string
	IsDraft bool
}

// PRSource is what the UI needs from GitHub pull requests. The single
// implementation is internal/adapter/github/gh (UI-0005 section 4), which is
// the only place that runs `gh`.
//
// Every error an implementation returns is an *errcode.Error.
type PRSource interface {
	// AuthStatus reports whether gh is installed and logged in. A missing gh or
	// a missing login is a status, not an error; an error means the check
	// itself failed (canceled, timed out, unreadable output).
	AuthStatus(ctx context.Context) (AuthStatus, error)
	// SearchReviewRequested finds the open pull requests, in the given
	// repositories ("owner/name"), whose review is requested from the signed-in
	// user: one combined query, not one per repository (UI-0005 section 2). A
	// missing gh is gh_not_found, a missing login gh_not_authenticated.
	SearchReviewRequested(ctx context.Context, repos []string) ([]PRRef, error)
	// ViewPR reads the state and head commit of one pull request.
	ViewPR(ctx context.Context, repo string, number int) (PRInfo, error)
}
