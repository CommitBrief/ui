// SPDX-License-Identifier: GPL-3.0-or-later

// Package errcode is the closed set of failure classes the UI reasons about
// (UI-0004 section 5). Adapters reduce every failure to one of these codes;
// the backend never hands localized text to the frontend, only the code.
package errcode

import (
	"errors"
	"fmt"
)

// Code is one member of the closed failure-class set.
type Code string

const (
	CLINotFound        Code = "cli_not_found"
	CLITooOld          Code = "cli_too_old"
	GHNotFound         Code = "gh_not_found"
	GHNotAuthenticated Code = "gh_not_authenticated"
	NotARepo           Code = "not_a_repo"
	ProviderError      Code = "provider_error"
	ProviderNotAPI     Code = "provider_not_api"
	SelfPR             Code = "self_pr"
	TooVolatile        Code = "too_volatile"
	SecretDetected     Code = "secret_detected"
	CostAborted        Code = "cost_aborted"
	Timeout            Code = "timeout"
	Canceled           Code = "canceled"
	Degraded           Code = "degraded"
	CapabilityMissing  Code = "capability_missing"
	Unknown            Code = "unknown"
)

var all = []Code{
	CLINotFound, CLITooOld, GHNotFound, GHNotAuthenticated, NotARepo,
	ProviderError, ProviderNotAPI, SelfPR, TooVolatile, SecretDetected,
	CostAborted, Timeout, Canceled, Degraded, CapabilityMissing, Unknown,
}

// All returns every code in the closed set, in declaration order.
func All() []Code {
	out := make([]Code, len(all))
	copy(out, all)
	return out
}

// Valid reports whether c is a member of the closed set.
func (c Code) Valid() bool {
	for _, k := range all {
		if k == c {
			return true
		}
	}
	return false
}

// Error is a classified failure. Code is the contract; Message is a short
// English diagnostic for logs (never shown as UI text); Detail carries raw
// process stderr when the classification came from it.
type Error struct {
	Code    Code
	Message string
	Detail  string
	Err     error
}

// New builds an Error without a cause.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap builds an Error that keeps err as its cause.
func Wrap(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// WithDetail returns a copy of e carrying raw diagnostic text.
func (e *Error) WithDetail(detail string) *Error {
	c := *e
	c.Detail = detail
	return &c
}

func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	default:
		return string(e.Code)
	}
}

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Err }

// Is makes errors.Is compare by code: any *Error with the same code matches,
// so errors.Is(err, errcode.ErrCapabilityMissing) works for every instance.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// ErrCapabilityMissing matches any error classified as capability_missing.
var ErrCapabilityMissing = New(CapabilityMissing, "capability missing")

// CodeOf returns the code of the first *Error in err's chain. A nil error has
// no code (""); a non-nil error that carries none is Unknown.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Unknown
}

// Is reports whether err is classified with the given code.
func Is(err error, code Code) bool { return CodeOf(err) == code }
