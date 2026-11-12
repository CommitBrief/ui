// SPDX-License-Identifier: GPL-3.0-or-later

// Package update is the domain model of the self-update (UI-0007): versions and
// channels, the status the Updates screen shows, and the closed set of error
// codes the use cases hand to the frontend. It performs no I/O.
package update

import (
	"strings"
	"time"
)

// Channel says which releases an installation follows.
type Channel string

const (
	// ChannelStable follows releases only.
	ChannelStable Channel = "stable"
	// ChannelPrerelease follows releases and pre-releases (v0.1.0-rc.1, ...).
	ChannelPrerelease Channel = "prerelease"
)

// DefaultChannel is what a fresh installation follows.
const DefaultChannel = ChannelStable

// ParseChannel accepts a channel name; ok is false for anything else.
func ParseChannel(s string) (Channel, bool) {
	switch c := Channel(s); c {
	case ChannelStable, ChannelPrerelease:
		return c, true
	}
	return "", false
}

// Accepts reports whether a version belongs on the channel: the stable channel
// takes no pre-release, the pre-release channel takes everything.
func (c Channel) Accepts(v Version) bool {
	return c == ChannelPrerelease || !v.IsPrerelease()
}

// Keys of the `settings` table the Updates screen owns.
const (
	KeyAutoCheck = "updates.auto_check"
	KeyChannel   = "updates.channel"
	KeyLastCheck = "updates.last_check"
)

// DefaultAutoCheck is whether a fresh installation checks once a day.
const DefaultAutoCheck = true

// CheckInterval is the pause between two automatic checks.
const CheckInterval = 24 * time.Hour

// DevVersion is what a build without a release stamp reports. A build that
// reports it never updates itself: it would replace a developer's binary.
const DevVersion = "0.0.0-dev"

// IsDevBuild reports whether version is not a stamped release version.
func IsDevBuild(version string) bool {
	return version == "" || version == DevVersion || strings.HasSuffix(version, "-dev")
}

// State is where the updater stands.
type State string

const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateUpToDate    State = "up_to_date"
	StateAvailable   State = "available"
	StateDownloading State = "downloading"
	StateVerifying   State = "verifying"
	StateInstalling  State = "installing"
	// StateRestarting: the new version is in place and the application is
	// quitting so that it starts again.
	StateRestarting State = "restarting"
	// StateFailed: the last check or install failed; Status.Error says why and
	// the running version is untouched.
	StateFailed State = "failed"
)

// Release is a newer version the release feed offered.
type Release struct {
	// Version is the semantic version without "v".
	Version    string `json:"version"`
	Prerelease bool   `json:"prerelease"`
	// Notes is the release text, plain text only, bounded in size.
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"publishedAt"`
	// Size is the download size in bytes.
	Size int64 `json:"size"`
}

// Progress is how much of the download is on disk.
type Progress struct {
	Written int64 `json:"written"`
	Total   int64 `json:"total"`
}

// Delegation says why this installation does not update itself.
type Delegation string

const (
	// DelegationNone: the application updates itself.
	DelegationNone Delegation = ""
	// DelegationPackageManager: installed by a package manager (deb, rpm,
	// Flatpak, Nix, ...), which updates it.
	DelegationPackageManager Delegation = "package_manager"
	// DelegationNotWritable: the install location is not writable by the user.
	DelegationNotWritable Delegation = "not_writable"
	// DelegationUnbundled: a bare binary outside an application bundle (a
	// development run); replacing it would be wrong.
	DelegationUnbundled Delegation = "unbundled"
	// DelegationDevBuild: a build without a release version stamp.
	DelegationDevBuild Delegation = "development_build"
	// DelegationUnsupported: this operating system has no self-update.
	DelegationUnsupported Delegation = "unsupported"
)

// Status is what the Updates screen shows about the updater itself.
type Status struct {
	State State `json:"state"`
	// Current is the running version.
	Current string `json:"current"`
	// Available is the release found by the last check, while one is pending.
	Available *Release `json:"available"`
	// Progress is set while State is StateDownloading.
	Progress *Progress `json:"progress"`
	// Error is the code of the last failure while State is StateFailed.
	Error Code `json:"error"`
	// Delegation is non-empty when this installation does not update itself.
	Delegation Delegation `json:"delegation"`
}

// Code is a member of the closed set of update error codes. The backend hands
// only the code to the frontend, never text.
type Code string

const (
	// CodeCheckFailed: the release feed could not be read.
	CodeCheckFailed Code = "update_check_failed"
	// CodeFeedUnavailable: the release feed answered that the repository or its
	// releases do not exist or are not readable without credentials (404, 401,
	// 403). Never reported as "no update": an unreachable feed is a failure.
	CodeFeedUnavailable Code = "update_feed_unavailable"
	// CodeChecksumMissing: the release has no checksums file, the file is
	// unusable, or it does not list the artefact for this system.
	CodeChecksumMissing Code = "update_checksum_missing"
	// CodeChecksumMismatch: the downloaded artefact does not hash to its
	// checksums.txt line.
	CodeChecksumMismatch Code = "update_checksum_mismatch"
	// CodeNoArtifact: the release has no artefact for this system.
	CodeNoArtifact Code = "update_no_artifact"
	// CodeDownloadFailed: the artefact could not be downloaded.
	CodeDownloadFailed Code = "update_download_failed"
	// CodeInstallFailed: the new version could not be put in place; the
	// running version is untouched.
	CodeInstallFailed Code = "update_install_failed"
	// CodeRollbackFailed: the install failed and putting things back failed
	// too; reinstalling from the release page may be necessary.
	CodeRollbackFailed Code = "update_rollback_failed"
	// CodeDelegated: this installation is updated by something else.
	CodeDelegated Code = "update_delegated"
	// CodeNothingToInstall: install was asked for with no update pending.
	CodeNothingToInstall Code = "update_nothing_to_install"
	// CodeBusy: another check or install is running.
	CodeBusy Code = "update_busy"
	// CodeInvalid: an input (the channel) is not acceptable; Field names it.
	CodeInvalid Code = "update_invalid"
	// CodeCanceled: the call was canceled by the frontend.
	CodeCanceled Code = "canceled"
	// CodeInternal: anything unexpected; details are in the log only.
	CodeInternal Code = "internal"
)

// Error is a classified update error, serialized as {code, field}. Err (the
// cause, which may hold paths or URLs) never leaves the process.
type Error struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
	Err   error  `json:"-"`
}

// Error returns only the code and the field.
func (e *Error) Error() string {
	if e.Field != "" {
		return string(e.Code) + " (" + e.Field + ")"
	}
	return string(e.Code)
}

// Unwrap exposes the cause for logs.
func (e *Error) Unwrap() error { return e.Err }

// Is matches by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// New builds an *Error for a code and an optional field.
func New(code Code, field string) *Error { return &Error{Code: code, Field: field} }

// Wrap builds an *Error that keeps cause for logs.
func Wrap(code Code, cause error) *Error { return &Error{Code: code, Err: cause} }
