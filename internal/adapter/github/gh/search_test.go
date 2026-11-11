// SPDX-License-Identifier: GPL-3.0-or-later

package gh

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

const searchAnswer = `[
 {"number":12,"isDraft":false,"repository":{"name":"app","nameWithOwner":"Acme/App"},"updatedAt":"2026-11-11T08:30:00Z","url":"https://github.com/Acme/App/pull/12"},
 {"number":13,"isDraft":true,"repository":{"name":"docs","nameWithOwner":"acme/docs"},"updatedAt":"2026-11-11T09:00:00Z","url":"https://github.com/acme/docs/pull/13"},
 {"number":12,"isDraft":false,"repository":{"name":"app","nameWithOwner":"acme/app"},"updatedAt":"2026-11-11T08:30:00Z","url":"dup"},
 {"number":99,"isDraft":false,"repository":{"name":"other","nameWithOwner":"evil/other"},"updatedAt":"2026-11-11T08:30:00Z","url":"x"},
 {"number":0,"isDraft":false,"repository":{"name":"app","nameWithOwner":"acme/app"},"updatedAt":"2026-11-11T08:30:00Z","url":"x"}
]`

func TestSearchReviewRequestedIsOneCombinedQuery(t *testing.T) {
	h := newHarness(t, true)
	h.set("FAKEGH_STDOUT", strings.ReplaceAll(searchAnswer, "\n", " "))

	got, err := h.client.SearchReviewRequested(context.Background(), []string{"Acme/App", "acme/docs"})
	if err != nil {
		t.Fatal(err)
	}
	want := []port.PRRef{
		{Repo: "Acme/App", Number: 12, UpdatedAt: time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC), URL: "https://github.com/Acme/App/pull/12"},
		{Repo: "acme/docs", Number: 13, IsDraft: true, UpdatedAt: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC), URL: "https://github.com/acme/docs/pull/13"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %+v\nwant   %+v\n(case-insensitive duplicates, repositories nobody asked for and number 0 are dropped)", got, want)
	}

	rec := h.lastCall()
	wantArgs := []string{
		"search", "prs", "--review-requested=@me", "--state=open", "--limit=200",
		"--json=number,repository,isDraft,updatedAt,url", "--repo=Acme/App", "--repo=acme/docs",
	}
	if !reflect.DeepEqual(rec.Args, wantArgs) {
		t.Errorf("argv = %v\nwant   %v", rec.Args, wantArgs)
	}
	if rec.Env["GH_PROMPT_DISABLED"] != "1" {
		t.Errorf("gh must not prompt: %v", rec.Env)
	}
}

