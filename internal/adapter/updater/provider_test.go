// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/update"
)

func TestUpdateLatestAcceptsAListedRelease(t *testing.T) {
	payload := []byte("the new application")
	rel := newRelease(t, "v0.0.1-test.2", payload)
	rel.prerelease = true
	rel.body = "Fixes\r\n\x1b[31mred\x1b[0m\x00 and <script>alert(1)</script>"
	feed := newTestFeed(t, rel)

	cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.0.1-test.1"), update.ChannelPrerelease)
	if err != nil {
		t.Fatal(err)
	}
	if cand == nil {
		t.Fatal("no candidate")
	}
	if cand.Release.Version != "0.0.1-test.2" || !cand.Release.Prerelease {
		t.Errorf("release = %+v", cand.Release)
	}
	if cand.Release.Size != int64(len(payload)) {
		t.Errorf("size = %d", cand.Release.Size)
	}
	if cand.Asset != "commitbrief-ui_0.0.1-test.2_linux_amd64.AppImage" || cand.Tag != "v0.0.1-test.2" {
		t.Errorf("asset/tag = %q %q", cand.Asset, cand.Tag)
	}
	if cand.SHA256 != sha256.Sum256(payload) {
		t.Error("the digest is not the one from the signed checksums")
	}
	// Plain text only: control characters are gone, markup is kept as text.
	if strings.ContainsAny(cand.Release.Notes, "\x00\x1b\r") {
		t.Errorf("notes keep control characters: %q", cand.Release.Notes)
	}
	if !strings.Contains(cand.Release.Notes, "<script>") {
		t.Errorf("notes were altered beyond control characters: %q", cand.Release.Notes)
	}
}

func TestUpdateLatestMacOSUsesTheUniversalArtefact(t *testing.T) {
	v := mustVersion(t, "0.2.0")
	name := artifactName(v, "darwin", "arm64", ".zip")
	if name != "commitbrief-ui_0.2.0_darwin_universal.zip" {
		t.Fatalf("name = %q", name)
	}
	rel := newRelease(t, "v0.2.0", []byte("x"))
	// Re-list for the macOS name.
	payload := []byte("mac app")
	sum := sha256.Sum256(payload)
	checksums := []byte(strings.Repeat("0", 64) + "  other\n")
	checksums = append([]byte(hexString(sum)+"  "+name+"\n"), checksums...)
	rel.files = map[string][]byte{name: payload, ChecksumsName: checksums}
	feed := newTestFeed(t, rel)
	src := NewSource(SourceConfig{Repository: testRepo, APIBase: feed.srv.URL + "/api", DownloadBase: feed.srv.URL + "/dl",
		GOOS: "darwin", GOARCH: "arm64", Suffix: ".zip", allowHTTP: true})
	cand, err := src.Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if err != nil || cand == nil || cand.Asset != name {
		t.Fatalf("Latest = %+v, %v", cand, err)
	}
}

func hexString(sum [sha256.Size]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range sum {
		out = append(out, digits[b>>4], digits[b&15])
	}
	return string(out)
}

