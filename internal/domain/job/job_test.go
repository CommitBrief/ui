// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func valid() Job {
	return Job{
		Kind: KindReviewPR, Name: "PR 7", Mode: ModePreview, Enabled: true,
		Spec: json.RawMessage(`{"number":7}`), Targets: []int64{1},
	}
}

func code(err error) (Code, string) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Field
	}
	return "", ""
}

func TestJobValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid job refused: %v", err)
	}
	cases := []struct {
		name  string
		edit  func(*Job)
		code  Code
		field string
	}{
		{"bad kind", func(j *Job) { j.Kind = "Review PR" }, CodeKindInvalid, "kind"},
		{"kind without dot", func(j *Job) { j.Kind = "review" }, CodeKindInvalid, "kind"},
		{"empty name", func(j *Job) { j.Name = "  " }, CodeNameInvalid, "name"},
		{"long name", func(j *Job) { j.Name = strings.Repeat("a", MaxNameLen+1) }, CodeNameInvalid, "name"},
		{"control character", func(j *Job) { j.Name = "a\nb" }, CodeNameInvalid, "name"},
		{"unknown mode", func(j *Job) { j.Mode = "dry" }, CodeModeInvalid, "mode"},
		{"local review cannot post", func(j *Job) { j.Kind, j.Mode, j.Spec = KindReviewLocal, ModePost, json.RawMessage(`{}`) }, CodeModeInvalid, "mode"},
		{"empty spec", func(j *Job) { j.Spec = nil }, CodeSpecInvalid, "spec"},
		{"spec not an object", func(j *Job) { j.Spec = json.RawMessage(`[1]`) }, CodeSpecInvalid, "spec"},
		{"spec not json", func(j *Job) { j.Spec = json.RawMessage(`{`) }, CodeSpecInvalid, "spec"},
		{"spec too big", func(j *Job) { j.Spec = json.RawMessage(`{"a":"` + strings.Repeat("x", MaxSpecBytes) + `"}`) }, CodeSpecInvalid, "spec"},
		{"no target", func(j *Job) { j.Targets = nil }, CodeTargetsInvalid, "targets"},
		{"two targets for a one-target kind", func(j *Job) { j.Targets = []int64{1, 2} }, CodeTargetsInvalid, "targets"},
		{"duplicate target", func(j *Job) { j.Targets = []int64{1, 1} }, CodeTargetsInvalid, "targets"},
		{"non-positive target", func(j *Job) { j.Targets = []int64{0} }, CodeTargetsInvalid, "targets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := valid()
			tc.edit(&j)
			c, f := code(j.Validate())
			if c != tc.code || f != tc.field {
				t.Errorf("Validate = %q (%q), want %q (%q)", c, f, tc.code, tc.field)
			}
		})
	}

	// A kind the domain has no descriptor for (a later phase's) only gets the
	// shape checks.
	future := valid()
	future.Kind, future.Targets, future.Mode = "bot.review_requested", []int64{1, 2, 3}, ModePost
	if err := future.Validate(); err != nil {
		t.Errorf("an unregistered kind was refused: %v", err)
	}
}

func TestDescriptors(t *testing.T) {
	ds := Descriptors()
	if len(ds) != 2 || ds[0].Kind != KindReviewLocal || ds[1].Kind != KindReviewPR {
		t.Fatalf("Descriptors = %+v", ds)
	}
	ds[0].Kind = "mutated.kind"
	if again := Descriptors(); again[0].Kind != KindReviewLocal {
		t.Error("Descriptors returned the internal slice")
	}
	if d, ok := Lookup(KindReviewPR); !ok || !d.supports(ModePost) {
		t.Errorf("Lookup(review.pr) = %+v, %v", d, ok)
	}
	if _, ok := Lookup("nope.kind"); ok {
		t.Error("Lookup found an unknown kind")
	}
	for _, d := range Descriptors() {
		if !d.Kind.Valid() {
			t.Errorf("registered kind %q is not a valid kind name", d.Kind)
		}
	}
}

func TestLocalReviewSpec(t *testing.T) {
	good := []string{
		`{"mode":"staged"}`,
		`{"mode":"unstaged"}`,
		`{"mode":"range","range":["main..HEAD"]}`,
		`{"mode":"range","range":["main","HEAD"]}`,
		`{"mode":"range","range":["origin/main...feature/x"]}`,
		`{"mode":"range","range":["HEAD~3","HEAD^"]}`,
	}
	for _, raw := range good {
		if _, err := ParseLocalReviewSpec(json.RawMessage(raw)); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
	bad := []struct{ raw, field string }{
		{`{"mode":"all"}`, "spec.mode"},
		{`{}`, "spec.mode"},
		{`{"mode":"staged","range":["main"]}`, "spec.range"},
		{`{"mode":"range"}`, "spec.range"},
		{`{"mode":"range","range":[]}`, "spec.range"},
		{`{"mode":"range","range":["a","b","c","d","e"]}`, "spec.range"},
		{`{"mode":"range","range":["--output=/tmp/x"]}`, "spec.range"},
		{`{"mode":"range","range":["-p"]}`, "spec.range"},
		{`{"mode":"range","range":["a b"]}`, "spec.range"},
		{`{"mode":"range","range":["a;rm -rf"]}`, "spec.range"},
		{`{"mode":"range","range":["` + strings.Repeat("a", maxRevLen+1) + `"]}`, "spec.range"},
		{`{"mode":"staged","extra":1}`, "spec"},
		{`{"mode":"staged"} {"mode":"staged"}`, "spec"},
		{`not json`, "spec"},
		{``, "spec"},
	}
	for _, tc := range bad {
		_, err := ParseLocalReviewSpec(json.RawMessage(tc.raw))
		if c, f := code(err); c != CodeSpecInvalid || f != tc.field {
			t.Errorf("%s: error = %v, want spec_invalid on %q", tc.raw, err, tc.field)
		}
	}

	s := LocalReviewSpec{Mode: LocalRange, Range: []string{"main..HEAD"}}
	back, err := ParseLocalReviewSpec(s.Marshal())
	if err != nil || back.Mode != LocalRange || len(back.Range) != 1 || back.Range[0] != "main..HEAD" {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

func TestPRReviewSpec(t *testing.T) {
	if s, err := ParsePRReviewSpec(json.RawMessage(`{"number":42}`)); err != nil || s.Number != 42 {
		t.Errorf("good spec = %+v, %v", s, err)
	}
	for _, raw := range []string{`{"number":0}`, `{"number":-1}`, `{"number":2147483648}`, `{}`, `{"number":"7"}`, `{"number":7,"x":1}`, `[]`} {
		_, err := ParsePRReviewSpec(json.RawMessage(raw))
		if c, _ := code(err); c != CodeSpecInvalid {
			t.Errorf("%s: error = %v, want spec_invalid", raw, err)
		}
	}
	back, err := ParsePRReviewSpec(PRReviewSpec{Number: MaxPRNumber}.Marshal())
	if err != nil || back.Number != MaxPRNumber {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

func TestErrorShape(t *testing.T) {
	e := newErr(CodeSpecInvalid, "spec.mode")
	if e.Error() != "job_spec_invalid (spec.mode)" || newErr(CodeKindInvalid, "").Error() != "job_kind_invalid" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !errors.Is(e, newErr(CodeSpecInvalid, "")) || errors.Is(e, newErr(CodeNameInvalid, "")) {
		t.Error("errors.Is must match by code")
	}
}
