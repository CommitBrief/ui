// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

// tempDirRoot is an absolute, symlink-resolved temporary directory.
func tempDirRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const fakeBinaryContent = "#!/bin/sh\necho commitbrief\n"

// archiveEntry is one archive member, in the order the test wants it written.
type archiveEntry struct {
	Name string
	Body string
	Type byte   // tar type flag; 0 means a regular file
	Link string // link target for tar.TypeSymlink / tar.TypeLink
}

func sortedEntries(m map[string]string) []archiveEntry {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]archiveEntry, 0, len(names))
	for _, n := range names {
		out = append(out, archiveEntry{Name: n, Body: m[n]})
	}
	return out
}

// tarGz writes a tar.gz with the entries in sorted-name order (deterministic).
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	return tarGzOrdered(t, sortedEntries(entries)...)
}

// tarGzOrdered writes the entries exactly in the given order.
func tarGzOrdered(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.Name, Mode: 0o755, Typeflag: typ, Linkname: e.Link}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.Body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zipArchive writes a zip with the entries in sorted-name order (deterministic).
func zipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	return zipOrdered(t, sortedEntries(entries)...)
}

// zipOrdered writes the entries exactly in the given order.
func zipOrdered(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type releaseServer struct {
	srv       *httptest.Server
	tag       string
	assets    map[string][]byte // asset name -> body
	checksums string            // body of checksums.txt; empty means derive it
}

// newReleaseServer serves a fake GitHub "latest release" API plus its assets
// over TLS.
func newReleaseServer(t *testing.T, tag, goos, goarch string, archive []byte) *releaseServer {
	t.Helper()
	rs := &releaseServer{tag: tag, assets: map[string][]byte{}}
	name := assetName(tag, goos, goarch)
	rs.assets[name] = archive
	mux := http.NewServeMux()
	rs.srv = httptest.NewTLSServer(mux)
	t.Cleanup(rs.srv.Close)

	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		sums := rs.checksums
		if sums == "" {
			var sb strings.Builder
			for n, body := range rs.assets {
				h := sha256.Sum256(body)
				sb.WriteString(hex.EncodeToString(h[:]) + "  " + n + "\n")
			}
			sums = sb.String()
		}
		rs.assets[checksumsFile] = []byte(sums)
		var rel releaseInfo
		rel.Tag = rs.tag
		for n := range rs.assets {
			rel.Assets = append(rel.Assets, releaseAsset{Name: n, URL: rs.srv.URL + "/dl/" + n})
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := rs.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	return rs
}

func (rs *releaseServer) installer(dataDir, goos, goarch string) *ManagedInstaller {
	return &ManagedInstaller{
		DataDir: dataDir,
		APIURL:  rs.srv.URL + "/latest",
		HTTP:    rs.srv.Client(),
		GOOS:    goos,
		GOARCH:  goarch,
		AllowAssetURL: func(u *url.URL) error {
			if u.Host != strings.TrimPrefix(rs.srv.URL, "https://") {
				return errors.New("unexpected host " + u.Host)
			}
			return nil
		},
	}
}

func TestManagedInstallVerifiesAndExtracts(t *testing.T) {
	data := t.TempDir()
	rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{
		"LICENSE":     "gpl",
		"README.md":   "readme",
		"commitbrief": fakeBinaryContent,
	}))
	got, err := rs.installer(data, "linux", "amd64").Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "v1.21.2" || got.Path != ManagedBinaryPath(data, "linux") {
		t.Errorf("installed = %+v", got)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil || string(b) != fakeBinaryContent {
		t.Errorf("binary content = %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(got.Path)
		if info.Mode().Perm() != 0o700 {
			t.Errorf("mode = %v, want 0700", info.Mode().Perm())
		}
		dirInfo, _ := os.Stat(filepath.Dir(got.Path))
		if dirInfo.Mode().Perm() != 0o700 {
			t.Errorf("directory mode = %v, want 0700", dirInfo.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(got.Path))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestManagedInstallWindowsZip(t *testing.T) {
	data := t.TempDir()
	rs := newReleaseServer(t, "v1.21.2", "windows", "amd64", zipArchive(t, map[string]string{
		"README.md":       "readme",
		"commitbrief.exe": fakeBinaryContent,
	}))
	got, err := rs.installer(data, "windows", "amd64").Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got.Path) != "commitbrief.exe" {
		t.Errorf("path = %s", got.Path)
	}
	if b, _ := os.ReadFile(got.Path); string(b) != fakeBinaryContent {
		t.Errorf("content = %q", b)
	}
}

func TestManagedInstallChecksumMismatchIsAHardFailure(t *testing.T) {
	data := t.TempDir()
	archive := tarGz(t, map[string]string{"commitbrief": fakeBinaryContent})
	rs := newReleaseServer(t, "v1.21.2", "linux", "arm64", archive)
	rs.checksums = strings.Repeat("0", 64) + "  " + assetName("v1.21.2", "linux", "arm64") + "\n"

	_, err := rs.installer(data, "linux", "arm64").Install(context.Background())
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if !errcode.Is(err, errcode.CLINotFound) {
		t.Errorf("code = %q, want cli_not_found", errcode.CodeOf(err))
	}
	if _, statErr := os.Stat(ManagedBinaryPath(data, "linux")); !os.IsNotExist(statErr) {
		t.Error("a binary was installed despite the checksum mismatch")
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "cli")); len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestManagedInstallNoChecksumEntry(t *testing.T) {
	data := t.TempDir()
	rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{"commitbrief": fakeBinaryContent}))
	rs.checksums = strings.Repeat("a", 64) + "  some_other_file.tar.gz\n"
	_, err := rs.installer(data, "linux", "amd64").Install(context.Background())
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
}

func TestManagedInstallRejectsUnsafeInputs(t *testing.T) {
	// The hostile entry is tried before AND after the binary: validation must not
	// stop at the entry that was asked for (CI run 42941017242 hit exactly that,
	// through map iteration order in an earlier version of this test).
	bin := archiveEntry{Name: "commitbrief", Body: fakeBinaryContent}
	hostile := []string{
		"../evil", "a/../../evil", "/abs/evil", `..\evil`, `\\server\share\evil`, `C:\evil`, "C:/evil",
		"commitbrief:stream", "dir/../../../etc/passwd",
	}
	for _, name := range hostile {
		for _, after := range []bool{false, true} {
			where := map[bool]string{false: "before", true: "after"}[after]
			t.Run(fmt.Sprintf("tar entry %q %s the binary", name, where), func(t *testing.T) {
				evil := archiveEntry{Name: name, Body: "x"}
				entries := []archiveEntry{evil, bin}
				if after {
					entries = []archiveEntry{bin, evil}
				}
				data := tempDirRoot(t)
				rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGzOrdered(t, entries...))
				_, err := rs.installer(data, "linux", "amd64").Install(context.Background())
				if err == nil || !strings.Contains(err.Error(), "escapes") {
					t.Fatalf("err = %v, want an escape refusal", err)
				}
				if _, statErr := os.Stat(ManagedBinaryPath(data, "linux")); !os.IsNotExist(statErr) {
					t.Error("a binary was installed from a hostile archive")
				}
			})
			t.Run(fmt.Sprintf("zip entry %q %s the binary", name, where), func(t *testing.T) {
				evil := archiveEntry{Name: name, Body: "x"}
				entries := []archiveEntry{evil, {Name: "commitbrief.exe", Body: fakeBinaryContent}}
				if after {
					entries = []archiveEntry{entries[1], evil}
				}
				data := tempDirRoot(t)
				rs := newReleaseServer(t, "v1.21.2", "windows", "amd64", zipOrdered(t, entries...))
				_, err := rs.installer(data, "windows", "amd64").Install(context.Background())
				if err == nil || !strings.Contains(err.Error(), "escapes") {
					t.Fatalf("err = %v, want an escape refusal", err)
				}
			})
		}
	}
	t.Run("a symlink that points outside is refused", func(t *testing.T) {
		data := tempDirRoot(t)
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGzOrdered(t, bin,
			archiveEntry{Name: "link", Type: tar.TypeSymlink, Link: "../../etc/passwd"}))
		if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err == nil || !strings.Contains(err.Error(), "links outside") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("the binary entry as a symlink is refused", func(t *testing.T) {
		data := tempDirRoot(t)
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGzOrdered(t,
			archiveEntry{Name: "commitbrief", Type: tar.TypeSymlink, Link: "somewhere-safe"}))
		if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err == nil {
			t.Error("a symlink named commitbrief was accepted")
		}
	})
	t.Run("a duplicate or case-folded binary entry is refused", func(t *testing.T) {
		for _, dup := range []string{"commitbrief", "Commitbrief", "COMMITBRIEF"} {
			data := tempDirRoot(t)
			rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGzOrdered(t, bin, archiveEntry{Name: dup, Body: "other"}))
			if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err == nil {
				t.Errorf("a second entry %q next to the binary was accepted", dup)
			}
			if _, statErr := os.Stat(ManagedBinaryPath(data, "linux")); !os.IsNotExist(statErr) {
				t.Errorf("a binary was installed despite the ambiguous entry %q", dup)
			}
		}
	})
	t.Run("harmless entries are fine, in any position", func(t *testing.T) {
		data := tempDirRoot(t)
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGzOrdered(t,
			archiveEntry{Name: "man/", Type: tar.TypeDir},
			archiveEntry{Name: "man/commitbrief.1", Body: "man"},
			bin,
			archiveEntry{Name: "CHANGELOG.md", Body: "log"},
		))
		if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err != nil {
			t.Errorf("a normal release layout was refused: %v", err)
		}
	})
	t.Run("archive without the binary", func(t *testing.T) {
		data := t.TempDir()
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{"README.md": "x"}))
		if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err == nil {
			t.Error("an archive without the binary was accepted")
		}
	})
	t.Run("no asset for this platform", func(t *testing.T) {
		data := t.TempDir()
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{"commitbrief": "x"}))
		if _, err := rs.installer(data, "plan9", "mips").Install(context.Background()); err == nil {
			t.Error("a missing asset was not reported")
		}
	})
	t.Run("default URL policy refuses a foreign host", func(t *testing.T) {
		data := t.TempDir()
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{"commitbrief": "x"}))
		in := rs.installer(data, "linux", "amd64")
		in.AllowAssetURL = nil
		if _, err := in.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "not a CommitBrief release asset") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("relative data directory", func(t *testing.T) {
		in := &ManagedInstaller{DataDir: "relative"}
		if _, err := in.Install(context.Background()); err == nil {
			t.Error("a relative data directory was accepted")
		}
	})
}