func TestUpdateLatestChannelsAndVersions(t *testing.T) {
	mk := func(tag string, pre, draft bool) *testRelease {
		r := newRelease(t, tag, []byte("payload "+tag))
		r.prerelease, r.draft = pre, draft
		return r
	}
	releases := []*testRelease{
		mk("v0.1.0", false, false),
		mk("v0.2.0-rc.1", true, false),
		mk("v0.3.0", false, true), // a draft is never offered
		mk("v0.1.1", false, false),
		{tag: "nightly", files: map[string][]byte{"x": []byte("x")}}, // not a version
		mk("v0.2.0-rc.2", true, false),
	}
	feed := newTestFeed(t, releases...)
	src := feed.source()
	ctx := context.Background()

	tests := []struct {
		name    string
		current string
		channel update.Channel
		want    string // "" means up to date
	}{
		{"stable skips pre-releases and drafts", "0.1.0", update.ChannelStable, "0.1.1"},
		{"prerelease channel takes the highest", "0.1.0", update.ChannelPrerelease, "0.2.0-rc.2"},
		{"already on the newest stable", "0.1.1", update.ChannelStable, ""},
		{"never offers the running version", "0.2.0-rc.2", update.ChannelPrerelease, ""},
		{"never offers an older one (replay of an old release)", "0.9.0", update.ChannelPrerelease, ""},
		{"pre-release to its own release", "0.1.1-rc.1", update.ChannelStable, "0.1.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cand, err := src.Latest(ctx, mustVersion(t, tc.current), tc.channel)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if cand != nil {
				got = cand.Release.Version
			}
			if got != tc.want {
				t.Errorf("Latest = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpdateLatestRejectsAnUnusableRelease(t *testing.T) {
	const tag = "v1.0.0"
	cur := mustVersion(t, "0.9.0")
	payload := []byte("app")

	tests := []struct {
		name   string
		mutate func(r *testRelease)
		want   update.Code
	}{
		{"no checksums asset", func(r *testRelease) { delete(r.files, ChecksumsName) }, update.CodeChecksumMissing},
		{"empty checksums", func(r *testRelease) { r.files[ChecksumsName] = nil }, update.CodeChecksumMissing},
		{"checksums do not list the artefact", func(r *testRelease) {
			r.files[ChecksumsName] = []byte(strings.Repeat("a", 64) + "  something_else.zip\n")
		}, update.CodeChecksumMissing},
		{"checksums are malformed", func(r *testRelease) {
			r.files[ChecksumsName] = []byte("this is not a checksums file\n")
		}, update.CodeChecksumMissing},
		{"checksums list the artefact twice", func(r *testRelease) {
			name := artefactFor(t, tag)
			r.files[ChecksumsName] = []byte(strings.Repeat("a", 64) + "  " + name + "\n" + strings.Repeat("b", 64) + "  " + name + "\n")
		}, update.CodeChecksumMissing},
		{"no artefact for this system", func(r *testRelease) { delete(r.files, artefactFor(t, tag)) }, update.CodeNoArtifact},
		{"artefact of an unusable size", func(r *testRelease) { r.listedSize = map[string]int64{artefactFor(t, tag): 0} }, update.CodeNoArtifact},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := newRelease(t, tag, payload)
			tc.mutate(rel)
			feed := newTestFeed(t, rel)
			cand, err := feed.source().Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil {
				t.Fatalf("an unusable release was accepted: %+v", cand)
			}
			if got := codeOf(err); got != tc.want {
				t.Errorf("code = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestUpdateLatestRejectsReplayedChecksums(t *testing.T) {
	// A feed that serves the checksums.txt of an old release under a new tag gets
	// nowhere: the artefact name carries the version, so the new name is not in
	// the old list.
	old := newRelease(t, "v0.1.0", []byte("old app"))
	forged := newRelease(t, "v0.2.0", []byte("attacker app"))
	forged.files[ChecksumsName] = old.files[ChecksumsName]
	feed := newTestFeed(t, forged)

	cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if cand != nil || codeOf(err) != update.CodeChecksumMissing {
		t.Errorf("Latest = %+v, %v; want a rejection with %q", cand, err, update.CodeChecksumMissing)
	}
}

// Nothing is fetched over plain http: with no signature, the transport is what
// stands between the user and a stranger on the path.
func TestUpdateSourceIsHTTPSOnly(t *testing.T) {
	feed := newTestFeed(t, newRelease(t, "v1.0.0", []byte("x")))
	// The production configuration: allowHTTP is off, the test server is http.
	src := NewSource(SourceConfig{Repository: testRepo, APIBase: feed.srv.URL + "/api", DownloadBase: feed.srv.URL + "/dl",
		GOOS: testGOOS, GOARCH: testGOARCH, Suffix: testSuffix})
	cand, err := src.Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if cand != nil || codeOf(err) != update.CodeCheckFailed {
		t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeCheckFailed)
	}
	if _, _, err := src.Open(context.Background(), &Candidate{Tag: "v1.0.0", Asset: artefactFor(t, "v1.0.0")}); codeOf(err) != update.CodeDownloadFailed {
		t.Errorf("Open over http: %v", err)
	}
	if n := feed.requests.Load(); n != 0 {
		t.Errorf("%d requests reached an http server", n)
	}
}

func TestUpdateRedirectPolicy(t *testing.T) {
	src := NewSource(SourceConfig{})
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	from := mustURL("https://github.com/CommitBrief/ui/releases/download/v1.0.0/checksums.txt")
	tests := []struct {
		to      string
		allowed bool
	}{
		{"https://github.com/other/path", true},
		{"https://objects.githubusercontent.com/x", true},
		{"https://release-assets.githubusercontent.com/x", true},
		{"https://GITHUB.com/x", true},
		{"https://api.github.com/x", true},
		{"http://github.com/x", false},
		{"http://objects.githubusercontent.com/x", false},
		{"https://example.com/x", false},
		{"https://githubusercontent.com.evil.example/x", false},
		{"https://evilgithub.com/x", false},
		{"https://notgithubusercontent.com/x", false},
		{"https://github.com.evil.example/x", false},
		{"ftp://github.com/x", false},
	}
	for _, tc := range tests {
		err := src.allowRedirect(from, mustURL(tc.to))
		if (err == nil) != tc.allowed {
			t.Errorf("redirect to %s: err = %v, allowed = %v", tc.to, err, tc.allowed)
		}
	}
}

// End to end: a download that is redirected to another host is refused, and the
// redirect hop limit holds.
func TestUpdateRefusesARedirectToAnotherHost(t *testing.T) {
	feed := newTestFeed(t, newRelease(t, "v0.2.0", []byte("x")))
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  "+artefactFor(t, "v0.2.0")+"\n")
	}))
	t.Cleanup(evil.Close)
	// "localhost" is another host than the feed's 127.0.0.1, so this is the
	// off-origin redirect.
	target := strings.Replace(evil.URL, "127.0.0.1", "localhost", 1)
	feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/"+ChecksumsName) {
			http.Redirect(w, r, target+"/checksums.txt", http.StatusFound)
			return true
		}
		return false
	}
	cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if cand != nil || codeOf(err) != update.CodeCheckFailed {
		t.Errorf("Latest = %+v, %v; want a refusal with %q", cand, err, update.CodeCheckFailed)
	}

	// A redirect that stays on the host is followed.
	feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/"+ChecksumsName) && r.URL.RawQuery == "" {
			http.Redirect(w, r, r.URL.Path+"?hop=1", http.StatusFound)
			return true
		}
		return false
	}
	if cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable); err != nil || cand == nil {
		t.Errorf("a same-host redirect was not followed: %+v, %v", cand, err)
	}

	// A redirect loop ends at the hop limit.
	var hops atomic.Int64
	feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/"+ChecksumsName) {
			http.Redirect(w, r, fmt.Sprintf("%s?hop=%d", r.URL.Path, hops.Add(1)), http.StatusFound)
			return true
		}
		return false
	}
	if cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable); cand != nil || codeOf(err) != update.CodeCheckFailed {
		t.Errorf("a redirect loop: %+v, %v", cand, err)
	}
	if n := hops.Load(); n > maxRedirects+1 {
		t.Errorf("%d hops followed, the limit is %d", n, maxRedirects)
	}
}

