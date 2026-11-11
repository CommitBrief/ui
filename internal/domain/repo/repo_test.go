// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"path/filepath"
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

func TestNormalizeLocalPath(t *testing.T) {
	base := t.TempDir()
	in := base + string(filepath.Separator) + "a" + string(filepath.Separator) + ".." + string(filepath.Separator) + "b" + string(filepath.Separator)
	if got, want := NormalizeLocalPath(in), filepath.Join(base, "b"); got != want {
		t.Errorf("NormalizeLocalPath = %q, want %q", got, want)
	}
	if got := NormalizeLocalPath(""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
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
