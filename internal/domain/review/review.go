// SPDX-License-Identifier: GPL-3.0-or-later

// Package review holds the UI's own shape of a code review result. It mirrors
// the CLI's JSON schema v1 (and the ADR-0048 `remote` container) but is not
// tied to it: adapters translate, the rest of the UI depends only on these
// types (UI-0004 section 1).
package review

import "time"

// Severity is a finding's severity, ordered critical (most severe) to info.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// Valid reports whether s is one of the five severities.
func (s Severity) Valid() bool { return s.Rank() >= 0 }

// Rank is 0 for critical up to 4 for info, and -1 for an unknown value. Lower
// means more severe.
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	case SeverityInfo:
		return 4
	default:
		return -1
	}
}

// Finding is one review finding. Line is 1-based; LineEnd is 0 unless the
// finding spans several lines.
type Finding struct {
	Severity    Severity
	File        string
	Line        int
	LineEnd     int
	Title       string
	Description string
	Suggestion  string
	Language    string
	Snippet     string
	RuleID      string
	Fingerprint string
}

// Verdict is a GitHub review verdict.
type Verdict string

const (
	VerdictApprove        Verdict = "approve"
	VerdictComment        Verdict = "comment"
	VerdictRequestChanges Verdict = "request-changes"
)

// Usage is the token accounting of a review.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// Meta describes how a review was produced.
type Meta struct {
	Provider        string
	Model           string
	Lang            string
	Usage           Usage
	CostUSD         float64
	LatencyMS       int64
	Cached          bool
	Timestamp       time.Time
	Baselined       int
	Suppressed      int
	RetryCount      int
	DegradeReason   string
	FilteredCommits int
}

// RemoteStatus is how a posting-mode `remote pr` run ended.
type RemoteStatus string

const (
	RemoteSubmitted    RemoteStatus = "submitted"
	RemoteNoNewCommits RemoteStatus = "no_new_commits"
	RemoteDegraded     RemoteStatus = "degraded"
	RemoteTooVolatile  RemoteStatus = "too_volatile"
	RemoteFailed       RemoteStatus = "failed"
)

// RemoteReview describes the review attempt of a posting-mode run.
type RemoteReview struct {
	Mode          string // full | incremental | none
	FullReason    string
	PreviousOID   string
	Attempts      int
	MarkerWritten bool
}

// RemoteSync counts what the comment-sync step did.
type RemoteSync struct {
	Mode       string // synced | post_only | skipped
	Posted     int
	Updated    int
	Unchanged  int
	Resolved   int
	Replied    int
	Held       int
	PostFailed int
	Unanchored int
}

// Warning is one machine-readable warning of a posting-mode run. Code is an
// open set.
type Warning struct {
	Code   string
	Op     string
	Ref    string
	Count  int
	Detail string
}

// Remote is the ADR-0048 report of a posting-mode `remote pr` run.
type Remote struct {
	Status          RemoteStatus
	Repo            string
	Number          int
	HeadOID         string
	Verdict         Verdict
	VerdictUncapped Verdict
	MaxVerdict      Verdict
	Review          RemoteReview
	Sync            *RemoteSync
	Warnings        []Warning
}

// Report is a complete review result. Findings is never nil. Content holds
// raw model text only when the review degraded. Remote is set only by a
// posting-mode run.
type Report struct {
	Content  string
	Findings []Finding
	Meta     Meta
	Remote   *Remote
}

// Degraded reports whether the provider answer could not be parsed into
// findings, so Findings is empty because nothing was recovered, not because
// the code is clean.
func (r Report) Degraded() bool { return r.Meta.DegradeReason != "" }

// CountBySeverity returns how many findings carry each severity.
func (r Report) CountBySeverity() map[Severity]int {
	out := make(map[Severity]int, 5)
	for _, f := range r.Findings {
		out[f.Severity]++
	}
	return out
}