func TestUpdateLatestWithoutSuffix(t *testing.T) {
	feed := newTestFeed(t, newRelease(t, "v1.0.0", []byte("x")))
	src := NewSource(SourceConfig{Repository: testRepo, APIBase: feed.srv.URL + "/api", DownloadBase: feed.srv.URL + "/dl", GOOS: "linux", GOARCH: "amd64", allowHTTP: true})
	if _, err := src.Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable); codeOf(err) != update.CodeNoArtifact {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateLatestFeedFailures(t *testing.T) {
	cur := mustVersion(t, "0.1.0")
	tests := []struct {
		name string
		hook func(w http.ResponseWriter, r *http.Request) bool
	}{
		{"release list answers 500", func(w http.ResponseWriter, r *http.Request) bool {
			http.Error(w, "boom", http.StatusInternalServerError)
			return true
		}},
		{"release list is rate limited", func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, "rate limit", http.StatusForbidden)
			return true
		}},
		{"release list is secondarily rate limited", func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "slow down", http.StatusForbidden)
			return true
		}},
		{"release list is too many requests", func(w http.ResponseWriter, r *http.Request) bool {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return true
		}},
		{"release list is not JSON", func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = io.WriteString(w, "<html>")
			return true
		}},
		{"release list is too large", func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(strings.Repeat("[", maxReleaseListBytes+10)))
			return true
		}},
		{"checksums file is too large", func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/"+ChecksumsName) {
				_, _ = w.Write(make([]byte, maxChecksumsBytes+1))
				return true
			}
			return false
		}},
		{"checksums.txt is missing on the server", func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/"+ChecksumsName) {
				http.NotFound(w, r)
				return true
			}
			return false
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			feed := newTestFeed(t, newRelease(t, "v0.2.0", []byte("x")))
			feed.hook = tc.hook
			cand, err := feed.source().Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil || codeOf(err) != update.CodeCheckFailed {
				t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeCheckFailed)
			}
		})
	}

	// The feed is not reachable at all.
	feed := newTestFeed(t)
	src := feed.source()
	feed.srv.Close()
	if _, err := src.Latest(context.Background(), cur, update.ChannelStable); codeOf(err) != update.CodeCheckFailed {
		t.Errorf("unreachable feed: %v", err)
	}
}

