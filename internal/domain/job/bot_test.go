// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBotSpecPostNeedsConsent(t *testing.T) {
	var none BotReviewRequestedSpec
	if err := none.ValidateFor(ModePreview); err != nil {
		t.Errorf("preview without consent: %v", err)
	}
	if c, f := code(none.ValidateFor(ModePost)); c != CodeSpecInvalid || f != "spec.postConsentAt" {
		t.Errorf("post without consent = %q (%q)", c, f)
	}
	zero := time.Time{}
	if c, _ := code((BotReviewRequestedSpec{PostConsentAt: &zero}).ValidateFor(ModePost)); c != CodeSpecInvalid {
		t.Errorf("a zero consent time counts as none: %q", c)
	}
	now := time.Now()
	if err := (BotReviewRequestedSpec{PostConsentAt: &now}).ValidateFor(ModePost); err != nil {
		t.Errorf("post with consent: %v", err)
	}
}

func TestBotSpecRoundTripAndStrictDecoding(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	raw := BotReviewRequestedSpec{IncludeDrafts: true, PostConsentAt: &at}.Marshal()
	got, err := ParseBotReviewRequestedSpec(raw)
	if err != nil || !got.IncludeDrafts || got.PostConsentAt == nil || !got.PostConsentAt.Equal(at) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if s, err := ParseBotReviewRequestedSpec(json.RawMessage(`{}`)); err != nil || s.IncludeDrafts || s.PostConsentAt != nil {
		t.Errorf("empty spec = %+v, %v", s, err)
	}
	for _, bad := range []string{`{"includeDrafts":"yes"}`, `{"extra":1}`, `[]`, ``} {
		if c, _ := code(func() error { _, err := ParseBotReviewRequestedSpec(json.RawMessage(bad)); return err }()); c != CodeSpecInvalid {
			t.Errorf("spec %q: code %q", bad, c)
		}
	}
}

func TestBotKindIsRegisteredForManyTargetsAndBothModes(t *testing.T) {
	d, ok := Lookup(KindBotReviewRequested)
	if !ok || d.Targets != TargetsMany || !d.supports(ModePreview) || !d.supports(ModePost) {
		t.Fatalf("descriptor = %+v, %v", d, ok)
	}
	j := Job{Kind: KindBotReviewRequested, Name: "bot", Mode: ModePreview, Spec: json.RawMessage(`{}`), Targets: []int64{1, 2, 3}}
	if err := j.Validate(); err != nil {
		t.Errorf("a bot job with several targets: %v", err)
	}
	j.Targets = nil
	if c, _ := code(j.Validate()); c != CodeTargetsInvalid {
		t.Errorf("a bot job needs a target: %q", c)
	}
}