func TestSearchReviewRequestedWithNoRepositoriesDoesNotCallGH(t *testing.T) {
	h := newHarness(t, true)
	_ = os.Remove(h.argvLog)
	got, err := h.client.SearchReviewRequested(context.Background(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v", got, err)
	}
	if _, err := os.Stat(h.argvLog); err == nil {
		t.Error("gh was started for an empty repository list")
	}
}

func TestSearchRefusesAnythingThatCouldBeAnOption(t *testing.T) {
	h := newHarness(t, true)
	for _, bad := range []string{"--help/x", "-x/y", "a/b c", "a/b;rm", "a", "a/b/c", "a/..", "/b", "a/"} {
		_ = os.Remove(h.argvLog)
		_, err := h.client.SearchReviewRequested(context.Background(), []string{"ok/repo", bad})
		if !errcode.Is(err, errcode.Unknown) {
			t.Errorf("%q: err = %v, want a refusal", bad, err)
		}
		if _, statErr := os.Stat(h.argvLog); statErr == nil {
			t.Errorf("%q: gh was started", bad)
		}
	}
	many := make([]string, maxSearchRepos+1)
	for i := range many {
		many[i] = "o/r"
	}
	if _, err := h.client.SearchReviewRequested(context.Background(), many); !errcode.Is(err, errcode.Unknown) {
		t.Errorf("too many repositories: %v", err)
	}
}

func TestSearchFailuresAreClassified(t *testing.T) {
	ctx := context.Background()
	repos := []string{"acme/app"}

	if _, err := newHarness(t, false).client.SearchReviewRequested(ctx, repos); !errcode.Is(err, errcode.GHNotFound) {
		t.Errorf("no gh: %v", err)
	}

	for name, stderr := range map[string]string{
		"not logged in": "To get started with GitHub CLI, please run:  gh auth login",
		"bad token":     "HTTP 401: Bad credentials (https://api.github.com/graphql)",
		"env token":     "The token in GH_TOKEN is invalid.",
	} {
		h := newHarness(t, true)
		h.set("FAKEGH_EXIT", "1", "FAKEGH_STDERR", stderr)
		if _, err := h.client.SearchReviewRequested(ctx, repos); !errcode.Is(err, errcode.GHNotAuthenticated) {
			t.Errorf("%s: %v, want gh_not_authenticated", name, err)
		}
	}

	h := newHarness(t, true)
	h.set("FAKEGH_EXIT", "1", "FAKEGH_STDERR", "HTTP 403: API rate limit exceeded")
	_, err := h.client.SearchReviewRequested(ctx, repos)
	var ce *errcode.Error
	if !errcode.Is(err, errcode.Unknown) || !asCode(err, &ce) || !strings.Contains(ce.Detail, "rate limit") {
		t.Errorf("rate limit: %v (detail kept for the log)", err)
	}

	h = newHarness(t, true, func(c *Config) { c.Timeout = 200 * time.Millisecond })
	h.set("FAKEGH_SLEEP_MS", "5000", "FAKEGH_STDOUT", "[]")
	if _, err := h.client.SearchReviewRequested(ctx, repos); !errcode.Is(err, errcode.Timeout) {
		t.Errorf("slow gh: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newHarness(t, true).client.SearchReviewRequested(cctx, repos); !errcode.Is(err, errcode.Canceled) {
		t.Errorf("canceled: %v", err)
	}

	h = newHarness(t, true)
	h.set("FAKEGH_STDOUT", "not json")
	if _, err := h.client.SearchReviewRequested(ctx, repos); !errcode.Is(err, errcode.Unknown) {
		t.Errorf("garbage: %v", err)
	}
}

func asCode(err error, target **errcode.Error) bool { return errors.As(err, target) }

func TestViewPRReadsStateAndHead(t *testing.T) {
	ctx := context.Background()
	const oid = "0123456789abcdef0123456789abcdef01234567"
	for state, want := range map[string]string{"OPEN": port.PRStateOpen, "CLOSED": port.PRStateClosed, "MERGED": port.PRStateMerged} {
		h := newHarness(t, true)
		h.set("FAKEGH_STDOUT", `{"state":"`+state+`","headRefOid":"`+oid+`","isDraft":true}`)
		got, err := h.client.ViewPR(ctx, "acme/app", 7)
		if err != nil || got != (port.PRInfo{State: want, HeadOID: oid, IsDraft: true}) {
			t.Errorf("%s: %+v, %v", state, got, err)
		}
		if want := []string{"pr", "view", "7", "--repo=acme/app", "--json=state,headRefOid,isDraft"}; !reflect.DeepEqual(h.lastCall().Args, want) {
			t.Errorf("argv = %v", h.lastCall().Args)
		}
	}

	h := newHarness(t, true)
	for name, out := range map[string]string{
		"unknown state": `{"state":"LIMBO","headRefOid":"` + oid + `"}`,
		"no head":       `{"state":"OPEN","headRefOid":""}`,
		"odd head":      `{"state":"OPEN","headRefOid":"--oops"}`,
		"not json":      `<html>`,
	} {
		h.set("FAKEGH_STDOUT", out)
		if _, err := h.client.ViewPR(ctx, "acme/app", 7); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("%s: %v", name, err)
		}
	}

	for _, n := range []int{0, -1, maxPRNumber + 1} {
		if _, err := h.client.ViewPR(ctx, "acme/app", n); !errcode.Is(err, errcode.Unknown) {
			t.Errorf("number %d: %v", n, err)
		}
	}
	if _, err := h.client.ViewPR(ctx, "--x/y", 1); !errcode.Is(err, errcode.Unknown) {
		t.Errorf("option-like repository: %v", err)
	}
	if _, err := newHarness(t, false).client.ViewPR(ctx, "acme/app", 1); !errcode.Is(err, errcode.GHNotFound) {
		t.Errorf("no gh: %v", err)
	}
	h.set("FAKEGH_EXIT", "1", "FAKEGH_STDERR", "GraphQL: Could not resolve to a PullRequest with the number of 7.")
	if _, err := h.client.ViewPR(ctx, "acme/app", 7); !errcode.Is(err, errcode.Unknown) {
		t.Errorf("missing PR: %v", err)
	}
}
