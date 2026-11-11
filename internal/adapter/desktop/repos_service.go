// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/repo"
)

// ReposService is the Wails-bound face of the repository use cases. Its
// exported methods, and only those, are what the frontend can call: they are
// the generated TypeScript bindings (frontend/bindings). It adds no logic of
// its own beyond shaping errors: Wails copies a returned error's text to the
// webview, so every error leaves here as a *repo.Error whose text is just a
// code, a field and an entry number. Causes (paths, driver messages) stay in
// the log.
//
// Context cancellation from the frontend (a canceled call) arrives as the
// ctx argument.
type ReposService struct {
	repos *app.Repos
	log   *slog.Logger
}

// NewReposService binds the use cases. A nil logger means slog.Default().
func NewReposService(repos *app.Repos, log *slog.Logger) *ReposService {
	if log == nil {
		log = slog.Default()
	}
	return &ReposService{repos: repos, log: log}
}

// List returns every repository.
func (s *ReposService) List(ctx context.Context) ([]repo.Repository, error) {
	out, err := s.repos.List(ctx)
	return out, s.fail("list repositories", err)
}

// Get returns one repository.
func (s *ReposService) Get(ctx context.Context, id int64) (repo.Repository, error) {
	out, err := s.repos.Get(ctx, id)
	return out, s.fail("get repository", err)
}

// Add stores a new repository.
func (s *ReposService) Add(ctx context.Context, in app.RepoInput) (repo.Repository, error) {
	out, err := s.repos.Add(ctx, in)
	return out, s.fail("add repository", err)
}

// Update changes owner, name and local path of a repository.
func (s *ReposService) Update(ctx context.Context, id int64, in app.RepoInput) (repo.Repository, error) {
	out, err := s.repos.Update(ctx, id, in)
	return out, s.fail("update repository", err)
}

// SetEnabled turns a repository on or off.
func (s *ReposService) SetEnabled(ctx context.Context, id int64, enabled bool) (repo.Repository, error) {
	out, err := s.repos.SetEnabled(ctx, id, enabled)
	return out, s.fail("set repository enabled", err)
}

// Delete removes a repository.
func (s *ReposService) Delete(ctx context.Context, id int64) error {
	return s.fail("delete repository", s.repos.Delete(ctx, id))
}

// SuggestFromLocalPath proposes owner/name from a checkout's origin remote.
func (s *ReposService) SuggestFromLocalPath(ctx context.Context, path string) (app.RemoteSuggestion, error) {
	out, err := s.repos.SuggestFromLocalPath(ctx, path)
	return out, s.fail("suggest repository from local path", err)
}

// ExportJSON renders the repository list as a JSON document.
func (s *ReposService) ExportJSON(ctx context.Context) (string, error) {
	out, err := s.repos.ExportJSON(ctx)
	return out, s.fail("export repositories", err)
}

// ImportJSON adds the repositories of an export document.
func (s *ReposService) ImportJSON(ctx context.Context, data string) (app.ImportResult, error) {
	out, err := s.repos.ImportJSON(ctx, data)
	return out, s.fail("import repositories", err)
}

// fail turns any error into the *repo.Error the frontend sees. Classified
// errors pass through (their cause is logged at debug level); a canceled
// context becomes "canceled"; everything else is logged in full and reported as
// "internal".
func (s *ReposService) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	var re *repo.Error
	if errors.As(err, &re) {
		if re.Err != nil {
			s.log.Debug("repository operation failed", "op", op, "code", re.Code, "cause", re.Err)
		}
		// Return the classified error itself, not the (possibly wrapped) err:
		// the wrapper's text is not under our control.
		return &repo.Error{Code: re.Code, Field: re.Field, Entry: re.Entry}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return repo.New(repo.CodeCanceled)
	}
	s.log.Error("repository operation failed", "op", op, "error", err)
	return repo.New(repo.CodeInternal)
}