func TestManagedDefaultURLPolicy(t *testing.T) {
	m := &ManagedInstaller{}
	ok := "https://github.com/CommitBrief/commitbrief/releases/download/v1.21.2/commitbrief_1.21.2_linux_x86_64.tar.gz"
	u, _ := url.Parse(ok)
	if err := m.allow(u); err != nil {
		t.Errorf("the release asset URL was refused: %v", err)
	}
	for _, bad := range []string{
		"http://github.com/CommitBrief/commitbrief/releases/download/v1/x",
		"https://github.com/someone-else/commitbrief/releases/download/v1/x",
		"https://evil.example/CommitBrief/commitbrief/releases/download/v1/x",
		"https://github.com.evil.example/CommitBrief/commitbrief/releases/download/v1/x",
		"https://github.com/CommitBrief/commitbrief/archive/main.tar.gz",
	} {
		u, _ := url.Parse(bad)
		if err := m.allow(u); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestManagedAssetNameMatchesTheGoReleaserTemplate(t *testing.T) {
	tests := []struct{ tag, goos, goarch, want string }{
		{"v1.21.2", "darwin", "arm64", "commitbrief_1.21.2_darwin_arm64.tar.gz"},
		{"1.21.2", "linux", "amd64", "commitbrief_1.21.2_linux_x86_64.tar.gz"},
		{"v1.21.2", "linux", "386", "commitbrief_1.21.2_linux_i386.tar.gz"},
		{"v1.21.2", "windows", "amd64", "commitbrief_1.21.2_windows_x86_64.zip"},
	}
	for _, tc := range tests {
		if got := assetName(tc.tag, tc.goos, tc.goarch); got != tc.want {
			t.Errorf("assetName(%q,%q,%q) = %q, want %q", tc.tag, tc.goos, tc.goarch, got, tc.want)
		}
	}
	upper := strings.Repeat("AB", 32)
	lower := strings.Repeat("cd", 32)
	sums := parseChecksums([]byte(upper + "  a.tar.gz\n" + lower + " *b.zip\nbroken line\nshort  c.zip\n"))
	if sums["a.tar.gz"] != strings.ToLower(upper) || sums["b.zip"] != lower || len(sums) != 2 {
		t.Errorf("parseChecksums = %v", sums)
	}
}

// TestManagedEntryNameCheck is the host-independent rule itself, including the
// names no archive writer can produce (NUL).
func TestManagedEntryNameCheck(t *testing.T) {
	for _, ok := range []string{"commitbrief", "commitbrief.exe", "LICENSE", "man/commitbrief.1", "man/", "./commitbrief"} {
		if err := checkEntryName(ok); err != nil {
			t.Errorf("checkEntryName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "..", "../x", "a/../../x", "/etc/passwd", `\\srv\share`, `\evil`, `..\x`, `a\..\..\x`,
		`C:\x`, "C:/x", "c:x", "commitbrief.exe:ads", "bad\x00name", "a/..", "../",
		"a/b/../c", // stricter than needed on purpose: a release archive never has a .. segment
	} {
		if err := checkEntryName(bad); err == nil {
			t.Errorf("checkEntryName(%q) = nil, want a refusal", bad)
		}
	}
}
