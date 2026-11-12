// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/update"
)

func TestUpdateLatestAcceptsASignedRelease(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("the new application")
	rel := signedRelease(t, "v0.0.1-test.2", priv, payload)
	rel.prerelease = true
	rel.body = "Fixes\r\n\x1b[31mred\x1b[0m\x00 and <script>alert(1)</script>"
	feed := newTestFeed(t, rel)

	cand, err := feed.source(pub).Latest(context.Background(), mustVersion(t, "0.0.1-test.1"), update.ChannelPrerelease)
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
	pub, priv := newKey(t)
	v := mustVersion(t, "0.2.0")
	name := artifactName(v, "darwin", "arm64", ".zip")
	if name != "commitbrief-ui_0.2.0_darwin_universal.zip" {
		t.Fatalf("name = %q", name)
	}
	rel := signedRelease(t, "v0.2.0", priv, []byte("x"))
	// Re-sign for the macOS name.
	payload := []byte("mac app")
	sum := sha256.Sum256(payload)
	checksums := []byte(strings.Repeat("0", 64) + "  other\n")
	checksums = append([]byte(hexString(sum)+"  "+name+"\n"), checksums...)
	rel.files = map[string][]byte{name: payload, ChecksumsName: checksums, SignatureName: EncodeSignature(ed25519.Sign(priv, checksums))}
	feed := newTestFeed(t, rel)
	src := NewSource(SourceConfig{Repository: testRepo, APIBase: feed.srv.URL + "/api", DownloadBase: feed.srv.URL + "/dl",
		PublicKeys: []ed25519.PublicKey{pub}, GOOS: "darwin", GOARCH: "arm64", Suffix: ".zip"})
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
	pub, priv := newKey(t)
	mk := func(tag string, pre, draft bool) *testRelease {
		r := signedRelease(t, tag, priv, []byte("payload "+tag))
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
	src := feed.source(pub)
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

func TestUpdateLatestRejectsAnUntrustedRelease(t *testing.T) {
	pub, priv := newKey(t)
	_, otherPriv := newKey(t)
	const tag = "v1.0.0"
	cur := mustVersion(t, "0.9.0")
	payload := []byte("app")

	tests := []struct {
		name   string
		mutate func(r *testRelease)
		want   update.Code
	}{
		{"no signature asset", func(r *testRelease) { delete(r.files, SignatureName) }, update.CodeSignatureMissing},
		{"empty signature", func(r *testRelease) { r.files[SignatureName] = nil }, update.CodeSignatureMissing},
		{"no checksums asset", func(r *testRelease) { delete(r.files, ChecksumsName) }, update.CodeChecksumMissing},
		{"garbage signature", func(r *testRelease) { r.files[SignatureName] = []byte("not a signature") }, update.CodeSignatureInvalid},
		{"signature of another key", func(r *testRelease) {
			r.files[SignatureName] = EncodeSignature(ed25519.Sign(otherPriv, r.files[ChecksumsName]))
		}, update.CodeSignatureInvalid},
		{"checksums changed after signing", func(r *testRelease) {
			r.files[ChecksumsName] = append([]byte(strings.Repeat("a", 64)+"  evil\n"), r.files[ChecksumsName]...)
		}, update.CodeSignatureInvalid},
		{"signed checksums do not list the artefact", func(r *testRelease) {
			cs := []byte(strings.Repeat("a", 64) + "  something_else.zip\n")
			r.files[ChecksumsName] = cs
			r.files[SignatureName] = EncodeSignature(ed25519.Sign(priv, cs))
		}, update.CodeChecksumMissing},
		{"signed checksums are malformed", func(r *testRelease) {
			cs := []byte("this is not a checksums file\n")
			r.files[ChecksumsName] = cs
			r.files[SignatureName] = EncodeSignature(ed25519.Sign(priv, cs))
		}, update.CodeChecksumMissing},
		{"no artefact for this system", func(r *testRelease) { delete(r.files, artefactFor(t, tag)) }, update.CodeNoArtifact},
		{"artefact of an unusable size", func(r *testRelease) { r.listedSize = map[string]int64{artefactFor(t, tag): 0} }, update.CodeNoArtifact},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := signedRelease(t, tag, priv, payload)
			tc.mutate(rel)
			feed := newTestFeed(t, rel)
			cand, err := feed.source(pub).Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil {
				t.Fatalf("an untrusted release was accepted: %+v", cand)
			}
			if got := codeOf(err); got != tc.want {
				t.Errorf("code = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestUpdateLatestRejectsReplayedChecksums(t *testing.T) {
	// A feed that serves the signed checksums of an old release under a new tag
	// gets nowhere: the artefact name carries the version, so the new name is not
	// in the old signed list.
	pub, priv := newKey(t)
	old := signedRelease(t, "v0.1.0", priv, []byte("old app"))
	forged := signedRelease(t, "v0.2.0", priv, []byte("attacker app"))
	forged.files[ChecksumsName] = old.files[ChecksumsName]
	forged.files[SignatureName] = old.files[SignatureName]
	feed := newTestFeed(t, forged)

	cand, err := feed.source(pub).Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if cand != nil || codeOf(err) != update.CodeChecksumMissing {
		t.Errorf("Latest = %+v, %v; want a rejection with %q", cand, err, update.CodeChecksumMissing)
	}
}

func TestUpdateLatestWithoutKeyMakesNoRequest(t *testing.T) {
	_, priv := newKey(t)
	feed := newTestFeed(t, signedRelease(t, "v1.0.0", priv, []byte("x")))
	cand, err := feed.source().Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable)
	if cand != nil || codeOf(err) != update.CodeKeyMissing {
		t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeKeyMissing)
	}
	if n := feed.requests.Load(); n != 0 {
		t.Errorf("%d requests were made without a key", n)
	}
}

func TestUpdateLatestAcceptsAnyKeyOfARotationSet(t *testing.T) {
	// During a rotation the compiled-in set holds the old and the new key; a
	// release signed with either is accepted, one signed with a third is not.
	oldPub, oldPriv := newKey(t)
	newPub, newPriv := newKey(t)
	strangerPub, _ := newKey(t)
	cur := mustVersion(t, "0.1.0")
	ctx := context.Background()

	feed := newTestFeed(t, signedRelease(t, "v0.2.0", oldPriv, []byte("a")))
	if cand, err := feed.source(newPub, oldPub).Latest(ctx, cur, update.ChannelStable); err != nil || cand == nil {
		t.Errorf("signed with the old key, both listed: %v, %v", cand, err)
	}
	feed = newTestFeed(t, signedRelease(t, "v0.2.0", newPriv, []byte("a")))
	if cand, err := feed.source(newPub, oldPub).Latest(ctx, cur, update.ChannelStable); err != nil || cand == nil {
		t.Errorf("signed with the new key, both listed: %v, %v", cand, err)
	}
	if cand, err := feed.source(oldPub, strangerPub).Latest(ctx, cur, update.ChannelStable); cand != nil || codeOf(err) != update.CodeSignatureInvalid {
		t.Errorf("signed with a key outside the set: %v, %v", cand, err)
	}
}

func TestUpdateLatestWithoutSuffix(t *testing.T) {
	pub, priv := newKey(t)
	feed := newTestFeed(t, signedRelease(t, "v1.0.0", priv, []byte("x")))
	src := NewSource(SourceConfig{Repository: testRepo, APIBase: feed.srv.URL + "/api", DownloadBase: feed.srv.URL + "/dl", PublicKeys: []ed25519.PublicKey{pub}, GOOS: "linux", GOARCH: "amd64"})
	if _, err := src.Latest(context.Background(), mustVersion(t, "0.1.0"), update.ChannelStable); codeOf(err) != update.CodeNoArtifact {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateLatestFeedFailures(t *testing.T) {
	pub, priv := newKey(t)
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
		{"signature file is missing on the server", func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/"+SignatureName) {
				http.NotFound(w, r)
				return true
			}
			return false
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			feed := newTestFeed(t, signedRelease(t, "v0.2.0", priv, []byte("x")))
			feed.hook = tc.hook
			cand, err := feed.source(pub).Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil || codeOf(err) != update.CodeCheckFailed {
				t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeCheckFailed)
			}
		})
	}

	// The feed is not reachable at all.
	feed := newTestFeed(t)
	src := feed.source(pub)
	feed.srv.Close()
	if _, err := src.Latest(context.Background(), cur, update.ChannelStable); codeOf(err) != update.CodeCheckFailed {
		t.Errorf("unreachable feed: %v", err)
	}
}

func TestUpdateOpen(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("bytes of the artefact")
	feed := newTestFeed(t, signedRelease(t, "v0.2.0", priv, payload))
	src := feed.source(pub)
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

func TestUpdateRefuseDowngrade(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if err := refuseDowngrade(mk("http://evil.example/x"), []*http.Request{mk("https://github.com/x")}); err == nil {
		t.Error("an https to http redirect was followed")
	}
	if err := refuseDowngrade(mk("https://cdn.example/x"), []*http.Request{mk("https://github.com/x")}); err != nil {
		t.Errorf("an https to https redirect was refused: %v", err)
	}
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = mk("https://github.com/x")
	}
	if err := refuseDowngrade(mk("https://github.com/y"), via); err == nil {
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
	pub, priv := newKey(t)
	cur := mustVersion(t, "0.1.0")
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			feed := newTestFeed(t, signedRelease(t, "v0.2.0", priv, []byte("x")))
			feed.hook = func(w http.ResponseWriter, r *http.Request) bool {
				http.Error(w, `{"message":"Not Found"}`, status)
				return true
			}
			cand, err := feed.source(pub).Latest(context.Background(), cur, update.ChannelStable)
			if cand != nil || codeOf(err) != update.CodeFeedUnavailable {
				t.Errorf("Latest = %+v, %v; want %q", cand, err, update.CodeFeedUnavailable)
			}
		})
	}

	// A public repository with no release yet is a healthy feed: no update.
	feed := newTestFeed(t)
	if cand, err := feed.source(pub).Latest(context.Background(), cur, update.ChannelStable); cand != nil || err != nil {
		t.Errorf("empty feed: Latest = %+v, %v; want nil, nil", cand, err)
	}
}
