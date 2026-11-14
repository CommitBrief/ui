// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CommitBrief/ui/internal/domain/fspath"
	"github.com/CommitBrief/ui/internal/domain/repo"
)

// maxDialogTitleRunes bounds the title the webview may ask for.
const maxDialogTitleRunes = 100

// maxDialogFileNameRunes bounds the suggested file name the webview may ask for.
const maxDialogFileNameRunes = 100

// FolderPicker shows the operating system's folder chooser and returns the
// chosen folder, or "" when the user cancels.
type FolderPicker func(ctx context.Context, title string) (string, error)

// FileSaver shows the operating system's save dialog and returns the chosen
// file path, or "" when the user cancels.
type FileSaver func(ctx context.Context, title, fileName string) (string, error)

// RepoExporter renders the repository list as the export document
// (*app.Repos satisfies it).
type RepoExporter interface {
	ExportJSON(ctx context.Context) (string, error)
}

// DialogsService is the Wails-bound native dialog door of the webview. The
// webview has no direct access to the Wails Dialog runtime (security.go refuses
// it), so each native dialog a screen needs is one narrow method here:
//
//   - PickFolder: the folder chooser of the Repos form. It returns a path and
//     nothing else.
//   - SaveRepoExport: the Repos export. The webview never names a path and
//     never supplies the content: the user chooses the file in the native
//     dialog and this side writes the export document there. WKWebView and
//     WebKitGTK do not turn a <a download> click into a file, so the browser
//     cannot do this job.
//
// There is no method that reads a file or writes arbitrary content.
//
// Errors leave as *repo.Error (code only), like the other services.
type DialogsService struct {
	pick     FolderPicker
	save     FileSaver
	exporter RepoExporter
	log      *slog.Logger
	busy     sync.Mutex // one native dialog at a time
}

// NewDialogsService binds the native dialogs. A nil logger means slog.Default().
func NewDialogsService(pick FolderPicker, save FileSaver, exporter RepoExporter, log *slog.Logger) *DialogsService {
	if log == nil {
		log = slog.Default()
	}
	return &DialogsService{pick: pick, save: save, exporter: exporter, log: log}
}

// PickFolder opens the folder chooser with the given window title and returns
// the chosen absolute path, or "" if the user canceled (or another dialog is
// already open). The path is only a suggestion to the form: it is validated by
// the repository use cases like any other path from the webview.
func (s *DialogsService) PickFolder(ctx context.Context, title string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", repo.New(repo.CodeCanceled)
	}
	if !s.busy.TryLock() {
		return "", nil
	}
	defer s.busy.Unlock()
	path, err := s.pick(ctx, cleanDialogText(title, maxDialogTitleRunes))
	if err != nil {
		return "", s.fail("folder dialog", err)
	}
	return path, nil
}

// SaveRepoExport asks the user where to save the repository export and writes
// it there. It returns the chosen file's name (no directory), or "" if the user
// canceled (or another dialog is already open). title and fileName only decorate
// the dialog; the document is always rendered here.
func (s *DialogsService) SaveRepoExport(ctx context.Context, title, fileName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", repo.New(repo.CodeCanceled)
	}
	if !s.busy.TryLock() {
		return "", nil
	}
	defer s.busy.Unlock()
	path, err := s.save(ctx, cleanDialogText(title, maxDialogTitleRunes), cleanFileName(fileName))
	if err != nil {
		return "", s.fail("save dialog", err)
	}
	if path == "" {
		return "", nil
	}
	// The chosen path goes through the same allowlist as every other path that
	// reaches the file system: on Windows a UNC or device path would make the
	// machine authenticate to a host merely by being opened.
	if err := fspath.ValidateLocal(path); err != nil {
		if errors.Is(err, fspath.ErrNetwork) {
			return "", repo.New(repo.CodeLocalPathNetwork)
		}
		return "", repo.New(repo.CodeLocalPathInvalid)
	}
	doc, err := s.exporter.ExportJSON(ctx)
	if err != nil {
		return "", s.fail("render export", err)
	}
	path = filepath.Clean(path)
	// The native dialog does not enforce the JSON filter, so a name typed without
	// an extension would be written as typed, and the import file chooser (which
	// only offers .json files) could not pick the app's own export back up.
	if filepath.Ext(path) == "" {
		path += ".json"
	}
	// 0600 for a new file; WriteFile keeps the mode of a file it overwrites, so
	// the mode is set explicitly: the document lists local checkout paths.
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		return "", s.fail("write export", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", s.fail("restrict export file mode", err)
	}
	return filepath.Base(path), nil
}

// fail shapes an error for the webview: a canceled context becomes "canceled",
// a classified repo error passes as its code, everything else is logged in full
// (it may name paths or system messages) and reported as "internal".
func (s *DialogsService) fail(op string, err error) error {
	var re *repo.Error
	switch {
	case errors.As(err, &re):
		return &repo.Error{Code: re.Code, Field: re.Field, Entry: re.Entry}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return repo.New(repo.CodeCanceled)
	}
	s.log.Error("native dialog operation failed", "op", op, "error", err)
	return repo.New(repo.CodeInternal)
}

// cleanDialogText turns control characters into spaces, collapses whitespace
// and bounds the length of a text that came from the webview.
func cleanDialogText(text string, maxRunes int) string {
	spaced := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	out := []rune(strings.Join(strings.Fields(spaced), " "))
	if len(out) > maxRunes {
		out = out[:maxRunes]
	}
	return strings.TrimSpace(string(out))
}

// cleanFileName reduces a suggested file name to a bare name: no directory, no
// control characters.
func cleanFileName(name string) string {
	name = cleanDialogText(name, maxDialogFileNameRunes)
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// pickFolder is the production FolderPicker: the native chooser attached to the
// main window.
func (s *shell) pickFolder(a *application.App) FolderPicker {
	return func(_ context.Context, title string) (string, error) {
		d := a.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			CanCreateDirectories(false)
		if title != "" {
			d.SetTitle(title)
		}
		if w := s.window.Load(); w != nil {
			d.AttachToWindow(w)
		}
		return d.PromptForSingleSelection()
	}
}

// saveFile is the production FileSaver: the native save dialog attached to the
// main window, offering JSON files.
func (s *shell) saveFile(a *application.App) FileSaver {
	return func(_ context.Context, title, fileName string) (string, error) {
		d := a.Dialog.SaveFile().
			CanCreateDirectories(true).
			AddFilter("JSON", "*.json")
		if title != "" {
			d.SetMessage(title)
		}
		if fileName != "" {
			d.SetFilename(fileName)
		}
		if w := s.window.Load(); w != nil {
			d.AttachToWindow(w)
		}
		return d.PromptForSingleSelection()
	}
}
