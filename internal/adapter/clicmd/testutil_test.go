// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

// fakeBin is the stand-in commitbrief binary, built once for the test run.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "clicmd-fakecli-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		os.Exit(2)
	}
	name := "fakecli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	fakeBin = filepath.Join(dir, name)

	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the go tool is needed on PATH to build the fake CLI:", err)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	build := exec.Command(goBin, "build", "-o", fakeBin, "./testdata/fakecli")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakecli: %v\n%s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testEnv is a port.ProcessEnv: the real environment overlaid with fixed values.
type testEnv struct {
	vars map[string]string
	home string
}

func (e testEnv) Environ() []string {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range e.vars {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func (e testEnv) LookupEnv(key string) (string, bool) {
	if v, ok := e.vars[key]; ok {
		return v, true
	}
	return "", false
}

func (e testEnv) HomeDir() (string, error) { return e.home, nil }

// harness bundles an engine with the files its fake CLI reads and writes.
type harness struct {
	t       *testing.T
	engine  *Engine
	vars    map[string]string
	dataDir string
	binDir  string
	argvLog string
}

type harnessOpt func(*Config)

// newHarness builds an Engine that drives fakeBin. The PATH it hands out holds
// a fake `gh` so PR calls pass their preflight; withGH=false removes it.
func newHarness(t *testing.T, withGH bool, opts ...harnessOpt) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{
		t:       t,
		vars:    map[string]string{},
		dataDir: filepath.Join(root, "data"),
		binDir:  filepath.Join(root, "bin"),
		argvLog: filepath.Join(root, "argv.json"),
	}
	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withGH {
		name := "gh"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(h.binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.vars["PATH"] = h.binDir
	h.vars["FAKECLI_ARGV_FILE"] = h.argvLog

	cfg := Config{
		Env: testEnv{vars: h.vars, home: root},
		// A fixed Probe keeps tests from starting the developer's login shell.
		Probe:        func(_ context.Context) (string, error) { return "", nil },
		KnownDirs:    func() []string { return nil },
		DataDir:      h.dataDir,
		OverridePath: func() string { return fakeBin },
		KillGrace:    300 * time.Millisecond,
	}
	for _, o := range opts {
		o(&cfg)
	}
	h.engine = New(cfg)
	return h
}

// set sets FAKECLI_* variables for the next calls.
func (h *harness) set(kv ...string) {
	h.t.Helper()
	for i := 0; i+1 < len(kv); i += 2 {
		h.vars[kv[i]] = kv[i+1]
	}
}

// stdoutFile points the fake CLI's stdout at the content of a testdata file.
func (h *harness) stdoutFromTestdata(name string) {
	h.t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		h.t.Fatal(err)
	}
	h.set("FAKECLI_STDOUT_FILE", p)
}

// stdoutLiteral writes content to a temp file and points the fake CLI at it.
func (h *harness) stdoutLiteral(content string) {
	h.t.Helper()
	p := filepath.Join(h.t.TempDir(), "stdout.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.set("FAKECLI_STDOUT_FILE", p)
}

// argvRecord is what fakecli wrote about its last non-version call.
type argvRecord struct {
	Args       []string          `json:"args"`
	Cwd        string            `json:"cwd"`
	StdinBytes int               `json:"stdin_bytes"`
	Stdin      string            `json:"stdin"`
	Env        map[string]string `json:"env"`
}

func (h *harness) lastCall() argvRecord {
	h.t.Helper()
	b, err := os.ReadFile(h.argvLog)
	if err != nil {
		h.t.Fatalf("fakecli recorded nothing: %v", err)
	}
	var rec argvRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		h.t.Fatalf("argv record: %v", err)
	}
	return rec
}

// repoDir makes a directory that looks like a git repository root.
func repoDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// indexOf returns the position of want in list, or -1.
func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

// localStaged is a minimal valid local review request.
func localStaged(t *testing.T) port.LocalReviewRequest {
	t.Helper()
	return port.LocalReviewRequest{RepoPath: repoDir(t), Mode: port.LocalStaged}
}
