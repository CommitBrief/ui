// SPDX-License-Identifier: GPL-3.0-or-later

// Package job is the domain of a job: a stored definition of work the UI can
// run (a local review, a pull request review, later the review-requested bot).
// A job names its kind, carries a kind-owned JSON spec, a mode (preview or
// post) and the repositories it targets. This package performs no I/O.
package job

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind identifies a job type. It is an open set: a new kind is a new
// port.JobKind implementation plus a Descriptor here, nothing else changes
// (scheduler, run state machine and history are kind-independent).
type Kind string

// Kinds shipped so far.
const (
	// KindReviewLocal reviews the staged, unstaged or ranged changes of a
	// local checkout.
	KindReviewLocal Kind = "review.local"
	// KindReviewPR reviews one pull request.
	KindReviewPR Kind = "review.pr"
	// KindBotReviewRequested reviews the pull requests whose review is
	// requested from the signed-in GitHub user (UI-0005, UI-0008).
	KindBotReviewRequested Kind = "bot.review_requested"
)

// maxKindLen bounds a kind name.
const maxKindLen = 64

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Valid reports whether k has the shape of a kind name (dotted lower-case
// words). It does not say whether the kind is implemented.
func (k Kind) Valid() bool { return len(k) <= maxKindLen && kindRe.MatchString(string(k)) }

// Mode says whether a job only shows its result or also posts it to GitHub
// (UI-0008: preview is the default).
type Mode string

const (
	// ModePreview runs the review and keeps the result in the UI.
	ModePreview Mode = "preview"
	// ModePost also publishes the review on the pull request.
	ModePost Mode = "post"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModePreview || m == ModePost }

// Limits on user supplied values.
const (
	MaxNameLen    = 200
	MaxSpecBytes  = 64 << 10
	MaxTargets    = 100
	CurrentSpecV1 = 1
)

// Job is a job definition. Spec is opaque here: its schema belongs to the kind
// and is versioned by SpecVersion.
type Job struct {
	ID          int64           `json:"id"`
	Kind        Kind            `json:"kind"`
	Name        string          `json:"name"`
	Spec        json.RawMessage `json:"spec"`
	SpecVersion int             `json:"specVersion"`
	Mode        Mode            `json:"mode"`
	Enabled     bool            `json:"enabled"`
	// Targets are the IDs of the repositories the job runs on.
	Targets   []int64   `json:"targets"`
	CreatedAt time.Time `json:"createdAt"`
}

// TargetRule says how many repositories a kind runs on.
type TargetRule int

const (
	// TargetsOne: exactly one repository.
	TargetsOne TargetRule = iota
	// TargetsMany: at least one repository.
	TargetsMany
)

// Descriptor is what the domain knows about a kind: which modes it supports
// and how many targets it takes. Kind-specific spec validation lives with the
// kind (ParseLocalReviewSpec and friends).
type Descriptor struct {
	Kind    Kind
	Modes   []Mode
	Targets TargetRule
}

var descriptors = []Descriptor{
	{Kind: KindReviewLocal, Modes: []Mode{ModePreview}, Targets: TargetsOne},
	{Kind: KindReviewPR, Modes: []Mode{ModePreview}, Targets: TargetsOne},
	{Kind: KindBotReviewRequested, Modes: []Mode{ModePreview, ModePost}, Targets: TargetsMany},
}

// Descriptors returns every registered kind, in registration order.
func Descriptors() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

// Lookup finds the descriptor of a kind.
func Lookup(k Kind) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Kind == k {
			return d, true
		}
	}
	return Descriptor{}, false
}

// supports reports whether the descriptor allows the mode.
func (d Descriptor) supports(m Mode) bool {
	for _, x := range d.Modes {
		if x == m {
			return true
		}
	}
	return false
}

// Code is a member of the closed set of job definition error codes.
type Code string

const (
	CodeKindInvalid    Code = "job_kind_invalid"
	CodeNameInvalid    Code = "job_name_invalid"
	CodeModeInvalid    Code = "job_mode_invalid"
	CodeSpecInvalid    Code = "job_spec_invalid"
	CodeTargetsInvalid Code = "job_targets_invalid"
)

// Error is a classified job definition problem. Field is the JSON name of the
// offending field (a spec field is "spec.<name>").
type Error struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
}

func (e *Error) Error() string {
	if e.Field != "" {
		return string(e.Code) + " (" + e.Field + ")"
	}
	return string(e.Code)
}

// Is matches by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

func newErr(code Code, field string) *Error { return &Error{Code: code, Field: field} }

// Validate checks the definition: the shape of every field, the kind's mode
// and target rules when the kind is registered, and that Spec is a JSON object.
// The spec's own content is validated by the kind (port.JobKind.Validate).
func (j Job) Validate() error {
	if !j.Kind.Valid() {
		return newErr(CodeKindInvalid, "kind")
	}
	name := strings.TrimSpace(j.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen || strings.ContainsFunc(name, isControl) {
		return newErr(CodeNameInvalid, "name")
	}
	if !j.Mode.Valid() {
		return newErr(CodeModeInvalid, "mode")
	}
	if err := ValidateSpecShape(j.Spec); err != nil {
		return err
	}
	if len(j.Targets) > MaxTargets {
		return newErr(CodeTargetsInvalid, "targets")
	}
	seen := make(map[int64]bool, len(j.Targets))
	for _, id := range j.Targets {
		if id <= 0 || seen[id] {
			return newErr(CodeTargetsInvalid, "targets")
		}
		seen[id] = true
	}
	if d, ok := Lookup(j.Kind); ok {
		if !d.supports(j.Mode) {
			return newErr(CodeModeInvalid, "mode")
		}
		switch d.Targets {
		case TargetsOne:
			if len(j.Targets) != 1 {
				return newErr(CodeTargetsInvalid, "targets")
			}
		case TargetsMany:
			if len(j.Targets) < 1 {
				return newErr(CodeTargetsInvalid, "targets")
			}
		}
	}
	return nil
}

// ValidateSpecShape checks that raw is a bounded JSON object.
func ValidateSpecShape(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > MaxSpecBytes || trimmed[0] != '{' || !json.Valid(trimmed) {
		return newErr(CodeSpecInvalid, "spec")
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// decodeStrict decodes raw into v, refusing unknown fields and trailing data.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode spec: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("decode spec: trailing data")
	}
	return nil
}
