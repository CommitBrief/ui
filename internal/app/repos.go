// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

// Export file format. The file is versioned so that later releases can add
// fields (and, in a later phase, job definitions) without breaking older
// exports: unknown fields are ignored on import, a newer Version is refused.
const (
	ExportSchema  = "commitbrief-ui.repositories"
	ExportVersion = 1

	// MaxImportBytes and MaxImportEntries bound what an import file may ask
	// for; a real repository list is a few kilobytes. MaxImportBytes stays well
	// below the 4 MiB Wails runtime body limit (desktop/security.go): the file
	// travels as a JSON string argument, which escaping makes larger, and a file
	// that is over that limit must reach ImportJSON to be answered with
	// repo_import_too_large instead of a transport error.
	MaxImportBytes   = 2 << 20
	MaxImportEntries = 10000
)

// RepoInput is what the user types in the repository form.
type RepoInput struct {
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	LocalPath string `json:"localPath"`
}

// RemoteSuggestion is the owner/name derived from a checkout's origin remote.
type RemoteSuggestion struct {
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
	// AlreadyAdded is true when that owner/name is already in the list.
	AlreadyAdded bool `json:"alreadyAdded"`
}

// ImportResult summarizes an import.
type ImportResult struct {
	// Created counts new repositories.
	Created int `json:"created"`
	// Skipped counts entries that were already in the list (or repeated in
	// the file); existing rows are never overwritten.
	Skipped int `json:"skipped"`
	// PathsDropped counts created repositories whose local path was not kept:
	// it is not a git checkout on this machine, or it is a network or device
	// path (never probed).
	PathsDropped int `json:"pathsDropped"`
}

// Repos is the repository use-case service.
type Repos struct {
	store   port.RepoStore
	tx      port.TxRunner
	inspect port.LocalRepoInspector
	now     func() time.Time
}

// NewRepos wires the service. tx must operate on the same database as store.
func NewRepos(store port.RepoStore, tx port.TxRunner, inspect port.LocalRepoInspector) *Repos {
	return &Repos{store: store, tx: tx, inspect: inspect, now: time.Now}
}

// List returns every repository ordered by owner and name.
func (s *Repos) List(ctx context.Context) ([]repo.Repository, error) {
	return s.store.List(ctx)
}

// Get returns one repository.
func (s *Repos) Get(ctx context.Context, id int64) (repo.Repository, error) {
	r, err := s.store.Get(ctx, id)
	return r, mapStoreErr(err)
}

// Add validates the input and stores a new, enabled repository. A local path
// must be an absolute path to the root of a git work tree.
func (s *Repos) Add(ctx context.Context, in RepoInput) (repo.Repository, error) {
	r := repo.Repository{
		Owner:   strings.TrimSpace(in.Owner),
		Name:    strings.TrimSpace(in.Name),
		Enabled: true,
	}
	if err := r.Validate(); err != nil {
		return repo.Repository{}, err
	}
	// The path is screened (network and device paths refused) by string
	// inspection before the inspector, which touches the file system, sees it.
	var err error
	if r.LocalPath, err = repo.CleanLocalPath(in.LocalPath); err != nil {
		return repo.Repository{}, err
	}
	if r.LocalPath != "" {
		if err := s.inspect.CheckRoot(ctx, r.LocalPath); err != nil {
			return repo.Repository{}, err
		}
	}
	created, err := s.store.Create(ctx, r)
	return created, mapStoreErr(err)
}

// Update replaces owner, name and local path of a repository. The enabled flag
// is not part of it (SetEnabled owns it), and the store writes only those three
// columns, so a form save and a toggle never overwrite each other. The local
// path is only re-checked against the file system when it changes, so renaming a
// repository whose checkout has moved still works.
func (s *Repos) Update(ctx context.Context, id int64, in RepoInput) (repo.Repository, error) {
	cur, err := s.store.Get(ctx, id)
	if err != nil {
		return repo.Repository{}, mapStoreErr(err)
	}
	next := cur
	next.Owner = strings.TrimSpace(in.Owner)
	next.Name = strings.TrimSpace(in.Name)
	if err := next.Validate(); err != nil {
		return repo.Repository{}, err
	}
	// Screened even when it equals the stored path: nothing from outside is
	// trusted to reach the file system without the string check.
	if next.LocalPath, err = repo.CleanLocalPath(in.LocalPath); err != nil {
		return repo.Repository{}, err
	}
	if next.LocalPath != "" && next.LocalPath != cur.LocalPath {
		if err := s.inspect.CheckRoot(ctx, next.LocalPath); err != nil {
			return repo.Repository{}, err
		}
	}
	if err := s.store.Update(ctx, next); err != nil {
		return repo.Repository{}, mapStoreErr(err)
	}
	// Return what is stored now, including an enabled flag that may have
	// been toggled since the read above.
	out, err := s.store.Get(ctx, id)
	return out, mapStoreErr(err)
}

// SetEnabled turns a repository on or off without touching anything else.
func (s *Repos) SetEnabled(ctx context.Context, id int64, enabled bool) (repo.Repository, error) {
	if err := s.store.SetEnabled(ctx, id, enabled); err != nil {
		return repo.Repository{}, mapStoreErr(err)
	}
	out, err := s.store.Get(ctx, id)
	return out, mapStoreErr(err)
}

// Delete removes a repository, its job targets and tracked PRs; run history is
// kept.
func (s *Repos) Delete(ctx context.Context, id int64) error {
	return mapStoreErr(s.store.Delete(ctx, id))
}

