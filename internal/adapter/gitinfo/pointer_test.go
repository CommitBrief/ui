// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPointerPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		base := `C:\w\repo\.git`
		for target, want := range map[string]string{
			`wt\x`:         `C:\w\repo\.git\wt\x`,
			`..\x`:         `C:\w\repo\x`,
			`\repos\x.git`: `C:\repos\x.git`, // rooted, no drive: the drive of base, as git does
			`/repos/x.git`: `C:/repos/x.git`,
			`D:\y`:         `D:\y`,
		} {
			if got := pointerPath(base, target); filepath.Clean(got) != filepath.Clean(want) {
				t.Errorf("pointerPath(%q, %q) = %q, want %q", base, target, got, want)
			}
		}
		return
	}
	base := "/w/repo/.git"
	for target, want := range map[string]string{
		"wt/x":   "/w/repo/.git/wt/x",
		"../x":   "/w/repo/x",
		"/abs/x": "/abs/x",
		`\foo`:   `/w/repo/.git/\foo`, // a backslash is a name character here
		`a\..\b`: `/w/repo/.git/a\..\b`,
	} {
		if got := pointerPath(base, target); got != want {
			t.Errorf("pointerPath(%q, %q) = %q, want %q", base, target, got, want)
		}
	}
}
