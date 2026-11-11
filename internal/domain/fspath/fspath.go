// SPDX-License-Identifier: GPL-3.0-or-later

// Package fspath decides, from the string alone, whether a path that came from
// outside (a form field, an import file, the contents of a checkout, a setting)
// may be handed to the file system. It never touches the file system itself.
//
// It is an allowlist, not a blocklist. On Windows a path is accepted only if it
// is a plain drive path, X:\... or X:/... ; everything else is refused, which
// covers every prefix that reaches the network or a device without naming them:
// UNC (\\host\share), the extended and device prefixes (\\?\, \\.\), the NT
// object namespace (\??\), mixed separators, drive-relative (C:foo) and
// drive-less rooted (\foo) paths. The reason is that on Windows even a stat or
// an open of a UNC path makes the machine authenticate (NTLM) to the named
// host, which hands the user's credential hash to whoever controls it.
// On Unix a path is accepted if it is absolute and has no NUL byte.
//
// Use ValidateLocal for a path that must be absolute (a checkout directory, a
// program path). Use ValidateTarget for a string that may legitimately be
// relative (a symbolic link target, a "gitdir:" line).
//
// The ...For variants take the operating system as a parameter ("windows" or
// anything else for Unix rules) so that both rule sets can be tested on any
// machine; the plain variants use runtime.GOOS.
//
// A drive letter that happens to be a mapped network drive cannot be told from
// a local one by looking at the string; it was mapped by the user, and the
// string check does not claim to cover it.
package fspath

import (
	"errors"
	"runtime"
	"strings"
)

var (
	// ErrInvalid: the string is malformed for a path on this OS (NUL byte,
	// control or wildcard characters, a colon after the drive, a reserved
	// device name, or empty where a path is required).
	ErrInvalid = errors.New("fspath: invalid path")
	// ErrNotAbsolute: the path is not absolute (relative, drive-relative or
	// rooted without a drive).
	ErrNotAbsolute = errors.New("fspath: path is not absolute")
	// ErrNetwork: the path is a network, device or namespace path (Windows
	// only: \\host\share, //host/share, \\?\..., \\.\..., \??\...).
	ErrNetwork = errors.New("fspath: network or device path")
)

// ValidateLocal checks that path is an acceptable absolute local path on the
// running OS. The empty string is not a path and is refused (ErrNotAbsolute).
func ValidateLocal(path string) error { return ValidateLocalFor(runtime.GOOS, path) }

// ValidateLocalFor is ValidateLocal for the named OS. Any goos other than
// "windows" gets Unix rules.
func ValidateLocalFor(goos, path string) error {
	if strings.IndexByte(path, 0) >= 0 {
		return ErrInvalid
	}
	if goos != "windows" {
		if path == "" || path[0] != '/' {
			return ErrNotAbsolute
		}
		return nil
	}
	return validateWindows(path, true)
}

// ValidateTarget checks a string that may be relative: a symbolic link target,
// a "gitdir:" or "commondir" value. It refuses what ValidateLocal refuses
// except relativity: a relative path, and on Windows a path rooted without a
// drive (\foo, which stays on the current drive), are fine; network and device
// paths, drive-relative paths (C:foo) and malformed strings are not.
func ValidateTarget(target string) error { return ValidateTargetFor(runtime.GOOS, target) }

// ValidateTargetFor is ValidateTarget for the named OS.
func ValidateTargetFor(goos, target string) error {
	if strings.IndexByte(target, 0) >= 0 {
		return ErrInvalid
	}
	if goos != "windows" {
		return nil
	}
	return validateWindows(target, false)
}

// validateWindows applies the Windows allowlist. With absolute set the path must
// start with a drive and a separator; without it a relative path or a path
// rooted at the current drive is also accepted.
func validateWindows(path string, absolute bool) error {
	p := strings.ReplaceAll(path, "/", `\`)

	// \\host\share, \\?\..., \\.\..., and the NT namespace form \??\... .
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `\??\`) {
		return ErrNetwork
	}

	rest := p
	if len(p) >= 2 && isASCIILetter(p[0]) && p[1] == ':' {
		if len(p) < 3 || p[2] != '\\' {
			return ErrNotAbsolute // "C:" or "C:foo": relative to a per-drive directory
		}
		rest = p[3:]
	} else if absolute {
		return ErrNotAbsolute // relative, or rooted without a drive
	} else if p == "" {
		return ErrInvalid
	}

	// A colon anywhere after the drive is an alternate data stream or a second
	// drive; the other characters are not valid in Windows names (and ? and *
	// are wildcards).
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == ':' || c < 0x20 || strings.IndexByte(`<>"|?*`, c) >= 0 {
			return ErrInvalid
		}
	}
	for _, comp := range strings.Split(rest, `\`) {
		if isReservedName(comp) {
			return ErrInvalid
		}
	}
	return nil
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// isReservedName reports whether a path component names a Windows device
// (CON, PRN, AUX, NUL, COM1-9, LPT1-9, the superscript forms COM¹ COM² COM³
// LPT¹ LPT² LPT³, CONIN$, CONOUT$), with or without an extension and
// trailing dots or spaces, in any case. Such a component can never be an
// ordinary directory.
func isReservedName(comp string) bool {
	base := comp
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if num, ok := strings.CutPrefix(base, "COM"); ok {
		return isDeviceNumber(num)
	}
	if num, ok := strings.CutPrefix(base, "LPT"); ok {
		return isDeviceNumber(num)
	}
	return false
}

// isDeviceNumber reports whether s is the number of a COM or LPT device: 1-9 or
// one of the superscript digits ¹ ² ³, which Windows reserves as well.
func isDeviceNumber(s string) bool {
	switch s {
	case "¹", "²", "³":
		return true
	}
	return len(s) == 1 && s[0] >= '1' && s[0] <= '9'
}
