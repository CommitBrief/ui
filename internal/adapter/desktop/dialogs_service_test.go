// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/repo"
)

type stubExporter struct {
	doc string
	err error
}

func (e stubExporter) ExportJSON(context.Context) (string, error) { return e.doc, e.err }

func pickerOnly(pick FolderPicker) *DialogsService {
	return NewDialogsService(pick, nil, stubExporter{}, nil)
}

func saverOnly(save FileSaver, exp RepoExporter) *DialogsService {
	return NewDialogsService(nil, save, exp, nil)
}

func isRepoCode(err error, code repo.Code) bool {
	var re *repo.Error
	return errors.As(err, &re) && re.Code == code
}

func TestPickFolderReturnsTheChosenPathAndCleansTheTitle(t *testing.T) {
	var gotTitle string
	s := pickerOnly(func(_ context.Context, title string) (string, error) {
		gotTitle = title
		return "/work/ui", nil
	})
	path, err := s.PickFolder(context.Background(), "  Choose\x00 a\nfolder  ")
	if err != nil || path != "/work/ui" {
		t.Fatalf("PickFolder = %q, %v", path, err)
	}
	if gotTitle != "Choose a folder" {
		t.Errorf("title = %q", gotTitle)
	}
}

func TestCleanDialogTextBoundsTheLength(t *testing.T) {
	got := cleanDialogText(strings.Repeat("é", 500), maxDialogTitleRunes)
	if n := len([]rune(got)); n != maxDialogTitleRunes {
		t.Errorf("text has %d runes, want %d", n, maxDialogTitleRunes)
	}
}

