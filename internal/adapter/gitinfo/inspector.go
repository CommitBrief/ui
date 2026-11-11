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
	fi, err := os.Stat(path)
	if err != nil {
		return gitDirs{}, &repo.Error{Code: repo.CodeLocalPathMissing, Field: "localPath", Err: err}
	}
	if !fi.IsDir() {
		return gitDirs{}, &repo.Error{Code: repo.CodeLocalPathInvalid, Field: "localPath"}
	}

	notGit := func(cause error) error {
		return &repo.Error{Code: repo.CodeLocalPathNotGit, Field: "localPath", Err: cause}
	}
	dotGit := filepath.Join(path, ".git")
	gi, err := os.Stat(dotGit)
	if err != nil {
		return gitDirs{}, notGit(err)
	}
	gitDir := dotGit
	if !gi.IsDir() {
		data, err := readLimited(ctx, dotGit, maxPointerBytes)
		if err != nil {
			return gitDirs{}, notGit(err)
		}
		target, ok := strings.CutPrefix(firstLine(string(data)), "gitdir:")
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			return gitDirs{}, notGit(nil)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(path, target)
		}
		gitDir = filepath.Clean(target)
	}
	if hi, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil || !hi.Mode().IsRegular() {
		return gitDirs{}, notGit(err)
	}

	common := gitDir
	if data, err := readLimited(ctx, filepath.Join(gitDir, "commondir"), maxPointerBytes); err == nil {
		if rel := strings.TrimSpace(firstLine(string(data))); rel != "" {
			if filepath.IsAbs(rel) {
				common = filepath.Clean(rel)
			} else {
				common = filepath.Join(gitDir, rel)
			}
		}
	}
	return gitDirs{git: gitDir, common: common}, nil
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