func TestUpdateOpen(t *testing.T) {
	payload := []byte("bytes of the artefact")
	feed := newTestFeed(t, newRelease(t, "v0.2.0", payload))
	src := feed.source()
	cand, err := src.Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if err != nil || cand == nil {
		t.Fatalf("Latest = %v, %v", cand, err)
	}
	body, size, err := src.Open(context.Background(), cand)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != string(payload) || size != int64(len(payload)) {
		t.Errorf("Open returned %q (size %d)", got, size)
	}

	missing := *cand
	missing.Asset = "no-such-file"
	if _, _, err := src.Open(context.Background(), &missing); codeOf(err) != update.CodeDownloadFailed {
		t.Errorf("a missing artefact: %v", err)
	}
	// Names are escaped into the URL, never spliced.
	evil := *cand
	evil.Asset = "../../../etc/passwd"
	if u := src.downloadURL(evil.Tag, evil.Asset); strings.Contains(u, "/../") {
		t.Errorf("unescaped download URL %q", u)
	}
}

func TestUpdateCheckRedirectHopsAndScheme(t *testing.T) {
	src := NewSource(SourceConfig{})
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if err := src.checkRedirect(mk("http://github.com/x"), []*http.Request{mk("https://github.com/x")}); err == nil {
		t.Error("an https to http redirect was followed")
	}
	if err := src.checkRedirect(mk("https://objects.githubusercontent.com/x"), []*http.Request{mk("https://github.com/x")}); err != nil {
		t.Errorf("an https redirect to GitHub's asset host was refused: %v", err)
	}
	if err := src.checkRedirect(mk("https://cdn.example/x"), []*http.Request{mk("https://github.com/x")}); err == nil {
		t.Error("a redirect to another host was followed")
	}
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = mk("https://github.com/x")
	}
	if err := src.checkRedirect(mk("https://github.com/y"), via); err == nil {
		t.Error("a redirect loop was followed")
	}
}

func TestUpdateSanitizeNotes(t *testing.T) {
	if got := sanitizeNotes("a\r\nb\tc\x07\x1b[0m\u0085"); got != "a\nb\tc[0m" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("é", maxNotesBytes) // 2 bytes per rune
	got := sanitizeNotes(long)
	if len(got) > maxNotesBytes+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("len %d, suffix %q", len(got), got[len(got)-3:])
	}
	if strings.ContainsRune(got, '�') {
		t.Error("cut inside a rune")
	}
	if sanitizeNotes("  \n  ") != "" {
		t.Error("blank notes must be empty")
	}
}

// A feed GitHub answers 404 (a private or missing repository, the anonymous view
// of it) or 401/403 is "unavailable", never "no update": the user must be told
// that nothing was checked. Nothing is downloaded or trusted from such a feed.
func TestUpdateLatestUnavailableFeedIsAnError(t *testing.T) {
	cur := mustVersion(t, "0.1.0")
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			feed := newTestFeed(t, newRelease(t, "v0.2.0", []byte("x")))
			feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
				http.Error(w, `{"message":"Not Found"}`, status)
				return true
			}
			cand, err := feed.source().Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil || codeOf(err) != update.CodeFeedUnavailable {
				t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeFeedUnavailable)
			}
		})
	}

	// A public repository with no release yet is a healthy feed: no update.
	feed := newTestFeed(t)
	if cand, err := feed.source().Latest(context.Background(), cur, update.ChannelStable); cand != nil || err != nil {
		t.Errorf("empty feed: Latest = %+v, %v; want nil, nil", cand, err)
	}
}
