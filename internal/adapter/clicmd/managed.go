// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

// The managed copy (UI-0003 section 2): when no CLI is installed, the UI
// downloads the release archive for this platform from GitHub Releases into its
// own data directory, verifies it against the release's checksums.txt (SHA-256)
// and extracts the single binary. The copy lives in a writable directory, so
// `commitbrief upgrade` updates it like any manual install.
//
// Trust model: TLS to github.com plus SHA-256 from checksums.txt. The CLI does
// not sign checksums.txt (CLI ADR-0034), so this detects a corrupt or truncated
// download, not a compromised release. That is weaker than the UI's own update
// chain (UI-0007) and is recorded as a negative consequence in UI-0003.

const (
	defaultReleaseAPI     = "https://api.github.com/repos/CommitBrief/commitbrief/releases/latest"
	releaseDownloadPrefix = "/CommitBrief/commitbrief/releases/download/"
	checksumsFile         = "checksums.txt"

	maxArchiveBytes   = 200 << 20
	maxMetadataBytes  = 4 << 20
	maxChecksumsBytes = 1 << 20
)

// ErrChecksumMismatch is a hard failure: the downloaded archive does not match
// the release's checksums.txt, or has no entry there.
var ErrChecksumMismatch = errors.New("downloaded archive does not match checksums.txt")

// ManagedBinaryPath is where the managed copy of the CLI lives.
func ManagedBinaryPath(dataDir, goos string) string {
	name := cliName
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir, "cli", name)
}

// InstalledCLI describes a freshly installed managed copy.
type InstalledCLI struct {
	Path string
	Tag  string
}

// ManagedInstaller downloads and installs the managed CLI copy.
type ManagedInstaller struct {
	// DataDir is the application data directory (platform.Dirs.Data).
	DataDir string
	// APIURL is the GitHub "latest release" endpoint; empty means the CLI's
	// repository. Tests point it at an httptest server.
	APIURL string
	// HTTP is the client to use; nil builds one with sane transport timeouts.
	HTTP *http.Client
	// GOOS and GOARCH select the archive; empty means the running platform.
	GOOS, GOARCH string
	// AllowAssetURL vets the download URLs named by the release metadata; nil
	// accepts only https://github.com/CommitBrief/commitbrief/releases/download/….
	AllowAssetURL func(*url.URL) error
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type releaseInfo struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
}

func (r releaseInfo) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// assetName mirrors the archive name_template in the CLI's .goreleaser.yaml:
// commitbrief_<version>_<os>_<arch>.tar.gz (.zip on Windows), amd64 spelled
// x86_64 and 386 spelled i386.
func assetName(tag, goos, goarch string) string {
	v := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	arch := goarch
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "386":
		arch = "i386"
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("commitbrief_%s_%s_%s%s", v, goos, arch, ext)
}

var sha256HexRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseChecksums reads "<sha256>  <file>" lines into file -> lower-case sum.
func parseChecksums(data []byte) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !sha256HexRe.MatchString(f[0]) {
			continue
		}
		// A leading '*' on the file name marks sha256sum's binary mode.
		sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return sums
}

func (m *ManagedInstaller) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Transport: tr,
		// No whole-request Timeout: it would cut off a slow but progressing
		// download. The context bounds the run instead.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect to %s", req.URL.Scheme)
			}
			return nil
		},
	}
}

func (m *ManagedInstaller) allow(u *url.URL) error {
	if m.AllowAssetURL != nil {
		return m.AllowAssetURL(u)
	}
	if u.Scheme != "https" || u.Host != "github.com" || !strings.HasPrefix(u.Path, releaseDownloadPrefix) {
		return fmt.Errorf("download URL %q is not a CommitBrief release asset", u.String())
	}
	return nil
}

