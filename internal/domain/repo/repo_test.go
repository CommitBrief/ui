// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func codeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestValidateOwner(t *testing.T) {
	good := []string{"CommitBrief", "a", "my-org", "A1-b2", strings.Repeat("a", MaxOwnerLen)}
	for _, s := range good {
		if err := ValidateOwner(s); err != nil {
			t.Errorf("ValidateOwner(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", "a/b", "a b", "a.b", "a_b", "-- x", "é", strings.Repeat("a", MaxOwnerLen+1), "a\n"}
	for _, s := range bad {
		if got := codeOf(ValidateOwner(s)); got != CodeOwnerInvalid {
			t.Errorf("ValidateOwner(%q) code = %q, want %q", s, got, CodeOwnerInvalid)
		}
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"ui", "commitbrief.com", "a_b-c.d", ".github", "x.git-y"}
	for _, s := range good {
		if err := ValidateName(s); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", "a b", "a;b", "$(x)", "é", strings.Repeat("a", MaxNameLen+1), "a\n"}
	for _, s := range bad {
		if got := codeOf(ValidateName(s)); got != CodeNameInvalid {
			t.Errorf("ValidateName(%q) code = %q, want %q", s, got, CodeNameInvalid)
		}
	}
}

func TestValidateLocalPath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "x")
	if err := ValidateLocalPath(abs); err != nil {
		t.Errorf("absolute path rejected: %v", err)
	}
	if err := ValidateLocalPath(""); err != nil {
		t.Errorf("empty path must be allowed: %v", err)
	}
	for _, p := range []string{"relative/dir", ".", "~/x", abs + "\x00", strings.Repeat("/a", MaxLocalPathLen)} {
		if got := codeOf(ValidateLocalPath(p)); got != CodeLocalPathInvalid {
			t.Errorf("ValidateLocalPath(%q) code = %q, want %q", p, got, CodeLocalPathInvalid)
		}
	}
}

func TestCleanLocalPathCollapsesDotSegments(t *testing.T) {
	base := t.TempDir()
	in := base + string(filepath.Separator) + "a" + string(filepath.Separator) + ".." + string(filepath.Separator) + "b" + string(filepath.Separator)
	if got, err := CleanLocalPath(in); err != nil || got != filepath.Join(base, "b") {
		t.Errorf("CleanLocalPath = %q, %v; want %q", got, err, filepath.Join(base, "b"))
	}
	if got, err := CleanLocalPath(""); err != nil || got != "" {
		t.Errorf("empty stays empty, got %q, %v", got, err)
	}
}

func TestRepositoryValidateReportsFirstBadField(t *testing.T) {
	r := Repository{Owner: "ok", Name: "bad name"}
	err := r.Validate()
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeNameInvalid || e.Field != "name" {
		t.Fatalf("Validate() = %v, want name invalid", err)
	}
	if got := (Repository{Owner: "a", Name: "b"}).Slug(); got != "a/b" {
		t.Errorf("Slug() = %q", got)
	}
}

func TestErrorTextCarriesNoCauseText(t *testing.T) {
	cause := errors.New("open /Users/secret/path: permission denied")
	e := Wrap(CodeLocalPathMissing, cause)
	e.Field = "localPath"
	if strings.Contains(e.Error(), "secret") {
		t.Errorf("Error() leaks the cause: %q", e.Error())
	}
	if !errors.Is(e, cause) {
		t.Error("Unwrap must expose the cause for logs")
	}
	if !errors.Is(e, New(CodeLocalPathMissing)) {
		t.Error("errors.Is must match by code")
	}
	if errors.Is(e, New(CodeDuplicate)) {
		t.Error("errors.Is must not match another code")
	}
}

