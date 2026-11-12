// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/update"
)

const (
	testRepo   = "CommitBrief/ui"
	testGOOS   = "linux"
	testGOARCH = "amd64"
	testSuffix = ".AppImage"
)

// testRelease is one release the fake feed serves.
type testRelease struct {
	tag        string
	prerelease bool
	draft      bool
	body       string
	// files maps an asset name to the bytes served for it. Every key is listed
	// as an asset of the release.
	files map[string][]byte
	// listedSize overrides the size the release list reports for an asset.
	listedSize map[string]int64
}

func versionOf(t *testing.T, tag string) update.Version {
	t.Helper()
	v, err := update.ParseVersion(tag)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// artefactFor is the artefact name the source looks for on the test system.
func artefactFor(t *testing.T, tag string) string {
	return artifactName(versionOf(t, tag), testGOOS, testGOARCH, testSuffix)
}

// newRelease builds a release whose checksums.txt lists payload under the
// artefact name.
func newRelease(t *testing.T, tag string, payload []byte) *testRelease {
	t.Helper()
	name := artefactFor(t, tag)
	sum := sha256.Sum256(payload)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n" +
		strings.Repeat("0", 64) + "  commitbrief-ui_other.deb\n")
	return &testRelease{
		tag:  tag,
		body: "Notes for " + tag,
		files: map[string][]byte{
			name:          payload,
			ChecksumsName: checksums,
		},
	}
}

// testFeed is a fake GitHub: the release list API and the download paths.
type testFeed struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	releases []*testRelease
	requests atomic.Int64
	// hook, when set, may answer a request itself; it returns true when it did.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

func newTestFeed(t *testing.T, releases ...*testRelease) *testFeed {
	t.Helper()
	f := &testFeed{t: t, releases: releases}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *testFeed) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hook != nil && f.hook(w, r) {
		return
	}
	apiPath := "/api/repos/" + testRepo + "/releases"
	dlPrefix := "/dl/" + testRepo + "/releases/download/"
	switch {
	case r.URL.Path == apiPath:
		type asset struct {
			Name  string `json:"name"`
			Size  int64  `json:"size"`
			State string `json:"state"`
		}
		type rel struct {
			TagName     string    `json:"tag_name"`
			Draft       bool      `json:"draft"`
			Prerelease  bool      `json:"prerelease"`
			Body        string    `json:"body"`
			PublishedAt time.Time `json:"published_at"`
			Assets      []asset   `json:"assets"`
		}
		var out []rel
		for _, tr := range f.releases {
			e := rel{TagName: tr.tag, Draft: tr.draft, Prerelease: tr.prerelease, Body: tr.body,
				PublishedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
			for name, data := range tr.files {
				size := int64(len(data))
				if v, ok := tr.listedSize[name]; ok {
					size = v
				}
				e.Assets = append(e.Assets, asset{Name: name, Size: size, State: "uploaded"})
			}
			out = append(out, e)
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, dlPrefix):
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, dlPrefix), "/", 2)
		if len(parts) == 2 {
			for _, tr := range f.releases {
				if tr.tag == parts[0] {
					if data, ok := tr.files[parts[1]]; ok {
						_, _ = w.Write(data)
						return
					}
				}
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// source builds a Source for the test system pointed at the feed.
func (f *testFeed) source() *Source {
	return NewSource(SourceConfig{
		Repository:   testRepo,
		APIBase:      f.srv.URL + "/api",
		DownloadBase: f.srv.URL + "/dl",
		GOOS:         testGOOS,
		GOARCH:       testGOARCH,
		Suffix:       testSuffix,
		Version:      "0.0.0-test",
		allowHTTP:    true,
	})
}

func mustVersion(t *testing.T, s string) update.Version { return versionOf(t, s) }

// codeOf extracts the update.Code of an error, "" when it has none.
func codeOf(err error) update.Code {
	var ue *update.Error
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}
