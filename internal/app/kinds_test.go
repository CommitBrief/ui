// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/CommitBrief/ui/internal/domain/job"
)

func TestKindRegistry(t *testing.T) {
	a := &fakeKind{name: job.KindReviewLocal}
	b := &fakeKind{name: job.KindReviewPR}
	reg, err := NewKindRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reg.Get(job.KindReviewPR); !ok || got != b {
		t.Errorf("Get(review.pr) = %v, %v", got, ok)
	}
	if _, ok := reg.Get("bot.review_requested"); ok {
		t.Error("an unregistered kind was found")
	}
	if _, err := NewKindRegistry(a, &fakeKind{name: job.KindReviewLocal}); err == nil {
		t.Error("a duplicate kind was accepted")
	}
	if _, err := NewKindRegistry(&fakeKind{name: "NotAKind"}); err == nil {
		t.Error("an invalid kind name was accepted")
	}
}
