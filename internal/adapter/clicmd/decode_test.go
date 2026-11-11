// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/review"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestContractGoldenChecksums fails when a copied golden file was edited, or
// replaced without updating testdata/README.md.
func TestContractGoldenChecksums(t *testing.T) {
	readme := string(readTestdata(t, "README.md"))
	for _, name := range []string{"v1.golden", "v1_findings.golden", "remote_pr_report.golden.json"} {
		sum := sha256.Sum256(readTestdata(t, name))
		want := hex.EncodeToString(sum[:])
		var row string
		for _, line := range strings.Split(readme, "\n") {
			if strings.HasPrefix(line, "| `"+name+"`") {
				row = line
			}
		}
		if row == "" {
			t.Errorf("%s: no row in testdata/README.md", name)
			continue
		}
		if !strings.Contains(row, want) {
			t.Errorf("%s: sha256 %s is not recorded in testdata/README.md; was the golden file edited or re-copied?", name, want)
		}
	}
}

// TestContractGoldenDecode decodes the CLI's own golden documents.
func TestContractGoldenDecode(t *testing.T) {
	t.Run("v1 clean review", func(t *testing.T) {
		rep, err := decodeReport(readTestdata(t, "v1.golden"))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Findings == nil || len(rep.Findings) != 0 {
			t.Errorf("Findings = %#v, want empty non-nil", rep.Findings)
		}
		if rep.Content != "# Review\n\nLooks good.\n" {
			t.Errorf("Content = %q", rep.Content)
		}
		m := rep.Meta
		if m.Provider != "anthropic" || m.Model != "claude-opus-4-7" || m.Lang != "en" {
			t.Errorf("meta identity = %+v", m)
		}
		if m.Usage != (review.Usage{InputTokens: 4231, OutputTokens: 1503, CachedInputTokens: 800}) {
			t.Errorf("usage = %+v", m.Usage)
		}
		if m.CostUSD != 0.0632 || m.LatencyMS != 3200 || m.Cached {
			t.Errorf("cost/latency/cached = %v %v %v", m.CostUSD, m.LatencyMS, m.Cached)
		}
		if want := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC); !m.Timestamp.Equal(want) {
			t.Errorf("timestamp = %v, want %v", m.Timestamp, want)
		}
		if rep.Degraded() || rep.Remote != nil {
			t.Errorf("Degraded=%v Remote=%v, want false/nil", rep.Degraded(), rep.Remote)
		}
	})

	t.Run("v1 with a finding", func(t *testing.T) {
		rep, err := decodeReport(readTestdata(t, "v1_findings.golden"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Findings) != 1 {
			t.Fatalf("findings = %d, want 1", len(rep.Findings))
		}
		f := rep.Findings[0]
		want := review.Finding{
			Severity:    review.SeverityHigh,
			File:        "internal/auth/session.go",
			Line:        142,
			Title:       "SQL fragment built from request input",
			Description: "String concatenation feeds db.Query() directly.",
			Suggestion:  "Use a prepared statement.",
			RuleID:      "sec.injection",
			Fingerprint: "36d16a0897d8cd95b8efdf48aba7fce56f1ea31c705f465cb75a9847b246f0b5",
		}
		if f != want {
			t.Errorf("finding = %+v\nwant     %+v", f, want)
		}
		if got := rep.CountBySeverity()[review.SeverityHigh]; got != 1 {
			t.Errorf("CountBySeverity[high] = %d", got)
		}
	})

	t.Run("remote posting report", func(t *testing.T) {
		rep, err := decodeReport(readTestdata(t, "remote_pr_report.golden.json"))
		if err != nil {
			t.Fatal(err)
		}
		r := rep.Remote
		if r == nil {
			t.Fatal("Remote is nil")
		}
		if r.Status != review.RemoteSubmitted || r.Repo != "CommitBrief/web" || r.Number != 42 || r.HeadOID != "stable" {
			t.Errorf("remote identity = %+v", r)
		}
		if r.Verdict != review.VerdictComment || r.VerdictUncapped != review.VerdictApprove || r.MaxVerdict != review.VerdictComment {
			t.Errorf("verdicts = %q %q %q", r.Verdict, r.VerdictUncapped, r.MaxVerdict)
		}
		if r.Review.Mode != "full" || r.Review.FullReason != "not_requested" || r.Review.Attempts != 1 || !r.Review.MarkerWritten {
			t.Errorf("review = %+v", r.Review)
		}
		if r.Sync == nil || r.Sync.Mode != "synced" || r.Sync.Posted != 1 {
			t.Errorf("sync = %+v", r.Sync)
		}
		if r.Warnings == nil || len(r.Warnings) != 0 {
			t.Errorf("warnings = %#v, want empty non-nil", r.Warnings)
		}
		if rep.Meta.CostUSD != 200 || rep.Meta.Usage.InputTokens != 100 {
			t.Errorf("meta = %+v", rep.Meta)
		}
	})
}

