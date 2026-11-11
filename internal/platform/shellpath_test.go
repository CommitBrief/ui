// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import "testing"

func TestParseLoginShellPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"clean", pathMarkerStart + "/opt/homebrew/bin:/usr/bin" + pathMarkerEnd, "/opt/homebrew/bin:/usr/bin", true},
		{"banner and rc noise around it", "Welcome!\nnvm loaded\n" + pathMarkerStart + "/a:/b" + pathMarkerEnd + "\nbye\n", "/a:/b", true},
		{"the last marker wins", pathMarkerStart + "/stale" + pathMarkerEnd + "x" + pathMarkerStart + "/fresh" + pathMarkerEnd, "/fresh", true},
		{"no marker", "/usr/bin:/bin\n", "", false},
		{"unterminated", pathMarkerStart + "/usr/bin", "", false},
		{"empty value", pathMarkerStart + "  " + pathMarkerEnd, "", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseLoginShellPath(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("ParseLoginShellPath = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