func (m *ManagedInstaller) get(ctx context.Context, rawURL, accept string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "commitbrief-ui")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", redactURL(rawURL), resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("GET %s: response larger than %d bytes", redactURL(rawURL), limit)
	}
	return nil
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid url)"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// Install downloads the latest release for this platform, verifies it and
// installs the binary at ManagedBinaryPath. Every failure is classified
// cli_not_found: whatever went wrong, there is still no usable CLI.
func (m *ManagedInstaller) Install(ctx context.Context) (InstalledCLI, error) {
	out, err := m.install(ctx)
	if err != nil {
		var ce *errcode.Error
		if errors.As(err, &ce) {
			return InstalledCLI{}, err
		}
		return InstalledCLI{}, errcode.Wrap(errcode.CLINotFound, "could not install the managed commitbrief copy", err)
	}
	return out, nil
}

func (m *ManagedInstaller) install(ctx context.Context) (InstalledCLI, error) {
	goos, goarch := m.GOOS, m.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if m.DataDir == "" || !filepath.IsAbs(m.DataDir) {
		return InstalledCLI{}, errors.New("data directory must be an absolute path")
	}
	apiURL := m.APIURL
	if apiURL == "" {
		apiURL = defaultReleaseAPI
	}

	var meta strings.Builder
	if err := m.get(ctx, apiURL, "application/vnd.github+json", maxMetadataBytes, &meta); err != nil {
		return InstalledCLI{}, fmt.Errorf("fetch release metadata: %w", err)
	}
	var rel releaseInfo
	if err := json.Unmarshal([]byte(meta.String()), &rel); err != nil || rel.Tag == "" {
		return InstalledCLI{}, errors.New("release metadata is not usable")
	}

	name := assetName(rel.Tag, goos, goarch)
	archive, ok := rel.asset(name)
	if !ok {
		return InstalledCLI{}, fmt.Errorf("release %s has no asset %s", rel.Tag, name)
	}
	sums, ok := rel.asset(checksumsFile)
	if !ok {
		return InstalledCLI{}, fmt.Errorf("release %s has no %s", rel.Tag, checksumsFile)
	}
	for _, a := range []releaseAsset{archive, sums} {
		u, err := url.Parse(a.URL)
		if err != nil {
			return InstalledCLI{}, fmt.Errorf("asset %s: %w", a.Name, err)
		}
		if err := m.allow(u); err != nil {
			return InstalledCLI{}, err
		}
	}

	var sumsBuf strings.Builder
	if err := m.get(ctx, sums.URL, "", maxChecksumsBytes, &sumsBuf); err != nil {
		return InstalledCLI{}, fmt.Errorf("fetch %s: %w", checksumsFile, err)
	}
	want := parseChecksums([]byte(sumsBuf.String()))[name]
	if want == "" {
		return InstalledCLI{}, fmt.Errorf("%w: no entry for %s", ErrChecksumMismatch, name)
	}

	dir := filepath.Join(m.DataDir, "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return InstalledCLI{}, err
	}

	// Temp files sit next to the target so the final rename stays on one
	// filesystem and is atomic.
	archiveFile, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return InstalledCLI{}, err
	}
	archivePath := archiveFile.Name()
	defer func() {
		_ = archiveFile.Close()
		_ = os.Remove(archivePath)
	}()

	hasher := sha256.New()
	if err := m.get(ctx, archive.URL, "application/octet-stream", maxArchiveBytes, io.MultiWriter(archiveFile, hasher)); err != nil {
		return InstalledCLI{}, fmt.Errorf("download %s: %w", name, err)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != want {
		return InstalledCLI{}, fmt.Errorf("%w: %s", ErrChecksumMismatch, name)
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return InstalledCLI{}, err
	}

	binFile, err := os.CreateTemp(dir, ".bin-*")
	if err != nil {
		return InstalledCLI{}, err
	}
	binPath := binFile.Name()
	defer func() {
		_ = binFile.Close()
		_ = os.Remove(binPath) // harmless after a successful rename
	}()

	entry := cliName
	if goos == "windows" {
		entry += ".exe"
		info, statErr := archiveFile.Stat()
		if statErr != nil {
			return InstalledCLI{}, statErr
		}
		err = extractZipEntry(archiveFile, info.Size(), entry, binFile)
	} else {
		err = extractTarGzEntry(archiveFile, entry, binFile)
	}
	if err != nil {
		return InstalledCLI{}, err
	}
	if err := binFile.Close(); err != nil {
		return InstalledCLI{}, err
	}
	if err := os.Chmod(binPath, 0o700); err != nil {
		return InstalledCLI{}, err
	}
	target := ManagedBinaryPath(m.DataDir, goos)
	if err := os.Rename(binPath, target); err != nil {
		return InstalledCLI{}, err
	}
	return InstalledCLI{Path: target, Tag: rel.Tag}, nil
}

