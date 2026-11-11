// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"encoding/json"
	"regexp"
)

// Local review modes.
const (
	LocalStaged   = "staged"
	LocalUnstaged = "unstaged"
	LocalRange    = "range"
)

// Limits on a ranged local review: the revision arguments are passed to
// `git diff` by the CLI adapter, which applies the same shape check again.
const (
	maxRangeArgs = 4
	maxRevLen    = 256
)

// revisionRe is the shape of one `git diff` revision argument. A leading '-'
// would be read as an option, so the first character is never one.
var revisionRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/@^~:{}+-]*$`)

// MaxPRNumber is the largest pull request number accepted.
const MaxPRNumber = 1<<31 - 1

// LocalReviewSpec is the spec of a KindReviewLocal job.
type LocalReviewSpec struct {
	// Mode is LocalStaged, LocalUnstaged or LocalRange.
	Mode string `json:"mode"`
	// Range holds the `git diff` revision arguments of a LocalRange review,
	// for example ["main..HEAD"]; it must be empty otherwise.
	Range []string `json:"range,omitempty"`
}

// Validate checks the spec.
func (s LocalReviewSpec) Validate() error {
	switch s.Mode {
	case LocalStaged, LocalUnstaged:
		if len(s.Range) != 0 {
			return newErr(CodeSpecInvalid, "spec.range")
		}
	case LocalRange:
		if len(s.Range) == 0 || len(s.Range) > maxRangeArgs {
			return newErr(CodeSpecInvalid, "spec.range")
		}
		for _, r := range s.Range {
			if len(r) > maxRevLen || !revisionRe.MatchString(r) {
				return newErr(CodeSpecInvalid, "spec.range")
			}
		}
	default:
		return newErr(CodeSpecInvalid, "spec.mode")
	}
	return nil
}

// Marshal renders the spec as the JSON a Job carries.
func (s LocalReviewSpec) Marshal() json.RawMessage {
	b, _ := json.Marshal(s) // plain strings: cannot fail
	return b
}

// ParseLocalReviewSpec decodes and validates a KindReviewLocal spec. Unknown
// fields are refused.
func ParseLocalReviewSpec(raw json.RawMessage) (LocalReviewSpec, error) {
	var s LocalReviewSpec
	if err := ValidateSpecShape(raw); err != nil {
		return s, err
	}
	if err := decodeStrict(raw, &s); err != nil {
		return LocalReviewSpec{}, newErr(CodeSpecInvalid, "spec")
	}
	return s, s.Validate()
}

// PRReviewSpec is the spec of a KindReviewPR job.
type PRReviewSpec struct {
	// Number is the pull request number in the job's target repository.
	Number int `json:"number"`
}

// Validate checks the spec.
func (s PRReviewSpec) Validate() error {
	if s.Number <= 0 || s.Number > MaxPRNumber {
		return newErr(CodeSpecInvalid, "spec.number")
	}
	return nil
}

// Marshal renders the spec as the JSON a Job carries.
func (s PRReviewSpec) Marshal() json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// ParsePRReviewSpec decodes and validates a KindReviewPR spec.
func ParsePRReviewSpec(raw json.RawMessage) (PRReviewSpec, error) {
	var s PRReviewSpec
	if err := ValidateSpecShape(raw); err != nil {
		return s, err
	}
	if err := decodeStrict(raw, &s); err != nil {
		return PRReviewSpec{}, newErr(CodeSpecInvalid, "spec")
	}
	return s, s.Validate()
}
