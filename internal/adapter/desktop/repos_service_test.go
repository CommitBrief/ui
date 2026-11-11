// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/adapter/gitinfo"
	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/repo"
)

// newRealService wires the service to a real SQLite database and the real git
// inspector, the way the composition root does.
func newRealService(t *testing.T, dir string) *ReposService {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(dir, "state", "ui.db"), sqlite.WithLogger(testLogger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewReposService(app.NewRepos(store.Repos(), store, gitinfo.New()), testLogger)
}

func newCheckout(t *testing.T, originURL string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + originURL + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReposServiceEndToEnd(t *testing.T) {
	ctx := context.Background()
	s := newRealService(t, t.TempDir())
	checkout := newCheckout(t, "git@github.com:CommitBrief/ui.git")

	sug, err := s.SuggestFromLocalPath(ctx, checkout)
	if err != nil || sug.Owner != "CommitBrief" || sug.Name != "ui" || sug.AlreadyAdded {
		t.Fatalf("SuggestFromLocalPath = %+v, %v", sug, err)
	}
	added, err := s.Add(ctx, app.RepoInput{Owner: sug.Owner, Name: sug.Name, LocalPath: checkout})
	if err != nil || added.ID == 0 || !added.Enabled {
		t.Fatalf("Add = %+v, %v", added, err)
	}
	if sug, _ := s.SuggestFromLocalPath(ctx, checkout); !sug.AlreadyAdded {
		t.Error("AlreadyAdded must be true once the repository is in the list")
	}
	if _, err := s.Add(ctx, app.RepoInput{Owner: "commitbrief", Name: "UI"}); !isCode(err, repo.CodeDuplicate) {
		t.Errorf("duplicate Add = %v", err)
	}
	if _, err := s.SetEnabled(ctx, added.ID, false); err != nil {
		t.Fatal(err)
	}

	// Export, then import into a brand-new database: same list.
	doc, err := s.ExportJSON(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := newRealService(t, t.TempDir())
	res, err := other.ImportJSON(ctx, doc)
	if err != nil || res.Created != 1 || res.PathsDropped != 0 {
		t.Fatalf("ImportJSON = %+v, %v", res, err)
	}
	want, _ := s.List(ctx)
	got, _ := other.List(ctx)
	if len(got) != 1 || got[0].Owner != want[0].Owner || got[0].Name != want[0].Name ||
		got[0].LocalPath != want[0].LocalPath || got[0].Enabled != want[0].Enabled {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}

	if err := s.Delete(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("List after Delete = %+v", list)
	}
}

func isCode(err error, code repo.Code) bool {
	var e *repo.Error
	return errors.As(err, &e) && e.Code == code
}

// What reaches the webview must be only the code, field and entry: no paths,
// no driver text. Wails sends err.Error() as the message and JSON of the
// error as the cause.
func TestReposServiceErrorsLeakNothing(t *testing.T) {
	ctx := context.Background()
	s := newRealService(t, t.TempDir())
	secretDir := filepath.Join(t.TempDir(), "very-secret-dir")

	cases := map[string]error{}
	_, cases["missing path"] = s.Add(ctx, app.RepoInput{Owner: "a", Name: "b", LocalPath: secretDir})
	_, cases["bad owner"] = s.Add(ctx, app.RepoInput{Owner: "", Name: "b"})
	_, cases["not found"] = s.Get(ctx, 12345)
	_, cases["bad import"] = s.ImportJSON(ctx, `{"schema":"commitbrief-ui.repositories","version":1,"repositories":[{"owner":"x y","name":"z"}]}`)

	for name, err := range cases {
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		raw, jerr := json.Marshal(&err)
		if jerr != nil {
			t.Fatal(jerr)
		}
		for _, text := range []string{err.Error(), string(raw)} {
			if strings.Contains(text, "secret") || strings.Contains(text, string(filepath.Separator)) {
				t.Errorf("%s: error text leaks detail: %q", name, text)
			}
		}
		var obj struct {
			Code  string `json:"code"`
			Field string `json:"field"`
			Entry int    `json:"entry"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil || obj.Code == "" {
			t.Errorf("%s: error does not marshal to {code,...}: %s", name, raw)
		}
	}
	var imp struct{ Entry int }
	badImport := cases["bad import"]
	raw, _ := json.Marshal(&badImport)
	if err := json.Unmarshal(raw, &imp); err != nil || imp.Entry != 1 {
		t.Errorf("bad import entry = %d (%s)", imp.Entry, raw)
	}

	// Unclassified failures become "internal"; cancellation becomes "canceled".
	if err := s.fail("op", errors.New("open /Users/me/secret.db: no such file")); !isCode(err, repo.CodeInternal) || strings.Contains(err.Error(), "secret") {
		t.Errorf("unclassified error = %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.List(cctx); !isCode(err, repo.CodeCanceled) {
		t.Errorf("canceled List = %v", err)
	}
}