// checkEntryName rejects archive entry names that could escape a target
// directory on any host: absolute paths (Unix, drive-letter, UNC), ".."
// segments after cleaning (both separators), NUL bytes, and a colon (a drive
// letter, or an NTFS alternate data stream such as "commitbrief.exe:evil").
// It is host-independent on purpose: the archive is checked the same way on
// macOS, Linux and Windows, case-insensitive file systems included, because it
// never depends on how the host would resolve the name.
func checkEntryName(name string) error {
	bad := func() error { return fmt.Errorf("archive entry %q escapes the target directory", name) }
	if name == "" || strings.ContainsAny(name, "\x00:") {
		return bad()
	}
	slashed := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(name) {
		return bad()
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return bad()
		}
	}
	if clean := path.Clean(slashed); clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return bad()
	}
	return nil
}

// checkLinkTarget applies the same rule to a symlink or hardlink target. The
// installer never creates links, but an archive that carries an escaping one is
// hostile and is refused as a whole.
func checkLinkTarget(entry, target string) error {
	if err := checkEntryName(target); err != nil {
		return fmt.Errorf("archive entry %q links outside the target directory: %w", entry, err)
	}
	return nil
}

// extractTarGzEntry copies the single regular entry named want into w. The
// whole archive is always read to the end and every entry vetted, even after the
// wanted one was found: an archive with any unsafe entry is refused as a whole
// (fail closed), and a second entry that names the binary, under the same or a
// case-folded name, is refused as ambiguous.
func extractTarGzEntry(r io.Reader, want string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := checkEntryName(hdr.Name); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			if err := checkLinkTarget(hdr.Name, hdr.Linkname); err != nil {
				return err
			}
		}
		if !strings.EqualFold(hdr.Name, want) {
			continue
		}
		if hdr.Name != want || hdr.Typeflag != tar.TypeReg || found {
			// A look-alike (case-folded), a link or a duplicate of the binary:
			// on a case-insensitive file system these would collide with it.
			return fmt.Errorf("archive has an ambiguous or non-regular entry for %q", want)
		}
		found = true
		if err := copyCapped(w, tr); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("archive has no %q entry", want)
	}
	return nil
}

// extractZipEntry is extractTarGzEntry for zip archives.
func extractZipEntry(r io.ReaderAt, size int64, want string, w io.Writer) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	found := false
	for _, f := range zr.File {
		if err := checkEntryName(f.Name); err != nil {
			return err
		}
		if !strings.EqualFold(f.Name, want) {
			continue
		}
		mode := f.FileInfo().Mode()
		if f.Name != want || found || f.FileInfo().IsDir() || mode&os.ModeSymlink != 0 {
			return fmt.Errorf("archive has an ambiguous or non-regular entry for %q", want)
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = copyCapped(w, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("archive has no %q entry", want)
	}
	return nil
}

func copyCapped(w io.Writer, r io.Reader) error {
	n, err := io.Copy(w, io.LimitReader(r, maxArchiveBytes+1))
	if err != nil {
		return err
	}
	if n > maxArchiveBytes {
		return errors.New("extracted binary exceeds the size limit")
	}
	return nil
}