func TestDecodeRefusesWhatItDoesNotUnderstand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "empty"},
		{"whitespace", "  \n", "empty"},
		{"not json", "commitbrief: boom", "not a JSON document"},
		{"no schema", `{"findings":[]}`, "missing schema"},
		{"newer schema", `{"schema":2,"findings":[]}`, "unsupported report schema 2"},
		{"negative schema", `{"schema":-1}`, "invalid report schema"},
		{"unknown severity", `{"schema":1,"findings":[{"severity":"blocker","file":"a","line":1}]}`, "unknown severity"},
		{"trailing garbage", `{"schema":1} {"schema":1}`, "not a JSON document"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeReport([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDecodeToleratesUnknownFieldsAndNull(t *testing.T) {
	const doc = `{
	  "schema": 1,
	  "future_top_level": {"x": 1},
	  "findings": [
	    {"severity": "LOW", "file": "a.go", "line": 3, "line_end": 5, "title": "t", "description": "d",
	     "suggestion": "s", "language": "go", "snippet": "+x", "brand_new_field": true}
	  ],
	  "meta": {"provider": "p", "degrade_reason": "prose", "retry_count": 1, "baselined": 2, "suppressed": 3,
	           "filtered_commits": 4, "timestamp": "not a time", "future_meta": 9},
	  "remote": {"status": "failed", "pr": {"repo": "o/r", "number": 7}, "warnings": [{"code": "secrets_detected", "count": 2}]}
	}`
	rep, err := decodeReport([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Findings[0]
	if f.Severity != review.SeverityLow || f.LineEnd != 5 || f.Language != "go" || f.Snippet != "+x" {
		t.Errorf("finding = %+v", f)
	}
	if !rep.Degraded() || rep.Meta.DegradeReason != "prose" || rep.Meta.RetryCount != 1 ||
		rep.Meta.Baselined != 2 || rep.Meta.Suppressed != 3 || rep.Meta.FilteredCommits != 4 {
		t.Errorf("meta = %+v", rep.Meta)
	}
	if !rep.Meta.Timestamp.IsZero() {
		t.Errorf("bad timestamp should decode to the zero time, got %v", rep.Meta.Timestamp)
	}
	if rep.Remote == nil || rep.Remote.Status != review.RemoteFailed || len(rep.Remote.Warnings) != 1 ||
		rep.Remote.Warnings[0].Code != "secrets_detected" || rep.Remote.Warnings[0].Count != 2 {
		t.Errorf("remote = %+v", rep.Remote)
	}
}

func TestDecodeNullFindingsBecomeEmptySlice(t *testing.T) {
	rep, err := decodeReport([]byte(`{"schema":1,"findings":null,"meta":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Findings == nil || len(rep.Findings) != 0 {
		t.Errorf("Findings = %#v, want empty non-nil", rep.Findings)
	}
}

func TestDecodeUpgradeReport(t *testing.T) {
	rep, err := decodeUpgrade([]byte(`{"current":"1.21.2","latest":"1.22.0","method":"homebrew","update_available":true,"action":"brew upgrade commitbrief"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Current != "1.21.2" || rep.Latest != "1.22.0" || rep.Method != "homebrew" || !rep.UpdateAvailable || rep.Action != "brew upgrade commitbrief" {
		t.Errorf("report = %+v", rep)
	}
	if _, err := decodeUpgrade([]byte("nope")); err == nil {
		t.Error("garbage decoded without error")
	}
}
