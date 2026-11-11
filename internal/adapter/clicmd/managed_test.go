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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

const fakeBinaryContent = "#!/bin/sh\necho commitbrief\n"

func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
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

func zipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
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
	t.Run("archive entry that escapes", func(t *testing.T) {
		data := t.TempDir()
		rs := newReleaseServer(t, "v1.21.2", "linux", "amd64", tarGz(t, map[string]string{
			"../evil":     "x",
			"commitbrief": fakeBinaryContent,
		}))
		if _, err := rs.installer(data, "linux", "amd64").Install(context.Background()); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("err = %v, want an escape refusal", err)
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