// ValidateLocalPath applies fspath's allowlist for the running OS and maps the
// refusal to a code. The exhaustive, OS-independent tables live in the fspath
// tests (they pass the OS explicitly); this one checks the mapping on the OS the
// tests run on, so the Windows CI job exercises the Windows column and the Unix
// jobs the Unix one.
func TestLocalPathRulesFollowTheOperatingSystem(t *testing.T) {
	const ok = Code("")
	tests := []struct {
		path string
		win  Code // expected on Windows ("" = accepted)
		unix Code // expected elsewhere
	}{
		{`C:\Users\me\repo`, ok, CodeLocalPathInvalid},
		{`c:\users\me\repo`, ok, CodeLocalPathInvalid}, // drive letter and folder case are the user's business
		{`C:/Users/me/repo`, ok, CodeLocalPathInvalid}, // forward slashes are valid on Windows
		{"/home/me/repo", CodeLocalPathInvalid, ok},
		{"/", CodeLocalPathInvalid, ok},
		{"/a b/c", CodeLocalPathInvalid, ok},

		// Not absolute, on both.
		{`C:repo`, CodeLocalPathInvalid, CodeLocalPathInvalid}, // relative to the current directory of drive C
		{`\repo`, CodeLocalPathInvalid, CodeLocalPathInvalid},  // rooted, but of whichever drive is current
		{`repo\sub`, CodeLocalPathInvalid, CodeLocalPathInvalid},
		{"repo/sub", CodeLocalPathInvalid, CodeLocalPathInvalid},
		{".", CodeLocalPathInvalid, CodeLocalPathInvalid},
		{"~/repo", CodeLocalPathInvalid, CodeLocalPathInvalid},

		// Network and device spellings: refused as such on Windows (where they
		// would make the machine authenticate to a host); on Unix they are
		// ordinary relative names.
		{`\\attacker\share\repo`, CodeLocalPathNetwork, CodeLocalPathInvalid},
		{`\\?\UNC\attacker\share\repo`, CodeLocalPathNetwork, CodeLocalPathInvalid},
		{`\\?\C:\Users\me`, CodeLocalPathNetwork, CodeLocalPathInvalid},
		{`\\.\pipe\x`, CodeLocalPathNetwork, CodeLocalPathInvalid},
		{`\??\UNC\attacker\share\repo`, CodeLocalPathNetwork, CodeLocalPathInvalid}, // NT namespace
		{`//attacker/share/repo`, CodeLocalPathNetwork, ok},                         // "/attacker/share/repo" on Unix
	}
	for _, tc := range tests {
		want := tc.unix
		if runtime.GOOS == "windows" {
			want = tc.win
		}
		err := ValidateLocalPath(tc.path)
		if got := codeOf(err); got != want {
			t.Errorf("ValidateLocalPath(%q) = %v (code %q), want code %q on %s", tc.path, err, got, want, runtime.GOOS)
		}
		// CleanLocalPath agrees, with whitespace around the input too.
		_, cerr := CleanLocalPath("  " + tc.path + " \t")
		if got := codeOf(cerr); got != want {
			t.Errorf("CleanLocalPath(%q) code = %q, want %q on %s", tc.path, got, want, runtime.GOOS)
		}
	}
}

func TestCleanLocalPathKeepsCaseAndDrive(t *testing.T) {
	if runtime.GOOS != "windows" {
		if got, err := CleanLocalPath("/Home/Me/../Me/Repo/"); err != nil || got != "/Home/Me/Repo" {
			t.Errorf("CleanLocalPath = %q, %v", got, err)
		}
		return
	}
	tests := map[string]string{
		`C:\Users\Me\..\Me\Repo\`: `C:\Users\Me\Repo`,
		`C:/Users/Me/Repo`:        `C:\Users\Me\Repo`,
		`c:\users\me\repo`:        `c:\users\me\repo`,
	}
	for in, want := range tests {
		got, err := CleanLocalPath(in)
		if err != nil || got != want {
			t.Errorf("CleanLocalPath(%q) = %q, %v; want %q", in, got, err, want)
		}
		if err := ValidateLocalPath(got); err != nil {
			t.Errorf("the normalized form %q is rejected: %v", got, err)
		}
	}
}

func TestCleanLocalPath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "a")
	if got, err := CleanLocalPath("  " + abs + string(filepath.Separator) + "  "); err != nil || got != abs {
		t.Errorf("CleanLocalPath = %q, %v; want %q", got, err, abs)
	}
	if got, err := CleanLocalPath(" \t "); err != nil || got != "" {
		t.Errorf("blank input = %q, %v; want empty and no error", got, err)
	}
	if _, err := CleanLocalPath("relative"); codeOf(err) != CodeLocalPathInvalid {
		t.Errorf("relative path = %v", err)
	}
}
