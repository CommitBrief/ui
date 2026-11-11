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
	Severity    Severity `json:"severity"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	LineEnd     int      `json:"lineEnd"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Suggestion  string   `json:"suggestion"`
	Language    string   `json:"language"`
	Snippet     string   `json:"snippet"`
	RuleID      string   `json:"ruleId"`
	Fingerprint string   `json:"fingerprint"`
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
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	CachedInputTokens int `json:"cachedInputTokens"`
}

// Meta describes how a review was produced.
type Meta struct {
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Lang            string    `json:"lang"`
	Usage           Usage     `json:"usage"`
	CostUSD         float64   `json:"costUsd"`
	LatencyMS       int64     `json:"latencyMs"`
	Cached          bool      `json:"cached"`
	Timestamp       time.Time `json:"timestamp"`
	Baselined       int       `json:"baselined"`
	Suppressed      int       `json:"suppressed"`
	RetryCount      int       `json:"retryCount"`
	DegradeReason   string    `json:"degradeReason"`
	FilteredCommits int       `json:"filteredCommits"`
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
	Mode          string `json:"mode"` // full | incremental | none
	FullReason    string `json:"fullReason"`
	PreviousOID   string `json:"previousOid"`
	Attempts      int    `json:"attempts"`
	MarkerWritten bool   `json:"markerWritten"`
}

// RemoteSync counts what the comment-sync step did.
type RemoteSync struct {
	Mode       string `json:"mode"` // synced | post_only | skipped
	Posted     int    `json:"posted"`
	Updated    int    `json:"updated"`
	Unchanged  int    `json:"unchanged"`
	Resolved   int    `json:"resolved"`
	Replied    int    `json:"replied"`
	Held       int    `json:"held"`
	PostFailed int    `json:"postFailed"`
	Unanchored int    `json:"unanchored"`
}

// Warning is one machine-readable warning of a posting-mode run. Code is an
// open set.
type Warning struct {
	Code   string `json:"code"`
	Op     string `json:"op"`
	Ref    string `json:"ref"`
	Count  int    `json:"count"`
	Detail string `json:"detail"`
}

// Remote is the ADR-0048 report of a posting-mode `remote pr` run.
type Remote struct {
	Status          RemoteStatus `json:"status"`
	Repo            string       `json:"repo"`
	Number          int          `json:"number"`
	HeadOID         string       `json:"headOid"`
	Verdict         Verdict      `json:"verdict"`
	VerdictUncapped Verdict      `json:"verdictUncapped"`
	MaxVerdict      Verdict      `json:"maxVerdict"`
	Review          RemoteReview `json:"review"`
	Sync            *RemoteSync  `json:"sync,omitempty"`
	Warnings        []Warning    `json:"warnings"`
}

// Report is a complete review result. Findings is never nil. Content holds
// raw model text only when the review degraded. Remote is set only by a
// posting-mode run.
type Report struct {
	Content  string    `json:"content"`
	Findings []Finding `json:"findings"`
	Meta     Meta      `json:"meta"`
	Remote   *Remote   `json:"remote,omitempty"`
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
