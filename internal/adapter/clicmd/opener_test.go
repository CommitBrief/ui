// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

func TestOpenCommandArgv(t *testing.T) {
	cases := []struct {
		goos, path string
		name       string
		args       []string
	}{
		{"darwin", "/Users/a/.commitbrief/config.yml", "open", []string{"-t", "/Users/a/.commitbrief/config.yml"}},
		{"linux", "/home/a/.commitbrief/config.yml", "xdg-open", []string{"/home/a/.commitbrief/config.yml"}},
		{"windows", `C:\Users\a\.commitbrief\config.yml`, "rundll32", []string{"url.dll,FileProtocolHandler", `C:\Users\a\.commitbrief\config.yml`}},
		// A path with spaces and shell metacharacters stays one argument.
		{"linux", "/home/a b/$(touch x);y/config.yml", "xdg-open", []string{"/home/a b/$(touch x);y/config.yml"}},
	}
	for _, tc := range cases {
		name, args := openCommand(tc.goos, tc.path)
		if name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s: = %s %q, want %s %q", tc.goos, name, args, tc.name, tc.args)
		}
	}
}

type openRecorder struct {
	calls [][]string
	err   error
}

func (r *openRecorder) start(name string, args []string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.err
}

func fileOpener(goos string, rec *openRecorder) *Opener {
	return &Opener{goos: goos, start: rec.start, stat: os.Stat}
}

func TestOpenerRunsTheOpenerOnAnExistingFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(file, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &openRecorder{}
	// The running OS: the path rules are those of the host, whatever it is.
	if err := fileOpener(runtime.GOOS, rec).Open(context.Background(), file); err != nil {
		t.Fatalf("Open() = %v", err)
	}
	name, args := openCommand(runtime.GOOS, file)
	if want := [][]string{append([]string{name}, args...)}; !reflect.DeepEqual(rec.calls, want) {
		t.Errorf("calls = %q, want %q", rec.calls, want)
	}
	if len(rec.calls) != 1 || rec.calls[0][len(rec.calls[0])-1] != file {
		t.Errorf("the file must be the last, single path argument: %q", rec.calls)
	}
}

func TestOpenerMissingFileOrDirectoryIsErrFileMissingAndStartsNothing(t *testing.T) {
	dir := t.TempDir()
	for name, path := range map[string]string{"missing": filepath.Join(dir, "nope.yml"), "directory": dir} {
		rec := &openRecorder{}
		err := fileOpener(runtime.GOOS, rec).Open(context.Background(), path)
		if !errors.Is(err, port.ErrFileMissing) {
			t.Errorf("%s: err = %v, want ErrFileMissing", name, err)
		}
		if len(rec.calls) != 0 {
			t.Errorf("%s: a process was started: %q", name, rec.calls)
		}
	}
}

func TestOpenerRefusesPathsThatAreNotAbsoluteLocalOnes(t *testing.T) {
	for _, path := range []string{`\\host\share\config.yml`, `\\?\C:\x`, `relative\config.yml`, ""} {
		rec := &openRecorder{}
		statted := false
		o := &Opener{goos: "windows", start: rec.start, stat: func(string) (os.FileInfo, error) {
			statted = true
			return nil, os.ErrNotExist
		}}
		if err := o.Open(context.Background(), path); err == nil || errors.Is(err, port.ErrFileMissing) {
			t.Errorf("Open(%q) = %v, want a refusal", path, err)
		}
		if statted || len(rec.calls) != 0 {
			t.Errorf("Open(%q) touched the file system or started a process", path)
		}
	}
}

func TestOpenerReportsAFailedStart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("exit status 1")
	rec := &openRecorder{err: boom}
	err := fileOpener(runtime.GOOS, rec).Open(context.Background(), file)
	if !errors.Is(err, boom) || errors.Is(err, port.ErrFileMissing) {
		t.Errorf("err = %v, want the opener's error", err)
	}
}

// A canceled call starts nothing.
func TestOpenerDoesNotStartForACanceledCall(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &openRecorder{}
	if err := fileOpener(runtime.GOOS, rec).Open(ctx, file); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("a process was started: %q", rec.calls)
	}
}

// The start seam is what keeps an opener that stays in the foreground from
// holding the UI: a command that runs for a long time must not block Open.
func TestStartCommandDoesNotWaitForTheProcess(t *testing.T) {
	name, args := "sleep", []string{"2"}
	if runtime.GOOS == "windows" {
		name, args = "ping", []string{"-n", "3", "127.0.0.1"}
	}
	begin := time.Now()
	if err := startCommand(name, args); err != nil {
		t.Skipf("cannot start %s here: %v", name, err)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Errorf("startCommand took %v: it waited for the process", elapsed)
	}
	if err := startCommand("this-command-does-not-exist-cb", nil); err == nil {
		t.Error("a command that cannot start must be an error")
	}
}
