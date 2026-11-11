// SPDX-License-Identifier: GPL-3.0-or-later

package run

import (
	"errors"
	"fmt"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

var allStatuses = []Status{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCanceled, StatusAttention}

func TestStatusTransitions(t *testing.T) {
	allowed := map[Status][]Status{
		StatusQueued:  {StatusRunning, StatusCanceled},
		StatusRunning: {StatusSucceeded, StatusFailed, StatusCanceled, StatusAttention},
	}
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := false
			for _, ok := range allowed[from] {
				if ok == to {
					want = true
				}
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestStatusSets(t *testing.T) {
	for _, s := range allStatuses {
		if !s.Valid() {
			t.Errorf("%s is not Valid", s)
		}
		wantTerminal := s != StatusQueued && s != StatusRunning
		if s.Terminal() != wantTerminal {
			t.Errorf("%s.Terminal() = %v", s, s.Terminal())
		}
		if s.Terminal() {
			for _, to := range allStatuses {
				if s.CanTransitionTo(to) {
					t.Errorf("terminal status %s can move to %s", s, to)
				}
			}
		}
	}
	if Status("paused").Valid() || Status("").Valid() {
		t.Error("an unknown status is Valid")
	}
	for _, tr := range []Trigger{TriggerManual, TriggerSchedule, TriggerCatchup} {
		if !tr.Valid() {
			t.Errorf("trigger %s is not Valid", tr)
		}
	}
	if Trigger("cron").Valid() {
		t.Error("an unknown trigger is Valid")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err    error
		status Status
		code   errcode.Code
	}{
		{nil, StatusSucceeded, ""},
		{errcode.New(errcode.Canceled, ""), StatusCanceled, errcode.Canceled},
		{fmt.Errorf("wrapped: %w", errcode.New(errcode.Canceled, "")), StatusCanceled, errcode.Canceled},
		{errcode.New(errcode.CLINotFound, ""), StatusAttention, errcode.CLINotFound},
		{errcode.New(errcode.CLITooOld, ""), StatusAttention, errcode.CLITooOld},
		{errcode.New(errcode.GHNotFound, ""), StatusAttention, errcode.GHNotFound},
		{errcode.New(errcode.GHNotAuthenticated, ""), StatusAttention, errcode.GHNotAuthenticated},
		{errcode.New(errcode.NotARepo, ""), StatusAttention, errcode.NotARepo},
		{errcode.New(errcode.ProviderNotAPI, ""), StatusAttention, errcode.ProviderNotAPI},
		{errcode.New(errcode.SelfPR, ""), StatusAttention, errcode.SelfPR},
		{errcode.New(errcode.CapabilityMissing, ""), StatusAttention, errcode.CapabilityMissing},
		{errcode.New(errcode.ProviderError, ""), StatusFailed, errcode.ProviderError},
		{errcode.New(errcode.Timeout, ""), StatusFailed, errcode.Timeout},
		{errcode.New(errcode.TooVolatile, ""), StatusFailed, errcode.TooVolatile},
		{errcode.New(errcode.SecretDetected, ""), StatusFailed, errcode.SecretDetected},
		{errcode.New(errcode.CostAborted, ""), StatusFailed, errcode.CostAborted},
		{errcode.New(errcode.Degraded, ""), StatusFailed, errcode.Degraded},
		{errors.New("plain"), StatusFailed, errcode.Unknown},
	}
	for _, tc := range cases {
		gotS, gotC := Classify(tc.err)
		if gotS != tc.status || gotC != tc.code {
			t.Errorf("Classify(%v) = %s/%s, want %s/%s", tc.err, gotS, gotC, tc.status, tc.code)
		}
		if !gotS.Terminal() {
			t.Errorf("Classify(%v) returned a non-terminal status", tc.err)
		}
	}
}

func TestAggregate(t *testing.T) {
	cases := []struct {
		in   []Status
		want Status
	}{
		{nil, StatusSucceeded},
		{[]Status{StatusQueued}, StatusQueued},
		{[]Status{StatusQueued, StatusQueued}, StatusQueued},
		{[]Status{StatusRunning, StatusQueued}, StatusRunning},
		{[]Status{StatusSucceeded, StatusQueued}, StatusRunning},
		{[]Status{StatusSucceeded, StatusRunning, StatusFailed}, StatusRunning},
		{[]Status{StatusSucceeded}, StatusSucceeded},
		{[]Status{StatusSucceeded, StatusSucceeded}, StatusSucceeded},
		{[]Status{StatusSucceeded, StatusCanceled}, StatusCanceled},
		{[]Status{StatusCanceled, StatusCanceled}, StatusCanceled},
		{[]Status{StatusSucceeded, StatusAttention}, StatusAttention},
		{[]Status{StatusCanceled, StatusAttention}, StatusAttention},
		{[]Status{StatusAttention, StatusFailed, StatusSucceeded}, StatusFailed},
	}
	for _, tc := range cases {
		if got := Aggregate(tc.in); got != tc.want {
			t.Errorf("Aggregate(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestRunErrorCodeAndCost(t *testing.T) {
	tasks := []Task{
		{Status: StatusSucceeded, CostUSD: 0.5},
		{Status: StatusAttention, ErrorCode: errcode.CLITooOld, CostUSD: 0},
		{Status: StatusFailed, ErrorCode: errcode.Timeout, CostUSD: 0.25},
	}
	if got := RunErrorCode(tasks); got != errcode.CLITooOld {
		t.Errorf("RunErrorCode = %q, want the first failing task's code", got)
	}
	if got := RunErrorCode(tasks[:1]); got != "" {
		t.Errorf("RunErrorCode of a clean run = %q", got)
	}
	if got := TotalCost(tasks); got != 0.75 {
		t.Errorf("TotalCost = %v", got)
	}
}

func TestLockKeys(t *testing.T) {
	if LockKeyForPR("Acme/App", 7) != LockKeyForPR("acme/app", 7) {
		t.Error("pull request keys must ignore the case of the repository name")
	}
	if LockKeyForPR("acme/app", 7) == LockKeyForPR("acme/app", 8) {
		t.Error("different pull requests share a key")
	}
	if LockKeyForPR("acme/app", 7) == LockKeyForPath("linux", "acme/app") {
		t.Error("a pull request key collides with a path key")
	}
	// The case rule belongs to the path's operating system, not to the machine
	// the test runs on.
	if LockKeyForPath("windows", `C:\Work\App`) != LockKeyForPath("windows", `c:\work\app`) {
		t.Error("Windows path keys must ignore case")
	}
	if LockKeyForPath("linux", "/work/App") == LockKeyForPath("linux", "/work/app") {
		t.Error("Unix path keys must keep case")
	}
	if LockKeyForPath("darwin", "/work/App") == LockKeyForPath("darwin", "/work/app") {
		t.Error("macOS path keys are kept as stored")
	}
}

func TestErrorShape(t *testing.T) {
	e := NewError(CodeJobInvalid, "spec.mode")
	if e.Error() != "job_invalid (spec.mode)" || NewError(CodeInternal, "").Error() != "internal" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !errors.Is(e, NewError(CodeJobInvalid, "")) || errors.Is(e, NewError(CodeQueueFull, "")) {
		t.Error("errors.Is must match by code")
	}
	cause := errors.New("disk path /secret/location")
	w := WrapError(CodeInternal, "", cause)
	if !errors.Is(w, cause) || w.Error() != "internal" {
		t.Errorf("WrapError lost the cause or leaked it: %q", w.Error())
	}
}
