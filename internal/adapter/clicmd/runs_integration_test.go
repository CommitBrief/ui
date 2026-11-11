// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package clicmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/app/jobkinds"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/review"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// TestIntegrationManualLocalReview runs a real local review through the whole
// manual-jobs stack: the installed `commitbrief` binary, a throwaway git
// repository with a staged change, a SQLite store and the run service. The
// model is a stub Ollama server on localhost that answers with a fixed findings
// document, so the run is deterministic, free and nothing leaves the machine
// (the installed gemma4:12b answers this CLI's prompt with an empty reply, so a
// real local model cannot make this test pass). Like the other integration
// tests it never touches the user's CLI configuration (isolatedEngine).
func TestIntegrationManualLocalReview(t *testing.T) {
	ctx := context.Background()
	const answer = `{"findings":[{"severity":"high","file":"user.go","line":8,"title":"SQL injection in Find",` +
		`"description":"name is concatenated into the query.","suggestion":"Use a placeholder and pass name as an argument."},` +
		`{"severity":"medium","file":"user.go","line":10,"title":"Scan target is a nil pointer",` +
		`"description":"u is nil when Scan writes u.Name.","suggestion":"Allocate the User first."}]}`
	var chatCalls atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"stub-model"}]}`)
		case "/api/chat":
			chatCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "stub-model", "message": map[string]string{"role": "assistant", "content": answer},
				"done": true, "prompt_eval_count": 1200, "eval_count": 150,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer stub.Close()

	eng, home := isolatedEngine(t)
	for k, v := range map[string]string{
		"provider": "ollama", "providers.ollama.model": "stub-model", "providers.ollama.base_url": stub.URL, "cache.enabled": "false",
	} {
		if err := eng.SetConfig(ctx, k, v, port.UserScope()); err != nil {
			t.Fatalf("SetConfig(%s): %v", k, err)
		}
	}

	// A repository with one committed file and one staged change that has an
	// obvious defect.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=it", "-c", "user.email=it@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "user.go"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("package user\n\nfunc Name(u *User) string {\n\treturn u.Name\n}\n\ntype User struct{ Name string }\n")
	git("add", "user.go")
	git("commit", "-q", "-m", "initial")
	write("package user\n\nimport \"database/sql\"\n\ntype User struct{ Name string }\n\nfunc Find(db *sql.DB, name string) (*User, error) {\n" +
		"\trows, _ := db.Query(\"SELECT name FROM users WHERE name = '\" + name + \"'\")\n\tvar u *User\n\trows.Scan(&u.Name)\n\treturn u, nil\n}\n")
	git("add", "user.go")

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "CommitBrief UI", "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	r, err := store.Repos().Create(ctx, repo.Repository{Owner: "Acme", Name: "Users", LocalPath: dir, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds, err := app.NewKindRegistry(jobkinds.NewReviewLocal(store.Repos(), eng), jobkinds.NewReviewPR(store.Repos(), eng))
	if err != nil {
		t.Fatal(err)
	}
	runs := app.NewRuns(app.RunsDeps{
		Kinds: kinds, Jobs: store.Jobs(), Runs: store.Runs(), Tx: store, Settings: store.Settings(),
		LogDir: t.TempDir(), Env: testEnv{vars: map[string]string{}, home: home},
	})
	if err := runs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_ = runs.Close(c)
	}()

	started, err := runs.StartLocalReview(ctx, app.LocalReviewInput{RepositoryID: r.ID, Mode: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Minute)
	var got run.Run
	for time.Now().Before(deadline) {
		if got, err = runs.GetRun(ctx, started.ID); err != nil {
			t.Fatal(err)
		}
		if got.Status.Terminal() {
			break
		}
		time.Sleep(time.Second)
	}
	if got.Status != run.StatusSucceeded {
		logText, _ := runs.TaskLog(ctx, got.Tasks[0].ID)
		t.Fatalf("run ended %s (%s); task %+v\nlog:\n%s", got.Status, got.ErrorCode, got.Tasks[0], logText.Text)
	}

	task := got.Tasks[0]
	var findings []review.Finding
	if err := json.Unmarshal(task.Findings, &findings); err != nil {
		t.Fatalf("stored findings are not a findings array: %v\n%s", err, task.Findings)
	}
	var report review.Report
	if err := json.Unmarshal(task.Report, &report); err != nil || report.Meta.Provider != "ollama" || report.Meta.Model != "stub-model" {
		t.Errorf("stored report: provider %q, %v", report.Meta.Provider, err)
	}
	if chatCalls.Load() == 0 {
		t.Error("the CLI never called the stub model")
	}
	if len(findings) != 2 || findings[0].Severity != review.SeverityHigh || findings[0].File != "user.go" {
		t.Errorf("stored findings = %+v, want the stub's two", findings)
	}
	if task.CLIVersion == "" || task.ExitCode == nil || *task.ExitCode != 0 || !task.HasLog {
		t.Errorf("stored task = %+v", task)
	}
	t.Logf("review.local succeeded: %d findings stored, cost $%.4f, CLI %s, model %s", len(findings), task.CostUSD, task.CLIVersion, report.Meta.Model)
	for _, f := range findings {
		t.Logf("  [%s] %s:%d %s", f.Severity, f.File, f.Line, f.Title)
	}
}
