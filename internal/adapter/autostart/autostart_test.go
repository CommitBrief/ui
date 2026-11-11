// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutostartLaunchAgentIsWellFormedAndStartsOnce(t *testing.T) {
	b, err := renderLaunchAgent("com.commitbrief.ui", "/Applications/CommitBrief UI.app/Contents/MacOS/commitbrief-ui")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Dict struct {
			Items []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, b)
	}
	text := string(b)
	for _, want := range []string{
		"<key>Label</key>", "<string>com.commitbrief.ui</string>",
		"<key>ProgramArguments</key>", "<string>/Applications/CommitBrief UI.app/Contents/MacOS/commitbrief-ui</string>",
		"<key>RunAtLoad</key>", "<true/>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plist lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "KeepAlive") {
		t.Error("KeepAlive would restart the application after the user chose Quit")
	}
}

func TestAutostartRenderersEscapeWhatTheirFormatReads(t *testing.T) {
	b, err := renderLaunchAgent("l", `/opt/a&b/<x>"y'/prog`)
	if err != nil {
		t.Fatal(err)
	}
	var v struct{}
	if err := xml.Unmarshal(b, &v); err != nil {
		t.Fatalf("an unusual path broke the XML: %v\n%s", err, b)
	}
	if strings.Contains(string(b), "<x>") || strings.Contains(string(b), "a&b") {
		t.Errorf("path not escaped:\n%s", b)
	}

	tests := map[string]string{
		`/opt/commitbrief-ui`:   `"/opt/commitbrief-ui"`,
		`/opt/My Apps/x y`:      `"/opt/My Apps/x y"`,
		`/opt/a"b`:              `"/opt/a\\"b"`,
		"/opt/a`b":              "\"/opt/a\\\\`b\"",
		`/opt/$HOME/x`:          `"/opt/\\$HOME/x"`,
		`/opt/50%/x`:            `"/opt/50%%/x"`,
		`/opt/back\slash`:       `"/opt/back\\\\slash"`,
		`/opt/semi;colon&amp|x`: `"/opt/semi;colon&amp|x"`,
	}
	for in, want := range tests {
		if got := desktopExec(in); got != want {
			t.Errorf("desktopExec(%q) = %s, want %s", in, got, want)
		}
	}

	d, err := renderDesktopEntry("CommitBrief UI", "/opt/My Apps/commitbrief-ui")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Desktop Entry]\n", "Type=Application\n", "Name=CommitBrief UI\n", `Exec="/opt/My Apps/commitbrief-ui"` + "\n", "X-GNOME-Autostart-enabled=true\n", "Terminal=false\n"} {
		if !strings.Contains(string(d), want) {
			t.Errorf("desktop entry lacks %q:\n%s", want, d)
		}
	}

	v1, err := windowsRunValue(`C:\Program Files\CommitBrief UI\commitbrief-ui.exe`)
	if err != nil || v1 != `"C:\Program Files\CommitBrief UI\commitbrief-ui.exe"` {
		t.Errorf("windowsRunValue = %q, %v", v1, err)
	}
}

// The program path goes into a file the login manager executes: only an absolute
// local path is accepted, by the same allowlist as every stored path.
func TestAutostartRefusesAnExecutableThatIsNotALocalPath(t *testing.T) {
	for _, exe := range []string{"", "relative/prog", "prog", "/opt/a\x00b", "/opt/a\nb", "/opt/a\x1b[0m"} {
		if _, err := renderLaunchAgent("l", exe); !errors.Is(err, errBadExecutable) {
			t.Errorf("plist for %q: %v", exe, err)
		}
		if _, err := renderDesktopEntry("n", exe); !errors.Is(err, errBadExecutable) {
			t.Errorf("desktop entry for %q: %v", exe, err)
		}
	}
	for _, exe := range []string{
		`\\evil-host\share\prog.exe`, `\\?\C:\prog.exe`, `\\.\pipe\x`, `//host/share/prog.exe`, `prog.exe`, `C:prog.exe`, `\prog.exe`,
		`C:\a"b\prog.exe`, `C:\prog.exe:stream`, `C:\CON`, "C:\\a\nb.exe",
	} {
		if _, err := windowsRunValue(exe); !errors.Is(err, errBadExecutable) {
			t.Errorf("Run value for %q: %v", exe, err)
		}
	}
}

func TestAutostartFileItemLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "autostart", "commitbrief-ui.desktop")
	exe := filepath.Join(dir, "bin", "commitbrief-ui")
	item := fileItem{path: target, render: func() ([]byte, error) { return []byte("exe=" + exe), nil }}

	if on, err := item.Enabled(ctx); err != nil || on {
		t.Fatalf("a fresh machine has the item: %v, %v", on, err)
	}
	if err := item.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if on, err := item.Enabled(ctx); err != nil || !on {
		t.Fatalf("after Enable: %v, %v", on, err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "exe="+exe {
		t.Errorf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(target)
		if info.Mode().Perm() != 0o644 {
			t.Errorf("mode = %v, want 0644 (launchd refuses a group-writable plist)", info.Mode().Perm())
		}
	}

	// Enabling again rewrites it for the program as it is now.
	item.render = func() ([]byte, error) { return []byte("moved"), nil }
	if err := item.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "moved" {
		t.Errorf("not rewritten: %q", got)
	}
	// No temporary files are left beside it.
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the item", len(entries))
	}

	if err := item.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if on, _ := item.Enabled(ctx); on {
		t.Error("still enabled after Disable")
	}
	if err := item.Disable(ctx); err != nil {
		t.Errorf("disabling a missing item: %v", err)
	}
}

func TestAutostartFileItemFailureShapes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	bad := errors.New("no executable")
	item := fileItem{path: filepath.Join(dir, "x", "a.desktop"), render: func() ([]byte, error) { return nil, bad }}
	if err := item.Enable(ctx); !errors.Is(err, bad) {
		t.Errorf("render failure = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("a directory was created for an item that could not be rendered")
	}

	// A relative target (an empty home directory) is never written.
	rel := fileItem{path: filepath.Join("Library", "LaunchAgents", "a.plist"), render: func() ([]byte, error) { return []byte("x"), nil }}
	if err := rel.Enable(ctx); err == nil {
		t.Error("a relative path was written")
	}
	if _, err := rel.Enabled(ctx); err == nil {
		t.Error("a relative path was read")
	}
	if err := rel.Disable(ctx); err == nil {
		t.Error("a relative path was removed")
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	ok := fileItem{path: filepath.Join(dir, "a"), render: func() ([]byte, error) { return []byte("x"), nil }}
	if err := ok.Enable(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

// In an AppImage os.Executable is a path under a temporary mount that is gone
// when the program exits; the login item must run the AppImage file itself.
func TestAutostartLinuxRunsTheAppImageNotItsTemporaryMount(t *testing.T) {
	mount := "/tmp/.mount_CommitXYZ/usr/bin/commitbrief-ui"
	cfg := Config{
		Executable: func() (string, error) { return mount, nil },
		AppImage:   func() string { return "/home/me/Apps/CommitBrief-UI.AppImage" },
	}
	exe, err := cfg.linuxExecutable()
	if err != nil || exe != "/home/me/Apps/CommitBrief-UI.AppImage" {
		t.Fatalf("exe = %q, %v", exe, err)
	}
	entry, err := renderDesktopEntry(DisplayName, exe)
	if err != nil || !strings.Contains(string(entry), `Exec="/home/me/Apps/CommitBrief-UI.AppImage"`) || strings.Contains(string(entry), ".mount_") {
		t.Fatalf("entry = %q, %v", entry, err)
	}

	// Not an AppImage: the running program, as before.
	cfg.AppImage = func() string { return "" }
	if exe, err := cfg.linuxExecutable(); err != nil || exe != mount {
		t.Errorf("outside an AppImage: %q, %v", exe, err)
	}

	// A malformed $APPIMAGE is refused by the same path checks; it never falls
	// back to the mount path.
	for _, bad := range []string{"relative/App.AppImage", "/home/me/App\n.AppImage"} {
		cfg.AppImage = func() string { return bad }
		exe, err := cfg.linuxExecutable()
		if err != nil {
			t.Fatalf("%q: %v", bad, err)
		}
		if _, err := renderDesktopEntry(DisplayName, exe); !errors.Is(err, errBadExecutable) {
			t.Errorf("$APPIMAGE %q: err = %v, want errBadExecutable", bad, err)
		}
	}
}