func TestCleanFileNameIsABareName(t *testing.T) {
	for in, want := range map[string]string{
		"repos.json":        "repos.json",
		"../../etc/passwd":  ".._.._etc_passwd",
		`C:\Users\x\r.json`: "C__Users_x_r.json",
		"a\nb.json":         "a b.json",
		"..":                "",
		"   ":               "",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickFolderCancelIsAnEmptyPath(t *testing.T) {
	s := pickerOnly(func(context.Context, string) (string, error) { return "", nil })
	if path, err := s.PickFolder(context.Background(), "x"); path != "" || err != nil {
		t.Errorf("canceled dialog = %q, %v", path, err)
	}
}

// A failure leaves as the code only: the cause (which may name a path or a
// system message) stays in the log.
func TestPickFolderErrorsLeakNothing(t *testing.T) {
	s := pickerOnly(func(context.Context, string) (string, error) {
		return "", errors.New("com error at /Users/someone/secret")
	})
	_, err := s.PickFolder(context.Background(), "x")
	if !isRepoCode(err, repo.CodeInternal) {
		t.Fatalf("error = %v, want internal", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error text leaks the cause: %q", err.Error())
	}
}

func TestPickFolderCanceledContext(t *testing.T) {
	called := false
	s := pickerOnly(func(context.Context, string) (string, error) { called = true; return "", nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.PickFolder(ctx, "x")
	if !isRepoCode(err, repo.CodeCanceled) || called {
		t.Errorf("canceled ctx: err = %v, picker called = %v", err, called)
	}
}

// Only one native dialog at a time: a second call while one is open returns
// the empty path instead of stacking a second dialog.
func TestPickFolderOneDialogAtATime(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	saved := false
	s := NewDialogsService(func(context.Context, string) (string, error) {
		close(entered)
		<-release
		return "/a", nil
	}, func(context.Context, string, string) (string, error) { saved = true; return "", nil }, stubExporter{}, nil)
	done := make(chan string)
	go func() {
		p, _ := s.PickFolder(context.Background(), "x")
		done <- p
	}()
	<-entered
	if p, err := s.PickFolder(context.Background(), "x"); p != "" || err != nil {
		t.Errorf("second dialog = %q, %v", p, err)
	}
	if p, err := s.SaveRepoExport(context.Background(), "x", "y"); p != "" || err != nil || saved {
		t.Errorf("save dialog while another is open = %q, %v (shown: %v)", p, err, saved)
	}
	close(release)
	if p := <-done; p != "/a" {
		t.Errorf("first dialog = %q", p)
	}
}

func TestSaveRepoExportWritesTheDocumentToTheChosenFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "repos.json")
	var gotTitle, gotName string
	s := saverOnly(func(_ context.Context, title, name string) (string, error) {
		gotTitle, gotName = title, name
		return target, nil
	}, stubExporter{doc: `{"schema":"x"}` + "\n"})
	name, err := s.SaveRepoExport(context.Background(), "Save\x00 it", "../evil.json")
	if err != nil || name != "repos.json" {
		t.Fatalf("SaveRepoExport = %q, %v", name, err)
	}
	if gotTitle != "Save it" || gotName != ".._evil.json" {
		t.Errorf("dialog got title %q, file name %q", gotTitle, gotName)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "{\"schema\":\"x\"}\n" {
		t.Fatalf("file = %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestSaveRepoExportCancelWritesNothing(t *testing.T) {
	called := false
	s := saverOnly(func(context.Context, string, string) (string, error) { return "", nil },
		stubExporterFunc(func() (string, error) { called = true; return "x", nil }))
	if name, err := s.SaveRepoExport(context.Background(), "t", "f"); name != "" || err != nil || called {
		t.Errorf("canceled save = %q, %v, exported = %v", name, err, called)
	}
}

type stubExporterFunc func() (string, error)

func (f stubExporterFunc) ExportJSON(context.Context) (string, error) { return f() }

func TestSaveRepoExportRefusesNetworkPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip(`the UNC allowlist rule only applies to Windows paths`)
	}
	s := saverOnly(func(context.Context, string, string) (string, error) { return `\\host\share\r.json`, nil }, stubExporter{doc: "x"})
	if _, err := s.SaveRepoExport(context.Background(), "t", "f"); !isRepoCode(err, repo.CodeLocalPathNetwork) {
		t.Errorf("UNC path: err = %v", err)
	}
}

func TestSaveRepoExportRefusesARelativePath(t *testing.T) {
	s := saverOnly(func(context.Context, string, string) (string, error) { return "relative/r.json", nil }, stubExporter{doc: "x"})
	if _, err := s.SaveRepoExport(context.Background(), "t", "f"); !isRepoCode(err, repo.CodeLocalPathInvalid) {
		t.Errorf("relative path: err = %v", err)
	}
}

// A failed write leaves as "internal": the cause names the path.
func TestSaveRepoExportErrorsLeakNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "secret-name.json")
	s := saverOnly(func(context.Context, string, string) (string, error) { return missing, nil }, stubExporter{doc: "x"})
	_, err := s.SaveRepoExport(context.Background(), "t", "f")
	if !isRepoCode(err, repo.CodeInternal) {
		t.Fatalf("write failure: err = %v, want internal", err)
	}
	if strings.Contains(err.Error(), "secret-name") {
		t.Errorf("error text leaks the path: %q", err.Error())
	}
	s = saverOnly(func(context.Context, string, string) (string, error) { return "", errors.New("/Users/x/secret") }, stubExporter{})
	if _, err = s.SaveRepoExport(context.Background(), "t", "f"); !isRepoCode(err, repo.CodeInternal) || strings.Contains(err.Error(), "secret") {
		t.Errorf("dialog failure: err = %v", err)
	}
	s = saverOnly(func(context.Context, string, string) (string, error) {
		return filepath.Join(t.TempDir(), "r.json"), nil
	}, stubExporter{err: errors.New("db at /Users/x/secret")})
	if _, err = s.SaveRepoExport(context.Background(), "t", "f"); !isRepoCode(err, repo.CodeInternal) || strings.Contains(err.Error(), "secret") {
		t.Errorf("export failure: err = %v", err)
	}
}

// The save dialog does not enforce the JSON filter: a name typed without an
// extension gets ".json", so the import chooser (which offers .json files only)
// can pick the export up again. A name that has an extension is kept.
func TestSaveRepoExportAddsTheJSONExtension(t *testing.T) {
	dir := t.TempDir()
	for typed, want := range map[string]string{"repos": "repos.json", "repos.json": "repos.json", "repos.backup": "repos.backup"} {
		s := saverOnly(func(context.Context, string, string) (string, error) { return filepath.Join(dir, typed), nil }, stubExporter{doc: "x"})
		name, err := s.SaveRepoExport(context.Background(), "t", "f")
		if err != nil || name != want {
			t.Errorf("typed %q: name = %q, %v; want %q", typed, name, err, want)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("typed %q: %v", typed, err)
		}
	}
}

// Overwriting an older, more open file tightens it: the document lists local
// checkout paths.
func TestSaveRepoExportTightensTheModeOfAnOverwrittenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	target := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := saverOnly(func(context.Context, string, string) (string, error) { return target, nil }, stubExporter{doc: "new"})
	if _, err := s.SaveRepoExport(context.Background(), "t", "f"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
		t.Errorf("mode after overwrite = %v, want 0600", info.Mode().Perm())
	}
}
