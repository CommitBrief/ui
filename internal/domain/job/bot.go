// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"encoding/json"
	"time"
)

// BotReviewRequestedSpec is the spec of a KindBotReviewRequested job. The job's
// targets are the repositories the bot watches; its Mode (UI-0008) says whether
// a review stays in the UI (preview, the default) or is posted to the pull
// request (post).
type BotReviewRequestedSpec struct {
	// IncludeDrafts also reviews draft pull requests; the default skips them.
	IncludeDrafts bool `json:"includeDrafts"`
	// PostConsentAt is when the user accepted, in so many words, that a posted
	// review is written under their own GitHub identity and removes them from
	// the pull request's requested reviewers (UI-0008 section 2). A post job
	// without it is rejected; a preview job ignores it.
	PostConsentAt *time.Time `json:"postConsentAt,omitempty"`
}

// ValidateFor checks the spec against the job's mode: a post job needs the
// recorded consent.
func (s BotReviewRequestedSpec) ValidateFor(mode Mode) error {
	if mode == ModePost && (s.PostConsentAt == nil || s.PostConsentAt.IsZero()) {
		return newErr(CodeSpecInvalid, "spec.postConsentAt")
	}
	return nil
}

// Marshal renders the spec as the JSON a Job carries.
func (s BotReviewRequestedSpec) Marshal() json.RawMessage {
	b, _ := json.Marshal(s) // plain values: cannot fail
	return b
}

// ParseBotReviewRequestedSpec decodes a KindBotReviewRequested spec. Unknown
// fields are refused. The mode-dependent rule (consent for post) is ValidateFor.
func ParseBotReviewRequestedSpec(raw json.RawMessage) (BotReviewRequestedSpec, error) {
	var s BotReviewRequestedSpec
	if err := ValidateSpecShape(raw); err != nil {
		return s, err
	}
	if err := decodeStrict(raw, &s); err != nil {
		return BotReviewRequestedSpec{}, newErr(CodeSpecInvalid, "spec")
	}
	return s, nil
}
