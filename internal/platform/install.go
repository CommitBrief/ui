// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"os"
	"path"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// InstallKind is how this copy of the application was installed, and so which
// release artefact can replace it.
type InstallKind string

const (
	// InstallNone: nothing can replace this copy (see InstallInfo.Delegation).
	InstallNone InstallKind = ""
	// InstallBundle: a macOS .app bundle, replaced by the release's .zip.
	InstallBundle InstallKind = "bundle"
	// InstallExe: a Windows executable, replaced by the release's .zip.
	InstallExe InstallKind = "exe"
	// InstallAppImage: a Linux AppImage, replaced by the release's AppImage.
	InstallAppImage InstallKind = "appimage"
	// InstallBinary: a Linux binary in a user directory (an unpacked tarball),
	// replaced by the release's .tar.gz.
	InstallBinary InstallKind = "binary"
)

// InstallInfo is what DetectInstall found out.
type InstallInfo struct {
	Kind InstallKind
	// Target is the path that would be replaced (the .app, the .exe, the
	// AppImage or the binary). Empty when Delegation is set.
	Target string
	// Delegation is non-empty when this copy is not self-updated.
	Delegation update.Delegation
}

// linuxPackagePrefixes are directories only a package manager writes to.
var linuxPackagePrefixes = []string{"/usr/", "/opt/", "/bin/", "/sbin/", "/snap/", "/app/", "/nix/", "/var/lib/flatpak/", "/var/lib/snapd/"}

// DetectInstall decides whether and how the running copy can update itself. exe
// is the running executable (os.Executable), env reads an environment variable
// and writable says whether the user can create files in a directory. All
// three are parameters, and the path rules follow goos rather than the host,
// so every operating system's rules are tested on any machine.
func DetectInstall(goos, exe string, env func(string) string, writable func(dir string) bool) InstallInfo {
	switch goos {
	case "darwin":
		bundle, ok := macBundle(exe)
		if !ok {
			return InstallInfo{Delegation: update.DelegationUnbundled}
		}
		// The swap renames the bundle and stages beside it: its parent
		// directory has to be writable (/Applications is, for an admin user).
		if !writable(path.Dir(bundle)) {
			return InstallInfo{Delegation: update.DelegationNotWritable}
		}
		return InstallInfo{Kind: InstallBundle, Target: bundle}
	case "windows":
		dir := windowsDir(exe)
		if dir == "" || !writable(dir) {
			return InstallInfo{Delegation: update.DelegationNotWritable}
		}
		return InstallInfo{Kind: InstallExe, Target: exe}
	case "linux":
		// An AppImage runs from a read-only mount; the file to replace is the
		// image itself ($APPIMAGE), valid only when exe really lives under the
		// mount ($APPDIR), because the variable is inherited by child processes.
		if img, mount := env("APPIMAGE"), env("APPDIR"); img != "" && mount != "" && strings.HasPrefix(exe, path.Clean(mount)+"/") {
			if !writable(path.Dir(img)) {
				return InstallInfo{Delegation: update.DelegationNotWritable}
			}
			return InstallInfo{Kind: InstallAppImage, Target: img}
		}
		for _, p := range linuxPackagePrefixes {
			if strings.HasPrefix(exe, p) {
				return InstallInfo{Delegation: update.DelegationPackageManager}
			}
		}
		if !writable(path.Dir(exe)) {
			return InstallInfo{Delegation: update.DelegationNotWritable}
		}
		return InstallInfo{Kind: InstallBinary, Target: exe}
	}
	return InstallInfo{Delegation: update.DelegationUnsupported}
}

// macBundle returns the enclosing .app of a macOS executable, as Wails'
// bundleTarget does: the first path element that ends in ".app" and is an
// ancestor of the executable.
func macBundle(exe string) (string, bool) {
	exe = path.Clean(exe)
	if !strings.HasPrefix(exe, "/") {
		return "", false
	}
	parts := strings.Split(exe, "/")
	for i := 1; i < len(parts)-1; i++ {
		if strings.HasSuffix(parts[i], ".app") && len(parts[i]) > len(".app") {
			return "/" + strings.Join(parts[1:i+1], "/"), true
		}
	}
	return "", false
}

// windowsDir is the directory of a Windows path (either separator).
func windowsDir(p string) string {
	i := strings.LastIndexAny(p, `\/`)
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// ShipsReleaseFor reports whether the release workflow builds an artefact for
// this operating system and architecture: macOS (one universal build), Windows
// amd64 and Linux amd64. Anything else has no artefact to update to.
func ShipsReleaseFor(goos, goarch string) bool {
	switch goos {
	case "darwin":
		return goarch == "arm64" || goarch == "amd64"
	case "windows", "linux":
		return goarch == "amd64"
	}
	return false
}

// DirWritable reports whether the user can create a file in dir. It tries it:
// permission bits alone do not say (ACLs, read-only volumes).
func DirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// ArtifactSuffix is the end of the release artefact name that replaces a copy
// installed as kind ("" when none does).
func ArtifactSuffix(kind InstallKind) string {
	switch kind {
	case InstallBundle, InstallExe:
		return ".zip"
	case InstallAppImage:
		return ".AppImage"
	case InstallBinary:
		return ".tar.gz"
	}
	return ""
}
