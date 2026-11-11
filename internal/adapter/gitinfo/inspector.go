// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

// Read limits: a legitimate .git file or config is far below these.
const (
	maxConfigBytes  = 1 << 20
	maxPointerBytes = 4 << 10
)

// Inspector implements port.LocalRepoInspector by reading files under .git.
type Inspector struct{}

// New returns an Inspector.
func New() *Inspector { return &Inspector{} }

var _ port.LocalRepoInspector = (*Inspector)(nil)

// CheckRoot verifies that path is the root of a git work tree.
func (Inspector) CheckRoot(ctx context.Context, path string) error {
	_, err := locate(ctx, path)
	return err
}

// Origin returns the repository the "origin" remote points at, like
// `git config --get remote.origin.url` followed by ParseRemoteURL.
func (Inspector) Origin(ctx context.Context, path string) (port.RemoteRef, error) {
	dirs, err := locate(ctx, path)
	if err != nil {
		return port.RemoteRef{}, err
	}
	data, err := readLimited(ctx, filepath.Join(dirs.common, "config"), maxConfigBytes)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return port.RemoteRef{}, ctxErr
		}
		var re *repo.Error
		if errors.As(err, &re) && re.Code == repo.CodeLocalPathNetwork {
			return port.RemoteRef{}, err
		}
		if errors.Is(err, os.ErrNotExist) {
			return port.RemoteRef{}, repo.New(repo.CodeNoRemote)
		}
		return port.RemoteRef{}, repo.Wrap(repo.CodeNoRemote, err)
	}
	raw, ok := originURL(string(data))
	if !ok {
		return port.RemoteRef{}, repo.New(repo.CodeNoRemote)
	}
	return ParseRemoteURL(raw)
}

type gitDirs struct {
	// git is the repository's git directory (.git, or the target of a
	// ".git" pointer file); common holds the shared config. They differ for
	// linked worktrees.
	git    string
	common string
}

// locate validates path and finds its git directories. A plain checkout has a
// ".git" directory; a linked worktree or submodule has a ".git" file that
// points at the real git directory.
//
// Nothing that could come from outside reaches the file system unscreened: the
// path itself is checked as a string first, and every path derived from the
// checkout's own contents (the ".git" entry, a "gitdir:" or "commondir" line,
// symbolic links anywhere along the way) goes through resolveLocal, which
// refuses network and device paths (a UNC path makes Windows authenticate to
// its host the moment it is touched).
func locate(ctx context.Context, path string) (gitDirs, error) {
	if err := ctx.Err(); err != nil {
		return gitDirs{}, err
	}
	if path == "" {
		return gitDirs{}, &repo.Error{Code: repo.CodeLocalPathInvalid, Field: "localPath"}
	}
	if err := repo.ValidateLocalPath(path); err != nil {
		return gitDirs{}, err
	}
	root, err := resolveLocal(path)
	if err != nil {
		return gitDirs{}, fromResolve(err, repo.CodeLocalPathMissing)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return gitDirs{}, &repo.Error{Code: repo.CodeLocalPathMissing, Field: "localPath", Err: err}
	}
	if !fi.IsDir() {
		return gitDirs{}, &repo.Error{Code: repo.CodeLocalPathInvalid, Field: "localPath"}
	}

	notGit := func(cause error) error {
		return &repo.Error{Code: repo.CodeLocalPathNotGit, Field: "localPath", Err: cause}
	}
	dotGit, err := resolveLocal(filepath.Join(root, ".git"))
	if err != nil {
		return gitDirs{}, fromResolve(err, repo.CodeLocalPathNotGit)
	}
	gi, err := os.Stat(dotGit)
	if err != nil {
		return gitDirs{}, notGit(err)
	}
	gitDir := dotGit
	if !gi.IsDir() {
		data, err := readLimited(ctx, dotGit, maxPointerBytes)
		if err != nil {
			return gitDirs{}, fromResolve(err, repo.CodeLocalPathNotGit)
		}
		target, ok := strings.CutPrefix(firstLine(string(data)), "gitdir:")
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			return gitDirs{}, notGit(nil)
		}
		gitDir, err = resolvePointer(root, target)
		if err != nil {
			return gitDirs{}, fromResolve(err, repo.CodeLocalPathNotGit)
		}
	}
	if hi, err := statLocal(filepath.Join(gitDir, "HEAD")); err != nil || !hi.Mode().IsRegular() {
		return gitDirs{}, fromResolve(err, repo.CodeLocalPathNotGit)
	}

	common := gitDir
	data, err := readLimited(ctx, filepath.Join(gitDir, "commondir"), maxPointerBytes)
	if err != nil {
		var re *repo.Error
		if errors.As(err, &re) && re.Code == repo.CodeLocalPathNetwork {
			return gitDirs{}, err
		}
	} else if rel := strings.TrimSpace(firstLine(string(data))); rel != "" {
		c, err := resolvePointer(gitDir, rel)
		if err != nil {
			var re *repo.Error
			if errors.As(err, &re) && re.Code == repo.CodeLocalPathNetwork {
				return gitDirs{}, err
			}
			// An unusable commondir is ignored: the git dir itself is used.
		} else {
			common = c
		}
	}
	return gitDirs{git: gitDir, common: common}, nil
}

// resolvePointer turns a path found inside a checkout (a "gitdir:" or
// "commondir" value) into a resolved absolute directory: a network or device
// path is refused as a string, a relative one is taken relative to base, and
// links are resolved without ever following one that leads to the network.
func resolvePointer(base, target string) (string, error) {
	if err := screenTarget(target); err != nil {
		return "", err
	}
	return resolveLocal(pointerPath(base, target))
}

// pointerPath makes a "gitdir:" or "commondir" value absolute the way git does:
// an absolute value stays as it is, a value rooted without a drive (Windows
// "\foo") lives on the drive of base, and anything else is relative to base.
func pointerPath(base, target string) string {
	switch {
	case filepath.IsAbs(target):
		return target
	case target != "" && os.IsPathSeparator(target[0]):
		return filepath.VolumeName(base) + target
	default:
		return filepath.Join(base, target)
	}
}

// statLocal is os.Stat after resolveLocal.
func statLocal(p string) (os.FileInfo, error) {
	r, err := resolveLocal(p)
	if err != nil {
		return nil, err
	}
	return os.Stat(r)
}

// fromResolve maps an error from resolveLocal or readLimited to what callers
// report: a classified error passes through unchanged, anything else (the path
// does not exist, cannot be read) becomes the given code.
func fromResolve(err error, code repo.Code) error {
	if err == nil {
		return &repo.Error{Code: code, Field: "localPath"}
	}
	var re *repo.Error
	if errors.As(err, &re) {
		return err
	}
	return &repo.Error{Code: code, Field: "localPath", Err: err}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// readLimited reads at most limit bytes of a regular file; a larger file is an
// error (not silently truncated). Anything that is not a regular file (a FIFO,
// a device, a socket, a symlink to one) is refused before it is opened: opening
// a FIFO for reading blocks until someone writes to it, which would hang the
// caller forever. The file is checked again after the open to narrow the window
// in which the path could be swapped.
func readLimited(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// resolveLocal: refuse network and device paths, and links that lead to
	// them, before any open or stat of the target.
	path, err := resolveLocal(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if opened, err := f.Stat(); err != nil || !opened.Mode().IsRegular() {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("gitinfo: file too large")
	}
	return data, nil
}

var errNotRegular = errors.New("gitinfo: not a regular file")
