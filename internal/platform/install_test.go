// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/update"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func yes(string) bool { return true }
func no(string) bool  { return false }

func TestDetectInstall(t *testing.T) {
	const bundleExe = "/Applications/CommitBrief UI.app/Contents/MacOS/commitbrief-ui"
	tests := []struct {
		name     string
		goos     string
		exe      string
		env      map[string]string
		writable func(string) bool
		want     InstallInfo
	}{
		{"darwin bundle", "darwin", bundleExe, nil, yes,
			InstallInfo{Kind: InstallBundle, Target: "/Applications/CommitBrief UI.app"}},
		{"darwin bundle in a root-owned directory", "darwin", bundleExe, nil, no,
			InstallInfo{Delegation: update.DelegationNotWritable}},
		{"darwin bare binary", "darwin", "/tmp/build/commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationUnbundled}},
		{"darwin relative path", "darwin", "commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationUnbundled}},
		{"darwin the .app is the executable itself", "darwin", "/x/Foo.app", nil, yes,
			InstallInfo{Delegation: update.DelegationUnbundled}},
		{"windows per-user install", "windows", `C:\Users\me\AppData\Local\Programs\CommitBrief UI\commitbrief-ui.exe`, nil, yes,
			InstallInfo{Kind: InstallExe, Target: `C:\Users\me\AppData\Local\Programs\CommitBrief UI\commitbrief-ui.exe`}},
		{"windows program files", "windows", `C:\Program Files\CommitBrief\CommitBrief UI\commitbrief-ui.exe`, nil, no,
			InstallInfo{Delegation: update.DelegationNotWritable}},
		{"windows no directory", "windows", `commitbrief-ui.exe`, nil, yes,
			InstallInfo{Delegation: update.DelegationNotWritable}},
		{"linux appimage", "linux", "/tmp/.mount_abc/usr/bin/commitbrief-ui",
			map[string]string{"APPIMAGE": "/home/me/Apps/commitbrief-ui.AppImage", "APPDIR": "/tmp/.mount_abc"}, yes,
			InstallInfo{Kind: InstallAppImage, Target: "/home/me/Apps/commitbrief-ui.AppImage"}},
		{"linux appimage in a read-only directory", "linux", "/tmp/.mount_abc/usr/bin/commitbrief-ui",
			map[string]string{"APPIMAGE": "/opt/Apps/x.AppImage", "APPDIR": "/tmp/.mount_abc"}, no,
			InstallInfo{Delegation: update.DelegationNotWritable}},
		{"linux inherited APPIMAGE does not make a child an appimage", "linux", "/home/me/.local/bin/commitbrief-ui",
			map[string]string{"APPIMAGE": "/home/me/Apps/x.AppImage", "APPDIR": "/tmp/.mount_abc"}, yes,
			InstallInfo{Kind: InstallBinary, Target: "/home/me/.local/bin/commitbrief-ui"}},
		{"linux deb", "linux", "/usr/bin/commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationPackageManager}},
		{"linux local package prefix", "linux", "/usr/local/bin/commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationPackageManager}},
		{"linux flatpak", "linux", "/app/bin/commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationPackageManager}},
		{"linux tarball in the home directory", "linux", "/home/me/.local/bin/commitbrief-ui", nil, yes,
			InstallInfo{Kind: InstallBinary, Target: "/home/me/.local/bin/commitbrief-ui"}},
		{"linux tarball in a directory the user cannot write", "linux", "/srv/tools/commitbrief-ui", nil, no,
			InstallInfo{Delegation: update.DelegationNotWritable}},
		{"freebsd", "freebsd", "/usr/local/bin/commitbrief-ui", nil, yes,
			InstallInfo{Delegation: update.DelegationUnsupported}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectInstall(tc.goos, tc.exe, envOf(tc.env), tc.writable)
			if got != tc.want {
				t.Errorf("DetectInstall = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestArtifactSuffix(t *testing.T) {
	want := map[InstallKind]string{
		InstallBundle: ".zip", InstallExe: ".zip", InstallAppImage: ".AppImage", InstallBinary: ".tar.gz", InstallNone: "",
	}
	for kind, s := range want {
		if got := ArtifactSuffix(kind); got != s {
			t.Errorf("ArtifactSuffix(%q) = %q, want %q", kind, got, s)
		}
	}
}

func TestShipsReleaseFor(t *testing.T) {
	want := map[[2]string]bool{
		{"darwin", "arm64"}: true, {"darwin", "amd64"}: true,
		{"windows", "amd64"}: true, {"windows", "arm64"}: false,
		{"linux", "amd64"}: true, {"linux", "arm64"}: false,
		{"freebsd", "amd64"}: false,
	}
	for k, w := range want {
		if got := ShipsReleaseFor(k[0], k[1]); got != w {
			t.Errorf("ShipsReleaseFor(%s, %s) = %v, want %v", k[0], k[1], got, w)
		}
	}
}

func TestDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !DirWritable(dir) {
		t.Error("a fresh temp directory must be writable")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".write-probe-*")); len(left) != 0 {
		t.Errorf("the probe file was left behind: %v", left)
	}
	if DirWritable(filepath.Join(dir, "missing", "deeper")) {
		t.Error("a missing directory must not be writable")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
