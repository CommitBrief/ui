// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/port"
)

// supportedSchema is the highest `schema` this adapter understands. A consumer
// must refuse a higher version and tolerate unknown fields (json-schema-v1.md).
const supportedSchema = 1

// The wire types below are the UI's own copy of the CLI's JSON schema v1 and of
// the ADR-0048 `remote` container. They are checked against the CLI's golden
// file by TestContractGoldenDecode; nothing from the CLI is imported (UI-0004).

type wireDocument struct {
	Schema   int           `json:"schema"`
	Content  string        `json:"content"`
	Findings []wireFinding `json:"findings"`
	Meta     wireMeta      `json:"meta"`
	Remote   *wireRemote   `json:"remote"`
}

type wireFinding struct {
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	LineEnd     int    `json:"line_end"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
	Language    string `json:"language"`
	Snippet     string `json:"snippet"`
	RuleID      string `json:"rule_id"`
	Fingerprint string `json:"fingerprint"`
}

type wireUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

type wireMeta struct {
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Lang            string    `json:"lang"`
	Usage           wireUsage `json:"usage"`
	CostUSD         float64   `json:"cost_usd"`
	LatencyMS       int64     `json:"latency_ms"`
	Cached          bool      `json:"cached"`
	Timestamp       string    `json:"timestamp"`
	Baselined       int       `json:"baselined"`
	Suppressed      int       `json:"suppressed"`
	RetryCount      int       `json:"retry_count"`
	DegradeReason   string    `json:"degrade_reason"`
	FilteredCommits int       `json:"filtered_commits"`
}

type wireRemote struct {
	Status string `json:"status"`
	PR     struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	} `json:"pr"`
	HeadOID         string `json:"head_oid"`
	Verdict         string `json:"verdict"`
	VerdictUncapped string `json:"verdict_uncapped"`
	MaxVerdict      string `json:"max_verdict"`
	Review          struct {
		Mode          string `json:"mode"`
		FullReason    string `json:"full_reason"`
		PreviousOID   string `json:"previous_oid"`
		Attempts      int    `json:"attempts"`
		MarkerWritten bool   `json:"marker_written"`
	} `json:"review"`
	Sync *struct {
		Mode       string `json:"mode"`
		Posted     int    `json:"posted"`
		Updated    int    `json:"updated"`
		Unchanged  int    `json:"unchanged"`
		Resolved   int    `json:"resolved"`
		Replied    int    `json:"replied"`
		Held       int    `json:"held"`
		PostFailed int    `json:"post_failed"`
		Unanchored int    `json:"unanchored"`
	} `json:"sync"`
	Warnings []struct {
		Code   string `json:"code"`
		Op     string `json:"op"`
		Ref    string `json:"ref"`
		Count  int    `json:"count"`
		Detail string `json:"detail"`
	} `json:"warnings"`
}

// decodeReport parses a JSON schema v1 document into the UI's domain type. It
// parses `schema` first and refuses a version it does not understand; unknown
// fields are ignored. Findings is never nil in the result.
func decodeReport(stdout []byte) (review.Report, error) {
	stdout = bytes.TrimSpace(stdout)
	if len(stdout) == 0 {
		return review.Report{}, errors.New("empty output")
	}

	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(stdout, &head); err != nil {
		return review.Report{}, fmt.Errorf("not a JSON document: %w", err)
	}
	switch {
	case head.Schema == 0:
		return review.Report{}, errors.New("not a CommitBrief report: missing schema field")
	case head.Schema > supportedSchema:
		return review.Report{}, fmt.Errorf("unsupported report schema %d (understood: %d)", head.Schema, supportedSchema)
	case head.Schema < 0:
		return review.Report{}, fmt.Errorf("invalid report schema %d", head.Schema)
	}

	var doc wireDocument
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return review.Report{}, fmt.Errorf("decode report: %w", err)
	}

	rep := review.Report{
		Content:  doc.Content,
		Findings: make([]review.Finding, 0, len(doc.Findings)),
		Meta: review.Meta{
			Provider: doc.Meta.Provider,
			Model:    doc.Meta.Model,
			Lang:     doc.Meta.Lang,
			Usage: review.Usage{
				InputTokens:       doc.Meta.Usage.InputTokens,
				OutputTokens:      doc.Meta.Usage.OutputTokens,
				CachedInputTokens: doc.Meta.Usage.CachedInputTokens,
			},
			CostUSD:         doc.Meta.CostUSD,
			LatencyMS:       doc.Meta.LatencyMS,
			Cached:          doc.Meta.Cached,
			Timestamp:       parseTimestamp(doc.Meta.Timestamp),
			Baselined:       doc.Meta.Baselined,
			Suppressed:      doc.Meta.Suppressed,
			RetryCount:      doc.Meta.RetryCount,
			DegradeReason:   doc.Meta.DegradeReason,
			FilteredCommits: doc.Meta.FilteredCommits,
		},
	}
	for i, f := range doc.Findings {
		sev := review.Severity(strings.ToLower(strings.TrimSpace(f.Severity)))
		if !sev.Valid() {
			// The severity set is closed: a new value would be a schema bump.
			return review.Report{}, fmt.Errorf("finding %d: unknown severity %q", i, f.Severity)
		}
		rep.Findings = append(rep.Findings, review.Finding{
			Severity:    sev,
			File:        f.File,
			Line:        f.Line,
			LineEnd:     f.LineEnd,
			Title:       f.Title,
			Description: f.Description,
			Suggestion:  f.Suggestion,
			Language:    f.Language,
			Snippet:     f.Snippet,
			RuleID:      f.RuleID,
			Fingerprint: f.Fingerprint,
		})
	}
	if doc.Remote != nil {
		rep.Remote = convertRemote(doc.Remote)
	}
	return rep, nil
}

func convertRemote(w *wireRemote) *review.Remote {
	r := &review.Remote{
		Status:          review.RemoteStatus(w.Status),
		Repo:            w.PR.Repo,
		Number:          w.PR.Number,
		HeadOID:         w.HeadOID,
		Verdict:         review.Verdict(w.Verdict),
		VerdictUncapped: review.Verdict(w.VerdictUncapped),
		MaxVerdict:      review.Verdict(w.MaxVerdict),
		Review: review.RemoteReview{
			Mode:          w.Review.Mode,
			FullReason:    w.Review.FullReason,
			PreviousOID:   w.Review.PreviousOID,
			Attempts:      w.Review.Attempts,
			MarkerWritten: w.Review.MarkerWritten,
		},
		Warnings: make([]review.Warning, 0, len(w.Warnings)),
	}
	if w.Sync != nil {
		r.Sync = &review.RemoteSync{
			Mode:       w.Sync.Mode,
			Posted:     w.Sync.Posted,
			Updated:    w.Sync.Updated,
			Unchanged:  w.Sync.Unchanged,
			Resolved:   w.Sync.Resolved,
			Replied:    w.Sync.Replied,
			Held:       w.Sync.Held,
			PostFailed: w.Sync.PostFailed,
			Unanchored: w.Sync.Unanchored,
		}
	}
	for _, x := range w.Warnings {
		r.Warnings = append(r.Warnings, review.Warning{Code: x.Code, Op: x.Op, Ref: x.Ref, Count: x.Count, Detail: x.Detail})
	}
	return r
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// wireUpgrade is `commitbrief upgrade --json`.
type wireUpgrade struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	Method          string `json:"method"`
	UpdateAvailable bool   `json:"update_available"`
	Action          string `json:"action"`
}

func decodeUpgrade(stdout []byte) (port.UpgradeReport, error) {
	var w wireUpgrade
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &w); err != nil {
		return port.UpgradeReport{}, fmt.Errorf("decode upgrade report: %w", err)
	}
	return port.UpgradeReport(w), nil
}
