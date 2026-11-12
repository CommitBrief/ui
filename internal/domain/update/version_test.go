// SPDX-License-Identifier: GPL-3.0-or-later

package update

import "testing"

func TestParseVersion(t *testing.T) {
	good := map[string]string{
		"1.2.3":            "1.2.3",
		"v1.2.3":           "1.2.3",
		"0.0.1-test.1":     "0.0.1-test.1",
		"v0.0.1-test.2":    "0.0.1-test.2",
		"1.0.0+build.5":    "1.0.0",
		"1.0.0-rc.1+b":     "1.0.0-rc.1",
		"10.20.30":         "10.20.30",
		"1.0.0-alpha-beta": "1.0.0-alpha-beta",
	}
	for in, want := range good {
		v, err := ParseVersion(in)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", in, err)
			continue
		}
		if v.String() != want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", in, v.String(), want)
		}
	}
	bad := []string{"", "v", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.x", "1.2.3-", "1.2.3-a..b",
		"1.2.3-01", "1.2.3+", "1.2.3 ", "-1.2.3", "1.2.3-ü", "latest", "1.2.99999999999999999999"}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) accepted", in)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	// Ascending: semver.org section 11 example plus the test-release tags.
	order := []string{
		"0.0.1-test.1", "0.0.1-test.2", "0.0.1-test.10", "0.0.1", "0.1.0-rc.1", "0.1.0",
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
	}
	for i, a := range order {
		for j, b := range order {
			va, _ := ParseVersion(a)
			vb, _ := ParseVersion(b)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := va.Compare(vb); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
	a, _ := ParseVersion("1.0.0+one")
	b, _ := ParseVersion("1.0.0+two")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}

func TestChannelAccepts(t *testing.T) {
	rel, _ := ParseVersion("1.0.0")
	pre, _ := ParseVersion("1.0.0-rc.1")
	if !ChannelStable.Accepts(rel) || ChannelStable.Accepts(pre) {
		t.Error("stable must take releases only")
	}
	if !ChannelPrerelease.Accepts(rel) || !ChannelPrerelease.Accepts(pre) {
		t.Error("prerelease must take both")
	}
	if _, ok := ParseChannel("nightly"); ok {
		t.Error("unknown channel accepted")
	}
	if c, ok := ParseChannel("prerelease"); !ok || c != ChannelPrerelease {
		t.Error("prerelease not parsed")
	}
}

func TestIsDevBuild(t *testing.T) {
	for _, v := range []string{"", DevVersion, "1.2.3-dev"} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false", v)
		}
	}
	for _, v := range []string{"0.1.0", "0.0.1-test.1", "1.0.0-rc.1"} {
		if IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = true", v)
		}
	}
}

func TestErrorShape(t *testing.T) {
	e := New(CodeInvalid, "channel")
	if e.Error() != "update_invalid (channel)" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !e.Is(New(CodeInvalid, "")) {
		t.Error("Is must match by code")
	}
}