// SuggestFromLocalPath reads the origin remote of the checkout at path and
// proposes owner/name for the form.
func (s *Repos) SuggestFromLocalPath(ctx context.Context, path string) (RemoteSuggestion, error) {
	path, err := repo.CleanLocalPath(path)
	if err != nil {
		return RemoteSuggestion{}, err
	}
	if path == "" {
		return RemoteSuggestion{}, &repo.Error{Code: repo.CodeLocalPathInvalid, Field: "localPath"}
	}
	ref, err := s.inspect.Origin(ctx, path)
	if err != nil {
		return RemoteSuggestion{}, err
	}
	out := RemoteSuggestion{Host: ref.Host, Owner: ref.Owner, Name: ref.Name}
	switch _, err := s.store.GetByRemote(ctx, ref.Owner, ref.Name); {
	case err == nil:
		out.AlreadyAdded = true
	case errors.Is(err, port.ErrNotFound):
	default:
		return RemoteSuggestion{}, err
	}
	return out, nil
}

type exportFile struct {
	Schema       string        `json:"schema"`
	Version      int           `json:"version"`
	ExportedAt   string        `json:"exportedAt,omitempty"`
	Repositories []exportEntry `json:"repositories"`
}

type exportEntry struct {
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	LocalPath string `json:"localPath,omitempty"`
	// Enabled is a pointer so that a file written by hand without the field
	// means "enabled" rather than "disabled".
	Enabled *bool `json:"enabled,omitempty"`
}

// ExportJSON renders the repository list as an indented JSON document.
func (s *Repos) ExportJSON(ctx context.Context) (string, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return "", err
	}
	f := exportFile{
		Schema:       ExportSchema,
		Version:      ExportVersion,
		ExportedAt:   s.now().UTC().Format(time.RFC3339),
		Repositories: make([]exportEntry, 0, len(list)),
	}
	for _, r := range list {
		enabled := r.Enabled
		f.Repositories = append(f.Repositories, exportEntry{Owner: r.Owner, Name: r.Name, LocalPath: r.LocalPath, Enabled: &enabled})
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// ImportJSON adds the repositories of an export file. It is all-or-nothing: an
// invalid entry (reported by position) aborts the import before anything is
// written. Repositories already in the list are skipped, not overwritten. A
// local path that is not a git checkout on this machine (an export usually
// comes from another one) is dropped, and the repository is still imported.
func (s *Repos) ImportJSON(ctx context.Context, data string) (ImportResult, error) {
	if len(data) > MaxImportBytes {
		return ImportResult{}, repo.New(repo.CodeImportTooLarge)
	}
	var f exportFile
	if err := json.Unmarshal([]byte(data), &f); err != nil {
		return ImportResult{}, repo.Wrap(repo.CodeImportInvalid, err)
	}
	if f.Schema != ExportSchema || f.Version < 1 {
		return ImportResult{}, repo.New(repo.CodeImportInvalid)
	}
	if f.Version > ExportVersion {
		return ImportResult{}, repo.New(repo.CodeImportSchema)
	}
	if len(f.Repositories) > MaxImportEntries {
		return ImportResult{}, repo.New(repo.CodeImportTooLarge)
	}

	// Validate everything (and probe the paths) before the write transaction
	// starts, so the write lock is not held across file system access.
	type candidate struct {
		r       repo.Repository
		dropped bool
	}
	cands := make([]candidate, 0, len(f.Repositories))
	for i, e := range f.Repositories {
		r := repo.Repository{
			Owner:   strings.TrimSpace(e.Owner),
			Name:    strings.TrimSpace(e.Name),
			Enabled: e.Enabled == nil || *e.Enabled,
		}
		if err := repo.ValidateOwner(r.Owner); err != nil {
			return ImportResult{}, atEntry(err, i+1)
		}
		if err := repo.ValidateName(r.Name); err != nil {
			return ImportResult{}, atEntry(err, i+1)
		}
		c := candidate{r: r}
		if strings.TrimSpace(e.LocalPath) != "" {
			// An import file comes from outside: a network or device path in
			// it (a UNC path would make Windows authenticate to that host the
			// moment it is touched) is refused by string inspection and
			// dropped, never handed to the inspector.
			lp, err := repo.CleanLocalPath(e.LocalPath)
			switch {
			case err != nil:
				c.dropped = true
			default:
				if err := s.inspect.CheckRoot(ctx, lp); err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return ImportResult{}, ctxErr
					}
					c.dropped = true
				} else {
					c.r.LocalPath = lp
				}
			}
		}
		cands = append(cands, c)
	}

	var res ImportResult
	err := s.tx.InTx(ctx, func(tx port.Tx) error {
		res = ImportResult{}
		store := tx.Repos()
		for _, c := range cands {
			switch _, err := store.GetByRemote(ctx, c.r.Owner, c.r.Name); {
			case err == nil:
				res.Skipped++
				continue
			case !errors.Is(err, port.ErrNotFound):
				return err
			}
			if _, err := store.Create(ctx, c.r); err != nil {
				return mapStoreErr(err)
			}
			res.Created++
			if c.dropped {
				res.PathsDropped++
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// atEntry stamps the 1-based position of an import entry on a repo error.
func atEntry(err error, entry int) error {
	var e *repo.Error
	if errors.As(err, &e) {
		c := *e
		c.Entry = entry
		return &c
	}
	return err
}

// mapStoreErr turns the store's sentinel errors into domain error codes and
// leaves everything else (I/O failures, canceled contexts) untouched.
func mapStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, port.ErrNotFound):
		return repo.Wrap(repo.CodeNotFound, err)
	case errors.Is(err, port.ErrConflict):
		return repo.Wrap(repo.CodeDuplicate, err)
	default:
		return err
	}
}
