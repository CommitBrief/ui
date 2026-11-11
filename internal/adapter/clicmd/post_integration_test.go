// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package clicmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/port"
)

// TestIntegrationBotPostAgainstARealPullRequest drives the real posting mode
// (`remote pr N --json --since-last-review --max-verdict comment`) against a real
// pull request. It WRITES TO GITHUB under the identity gh is logged in with: it
// posts review comments and submits a review. It therefore never runs by
// accident, and only a maintainer runs it, on a test repository they chose:
//
//	COMMITBRIEF_UI_IT_POST=owner/name#42 \
//	  go test -tags integration ./internal/adapter/clicmd -run TestIntegrationBotPost -v
//
// Unlike the other integration tests it uses the real environment: gh's login and
// the CLI's provider key live in the real home directory. It needs a CLI that
// ships ADR-0048 (v1.22.0 or later) and a structured-output (API) provider.
//
// What it checks, per run:
//   - the posting report is present and the verdict is never "approve" (the
//     ceiling is "comment"), including when the review has no findings;
//   - with COMMITBRIEF_UI_IT_EXPECT_INCREMENTAL=1 the run must be an incremental
//     review (or "no_new_commits"): the maintainer runs the test once, pushes a
//     commit to the pull request, then runs it again with that variable set, and
//     once more after a second push for the "two consecutive pushes" case.
func TestIntegrationBotPostAgainstARealPullRequest(t *testing.T) {
	target := os.Getenv("COMMITBRIEF_UI_IT_POST")
	if target == "" {
		t.Skip("set COMMITBRIEF_UI_IT_POST=owner/name#N to run; this test writes to GitHub")
	}
	repoSlug, num, ok := strings.Cut(target, "#")
	n, err := strconv.Atoi(num)
	if !ok || err != nil {
		t.Fatalf("COMMITBRIEF_UI_IT_POST must look like owner/name#42, got %q", target)
	}

	dataDir := filepath.Join(t.TempDir(), "ui-data")
	eng := New(Config{DataDir: dataDir, DefaultTimeout: 10 * time.Minute})
	ctx := context.Background()
	caps, err := eng.Capabilities(ctx)
	if err != nil {
		if errcode.Is(err, errcode.CLINotFound) {
			t.Skipf("no commitbrief binary on this machine: %v", err)
		}
		t.Fatal(err)
	}
	if !caps.Posting {
		t.Skipf("commitbrief %s cannot post (needs %s or later)", caps.Version, MinVersion(CapPosting))
	}

	rep, err := eng.PostPR(ctx, port.PRRequest{Repo: repoSlug, Number: n, Options: port.RunOptions{Unattended: true}})
	if err != nil {
		t.Fatalf("PostPR: %v", err)
	}
	r := rep.Remote
	if r == nil {
		t.Fatal("no posting report")
	}
	t.Logf("status=%s verdict=%s uncapped=%s review=%s/%s findings=%d head=%s",
		r.Status, r.Verdict, r.VerdictUncapped, r.Review.Mode, r.Review.FullReason, len(rep.Findings), r.HeadOID)

	if r.Verdict == review.VerdictApprove {
		t.Errorf("the bot submitted an approval; the ceiling must be %q", review.VerdictComment)
	}
	if r.MaxVerdict != review.VerdictComment {
		t.Errorf("max verdict = %q, want %q", r.MaxVerdict, review.VerdictComment)
	}
	if os.Getenv("COMMITBRIEF_UI_IT_EXPECT_INCREMENTAL") == "1" &&
		r.Status != review.RemoteNoNewCommits && r.Review.Mode != "incremental" {
		t.Errorf("review mode = %q (%s), want an incremental review after a push", r.Review.Mode, r.Review.FullReason)
	}
}
