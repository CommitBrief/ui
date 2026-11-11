// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"testing"

	"github.com/CommitBrief/ui/internal/port"
)

func TestParseVersionOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want port.CLIVersion
		err  bool
	}{
		{
			name: "release",
			in:   "commitbrief 1.21.2 (commit abc1234, built 2026-10-31T10:00:00Z)\n",
			want: port.CLIVersion{Major: 1, Minor: 21, Patch: 2, Raw: "1.21.2", Commit: "abc1234", Date: "2026-10-31T10:00:00Z"},
		},
		{
			name: "v prefix and prerelease",
			in:   "commitbrief v1.22.0-rc.1 (commit def, built unknown)",
			want: port.CLIVersion{Major: 1, Minor: 22, Pre: "rc.1", Raw: "v1.22.0-rc.1", Commit: "def", Date: "unknown"},
		},
		{
			name: "banner noise before the line",
			in:   "some banner\n\ncommitbrief 1.30.0 (commit a, built b)\n",
			want: port.CLIVersion{Major: 1, Minor: 30, Raw: "1.30.0", Commit: "a", Date: "b"},
		},
		{
			name: "source build",
			in:   "commitbrief dev (commit none, built unknown)\n",
			want: port.CLIVersion{Dev: true, Raw: "dev", Commit: "none", Date: "unknown"},
		},
		{name: "another program", in: "gh version 2.40.0\n", err: true},
		{name: "empty", in: "", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVersionOutput(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("got %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestCapabilityTable(t *testing.T) {
	v := func(major, minor, patch int, pre string) port.CLIVersion {
		return port.CLIVersion{Major: major, Minor: minor, Patch: patch, Pre: pre}
	}
	tests := []struct {
		name          string
		ver           port.CLIVersion
		base, posting bool
	}{
		{"older than base", v(1, 21, 1, ""), false, false},
		{"exact base", v(1, 21, 2, ""), true, false},
		{"snapshot after base", v(1, 21, 3, "snapshot-abc"), true, false},
		{"posting release candidate", v(1, 22, 0, "rc.1"), true, false},
		{"exact posting", v(1, 22, 0, ""), true, true},
		{"later", v(1, 30, 4, ""), true, true},
		{"next major", v(2, 0, 0, ""), true, true},
		{"source build proves nothing beyond the base", port.CLIVersion{Dev: true, Raw: "dev"}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Satisfies(tc.ver, CapBase); got != tc.base {
				t.Errorf("base = %v, want %v", got, tc.base)
			}
			if got := Satisfies(tc.ver, CapPosting); got != tc.posting {
				t.Errorf("posting = %v, want %v", got, tc.posting)
			}
		})
	}
	if MinVersion(CapBase) != "1.21.2" || MinVersion(CapPosting) != "1.22.0" || MinVersion(CapConfigSetStdin) != "1.22.0" {
		t.Errorf("table = base %s, posting %s, config_set_stdin %s", MinVersion(CapBase), MinVersion(CapPosting), MinVersion(CapConfigSetStdin))
	}
	dev := port.CLIVersion{Dev: true, Raw: "dev"}
	if !Satisfies(dev, CapBase) || Satisfies(dev, CapConfigSetStdin) || Satisfies(dev, CapPosting) || dev.AtLeast(0, 0, 0) {
		t.Error("a dev build must pass the base gate only")
	}
	if !Satisfies(v(1, 22, 0, ""), CapConfigSetStdin) || Satisfies(v(1, 21, 2, ""), CapConfigSetStdin) || Satisfies(v(1, 22, 0, "rc.1"), CapConfigSetStdin) {
		t.Error("config_set_stdin gate is wrong")
	}
}
