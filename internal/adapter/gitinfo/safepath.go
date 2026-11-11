// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/CommitBrief/ui/internal/domain/fspath"
	"github.com/CommitBrief/ui/internal/domain/repo"
)

// maxSymlinks bounds link expansion, like the 40 of most kernels.
const maxSymlinks = 40

var errTooManyLinks = errors.New("gitinfo: too many levels of symbolic links")

// linkTargetOS selects the rule set for strings found inside a checkout (link
// targets, "gitdir:" and "commondir" values). It is the running OS; tests set it
// to "windows" to exercise the Windows rules on any machine.
var linkTargetOS = runtime.GOOS

func networkError(cause error) error {
	return &repo.Error{Code: repo.CodeLocalPathNetwork, Field: "localPath", Err: cause}
}

// screenLocal applies the allowlist to an absolute path that is about to be
// used, and classifies the refusal.
func screenLocal(p string) error {
	return classify(fspath.ValidateLocal(p))
}

// screenTarget applies the allowlist to a string that came from the contents of
// a checkout and may be relative.
func screenTarget(t string) error {
	return classify(fspath.ValidateTargetFor(linkTargetOS, t))
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fspath.ErrNetwork):
		return networkError(err)
	default:
		return &repo.Error{Code: repo.CodeLocalPathInvalid, Field: "localPath", Err: err}
	}
}

// resolveLocal resolves p like filepath.EvalSymlinks would, one component at a
// time, with one difference: it never follows a symbolic link whose target is a
// network or device path (fspath.ValidateTarget). Such a link is refused with
// repo_local_path_network instead.
//
// Why not filepath.EvalSymlinks: it follows links as it goes, so by the time it
// could report a UNC result it has already stat-ed the UNC path, and on Windows
// that makes the machine authenticate (NTLM) to the host named in the path.
// Link targets are attacker influenced (a checkout can contain a ".git" that
// is a link, or a "gitdir:" line), so every target is screened as a string
// before it is used.
//
// The returned path contains no symbolic links. p must pass the allowlist
// (fspath.ValidateLocal: a plain absolute local path); resolveLocal checks that
// before any file system call.
func resolveLocal(p string) (string, error) {
	// The allowlist first, on the string as given and again once cleaned: only
	// plain absolute local paths get as far as the first Lstat.
	if err := screenLocal(p); err != nil {
		return "", err
	}
	p = filepath.Clean(p)
	if err := screenLocal(p); err != nil {
		return "", err
	}

	vol := filepath.VolumeName(p)
	cur := vol + string(filepath.Separator)
	pending := splitComponents(p[len(vol):])
	links := 0
	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		switch comp {
		case ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, comp)
		fi, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		// Windows reports some reparse points as irregular rather than as
		// symlinks; Readlink tells them apart (it fails on a plain
		// directory or file).
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				cur = next // not a link after all
				continue
			}
			return "", err
		}
		if err := screenTarget(target); err != nil {
			return "", err
		}
		links++
		if links > maxSymlinks {
			return "", errTooManyLinks
		}
		switch {
		case filepath.IsAbs(target):
			tv := filepath.VolumeName(target)
			cur = tv + string(filepath.Separator)
			pending = append(splitComponents(target[len(tv):]), pending...)
		case len(target) > 0 && os.IsPathSeparator(target[0]):
			// Rooted without a drive, which only happens on Windows (a rooted
			// path is absolute on Unix): the root of the current drive. A
			// drive-relative target ("C:rel") never gets here; screenTarget
			// has refused it.
			cur = filepath.VolumeName(cur) + string(filepath.Separator)
			pending = append(splitComponents(target), pending...)
		default:
			pending = append(splitComponents(target), pending...)
		}
	}
	return cur, nil
}

// splitComponents splits a path on the separators of the running OS and drops
// empty parts. That is "/" on Unix, where a backslash is an ordinary file name
// character, and both "/" and "\" on Windows. Splitting on anything else would
// resolve a different directory than the one the OS opens.
func splitComponents(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r < utf8.RuneSelf && os.IsPathSeparator(uint8(r)) })
}
