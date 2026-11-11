// SPDX-License-Identifier: GPL-3.0-or-later

package review

import (
	"encoding/json"
	"testing"
	"time"
)

// The stored report (tasks.report_json) and the frontend read these keys; they
// are part of the persisted format, so renaming a field is a migration.
func TestReportJSONKeys(t *testing.T) {
	rep := Report{
		Content:  "raw",
		Findings: []Finding{{Severity: SeverityHigh, File: "a.go", Line: 3, LineEnd: 5, Title: "t", RuleID: "r.x", Fingerprint: "f"}},
		Meta: Meta{
			Provider: "anthropic", CostUSD: 0.5, LatencyMS: 12, DegradeReason: "x",
			Usage: Usage{InputTokens: 1, OutputTokens: 2, CachedInputTokens: 3}, Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		Remote: &Remote{Status: RemoteSubmitted, HeadOID: "abc", Verdict: VerdictComment, VerdictUncapped: VerdictApprove, MaxVerdict: VerdictComment,
			Review: RemoteReview{Mode: "full", PreviousOID: "p"}, Sync: &RemoteSync{Mode: "synced", PostFailed: 1}, Warnings: []Warning{}},
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	has := func(m map[string]any, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Errorf("key %q missing from %v", k, m)
			}
		}
	}
	has(generic, "content", "findings", "meta", "remote")
	finding := generic["findings"].([]any)[0].(map[string]any)
	has(finding, "severity", "file", "line", "lineEnd", "title", "description", "suggestion", "language", "snippet", "ruleId", "fingerprint")
	meta := generic["meta"].(map[string]any)
	has(meta, "provider", "model", "costUsd", "latencyMs", "degradeReason", "usage", "timestamp", "filteredCommits")
	has(meta["usage"].(map[string]any), "inputTokens", "outputTokens", "cachedInputTokens")
	remote := generic["remote"].(map[string]any)
	has(remote, "status", "headOid", "verdict", "verdictUncapped", "maxVerdict", "review", "sync", "warnings")
	has(remote["review"].(map[string]any), "previousOid", "fullReason", "markerWritten")
	has(remote["sync"].(map[string]any), "postFailed")

	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Findings[0] != rep.Findings[0] || back.Meta.CostUSD != 0.5 || back.Remote == nil || back.Remote.HeadOID != "abc" || !back.Meta.Timestamp.Equal(rep.Meta.Timestamp) {
		t.Errorf("round trip = %+v", back)
	}

	// A preview report has no remote at all.
	b, _ = json.Marshal(Report{Findings: []Finding{}})
	var preview map[string]any
	_ = json.Unmarshal(b, &preview)
	if _, ok := preview["remote"]; ok {
		t.Errorf("a report without a remote marshals one: %s", b)
	}
}
