// SPDX-License-Identifier: GPL-3.0-or-later

package fspath

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// The tests pass the OS explicitly, so both rule sets run on every machine and
// the Windows logic is exercised by the macOS and Linux CI jobs too.

type pathCase struct {
	path string
	want error // nil: accepted
}

func runCases(t *testing.T, name string, f func(string) error, cases []pathCase) {
	t.Helper()
	for _, c := range cases {
		got := f(c.path)
		if c.want == nil {
			if got != nil {
				t.Errorf("%s(%q) = %v, want accepted", name, c.path, got)
			}
			continue
		}
		if !errors.Is(got, c.want) {
			t.Errorf("%s(%q) = %v, want %v", name, c.path, got, c.want)
		}
	}
}

func TestValidateLocalWindowsAllowlist(t *testing.T) {
	win := func(p string) error { return ValidateLocalFor("windows", p) }
	runCases(t, "windows", win, []pathCase{
		// Accepted: a drive letter, a separator, then an ordinary path.
		{`C:\`, nil},
		{`C:\Users\me\repo`, nil},
		{`c:\users\me\repo`, nil},
		{`Z:\a b\c.d`, nil},
		{`C:/Users/me/repo`, nil},
		{`C:\Users/me\repo`, nil}, // mixed separators after a drive are fine
		{`D:\my.repo\sub`, nil},
		{`C:\Program Files (x86)\x`, nil},

		// UNC, in every spelling.
		{`\\host\share`, ErrNetwork},
		{`\\host\share\repo`, ErrNetwork},
		{`//host/share/repo`, ErrNetwork},
		{`\/host/share`, ErrNetwork},
		{`/\host\share`, ErrNetwork},
		{`\\host`, ErrNetwork},
		{`\\`, ErrNetwork},
		{`//`, ErrNetwork},
		// Extended-length and device prefixes.
		{`\\?\C:\x`, ErrNetwork}, // even the local form: not on the allowlist
		{`\\?\UNC\host\share\x`, ErrNetwork},
		{`\\?\GLOBALROOT\Device\HarddiskVolume1\x`, ErrNetwork},
		{`\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\x`, ErrNetwork},
		{`//?/C:/x`, ErrNetwork},
		{`\\.\pipe\x`, ErrNetwork},
		{`\\.\C:\x`, ErrNetwork},
		{`//./pipe/x`, ErrNetwork},
		// The NT object namespace, which Go's filepath.IsAbs accepts on Windows.
		{`\??\UNC\host\share\x`, ErrNetwork},
		{`\??\C:\x`, ErrNetwork},
		{`/??/UNC/host/share/x`, ErrNetwork},

		// Not absolute.
		{``, ErrNotAbsolute},
		{`\foo`, ErrNotAbsolute},
		{`/foo`, ErrNotAbsolute},
		{`\?\foo`, ErrNotAbsolute},
		{`C:`, ErrNotAbsolute},
		{`C:foo`, ErrNotAbsolute},
		{`C:foo\bar`, ErrNotAbsolute},
		{`foo\bar`, ErrNotAbsolute},
		{`.`, ErrNotAbsolute},
		{`..\x`, ErrNotAbsolute},
		{`~\x`, ErrNotAbsolute},
		{`1:\x`, ErrNotAbsolute},
		{`CC:\x`, ErrNotAbsolute},

		// Malformed.
		{"C:\\a\x00b", ErrInvalid},
		{"\x00", ErrInvalid},
		{`C:\file.txt:stream`, ErrInvalid}, // alternate data stream
		{`C:\dir\:$DATA`, ErrInvalid},      //
		{`C:\a\C:\b`, ErrInvalid},          // drive again
		{`C:\a*b`, ErrInvalid},             // wildcard
		{`C:\a?b`, ErrInvalid},             //
		{`C:\a<b`, ErrInvalid},             //
		{`C:\a|b`, ErrInvalid},             //
		{`C:\a"b`, ErrInvalid},             //
		{"C:\\a\tb", ErrInvalid},           // control character
		{`C:\NUL`, ErrInvalid},             // device names
		{`C:\sub\con`, ErrInvalid},         //
		{`C:\aux.txt`, ErrInvalid},         //
		{`C:\Com1\x`, ErrInvalid},          //
		{`C:\LPT9`, ErrInvalid},            //
		{`C:\CONIN$`, ErrInvalid},          //
		{"C:\\COM\u00b9", ErrInvalid},      // superscript digits are reserved too
		{"C:\\lpt\u00b2.txt", ErrInvalid},  //
		{"C:\\x\\COM\u00b3", ErrInvalid},   //
		{`C:\com0`, nil},                   // COM0 is not reserved
		{"C:\\COM\u2074", nil},             // only \u00b9 \u00b2 \u00b3
		{`C:\console`, nil},                // only the exact names are
		{`C:\nul.d\x`, ErrInvalid},         // "nul.d" is still the device
	})
}

func TestValidateLocalUnixRules(t *testing.T) {
	unix := func(p string) error { return ValidateLocalFor("linux", p) }
	runCases(t, "linux", unix, []pathCase{
		{`/`, nil},
		{`/home/me/repo`, nil},
		{`/a b/c`, nil},
		{`/a:b`, nil}, // colons, backslashes and wildcards are ordinary on Unix
		{`/a\b`, nil},
		{`/a*b?`, nil},
		{`//double/leading`, nil}, // an ordinary absolute path on Linux and macOS
		{``, ErrNotAbsolute},
		{`relative/path`, ErrNotAbsolute},
		{`.`, ErrNotAbsolute},
		{`~/x`, ErrNotAbsolute},
		{`C:\Users\me`, ErrNotAbsolute}, // a Windows path is not absolute here
		{`\\host\share`, ErrNotAbsolute},
		{"/a\x00b", ErrInvalid},
		{"\x00", ErrInvalid},
	})
	// Every non-Windows name gets the same rules.
	for _, goos := range []string{"darwin", "freebsd", "linux", ""} {
		if err := ValidateLocalFor(goos, "/x"); err != nil {
			t.Errorf("goos %q: %v", goos, err)
		}
	}
}

func TestValidateTarget(t *testing.T) {
	win := func(p string) error { return ValidateTargetFor("windows", p) }
	runCases(t, "target/windows", win, []pathCase{
		// Relative and rooted-without-drive targets are legitimate link targets.
		{`..\x`, nil},
		{`sub/dir`, nil},
		{`.git`, nil},
		{`\foo`, nil},
		{`C:\Users\me`, nil},
		{`C:/Users/me`, nil},

		{`\\host\share`, ErrNetwork},
		{`//host/share`, ErrNetwork},
		{`\\?\C:\x`, ErrNetwork},
		{`\\.\pipe\x`, ErrNetwork},
		{`\??\UNC\host\share`, ErrNetwork},
		{`C:foo`, ErrNotAbsolute},
		{`C:`, ErrNotAbsolute},
		{``, ErrInvalid},
		{"a\x00b", ErrInvalid},
		{`ab:c`, ErrInvalid},
		{`a:b`, ErrNotAbsolute}, // one letter and a colon is a drive-relative path
		{`a*b`, ErrInvalid},
		{`nul`, ErrInvalid},
	})

	unix := func(p string) error { return ValidateTargetFor("linux", p) }
	runCases(t, "target/linux", unix, []pathCase{
		{`../x`, nil},
		{`/abs/path`, nil},
		{`\\host\share`, nil}, // an ordinary relative file name on Unix
		{`//host/share`, nil},
		{``, nil},
		{"a\x00b", ErrInvalid},
	})
}

// The runtime variants agree with the explicit ones for the running OS.
func TestRuntimeVariantsUseTheRunningOS(t *testing.T) {
	probe := `/abs`
	if runtime.GOOS == "windows" {
		probe = `C:\abs`
	}
	if err := ValidateLocal(probe); err != nil {
		t.Errorf("ValidateLocal(%q) = %v on %s", probe, err, runtime.GOOS)
	}
	other := `C:\abs`
	if runtime.GOOS == "windows" {
		other = `/abs`
	}
	if err := ValidateLocal(other); !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("ValidateLocal(%q) = %v on %s, want ErrNotAbsolute", other, err, runtime.GOOS)
	}
	if err := ValidateTarget("rel/x"); err != nil {
		t.Errorf("ValidateTarget(rel/x) = %v", err)
	}
}

// Every known way to spell a network path is refused on Windows, however the
// separators are mixed and however much whitespace-free padding follows.
func TestWindowsPrefixesAreRefusedInEverySeparatorMix(t *testing.T) {
	bases := []string{`??\UNC\h\s`, `?\UNC\h\s`, `.\pipe\x`, `host\share`}
	seps := []string{`\`, `/`}
	for _, b := range bases {
		for _, s1 := range seps {
			for _, s2 := range seps {
				p := s1 + s2 + strings.ReplaceAll(b, `\`, s2)
				if got := ValidateLocalFor("windows", p); !errors.Is(got, ErrNetwork) {
					t.Errorf("ValidateLocalFor(windows, %q) = %v, want ErrNetwork", p, got)
				}
			}
		}
	}
	// And the NT form with a single leading separator.
	for _, p := range []string{`\??\UNC\h\s`, `/??/UNC/h/s`, `\??/UNC\h\s`} {
		if got := ValidateLocalFor("windows", p); !errors.Is(got, ErrNetwork) {
			t.Errorf("ValidateLocalFor(windows, %q) = %v, want ErrNetwork", p, got)
		}
	}
}
