// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// Production endpoints of the release feed.
const (
	DefaultRepository   = "CommitBrief/ui"
	DefaultAPIBase      = "https://api.github.com"
	DefaultDownloadBase = "https://github.com"
	// AppName prefixes every release artefact name.
	AppName = "commitbrief-ui"

	// ChecksumsName and SignatureName are the two release assets that carry the
	// trust chain.
	ChecksumsName = "checksums.txt"
	SignatureName = "checksums.txt.sig"
)

// Bounds on what is read from the network.
const (
	maxReleaseListBytes = 4 << 20
	maxChecksumsBytes   = 64 << 10
	maxNotesBytes       = 8 << 10
	// DefaultMaxArtefactBytes caps one artefact download.
	DefaultMaxArtefactBytes = 1 << 30
	releasesPerPage         = 30
	// checkTimeout bounds one whole look at the feed (the list and the two trust
	// files). The artefact download has no overall limit, only the header and
	// stall limits: its length depends on the connection.
	checkTimeout  = 60 * time.Second
	headerTimeout = 30 * time.Second
	maxRedirects  = 5
)

// SourceConfig configures the GitHub Releases source.
type SourceConfig struct {
	// Repository is "owner/name". Defaults to DefaultRepository.
	Repository string
	// APIBase and DownloadBase default to GitHub's; tests point them at a local
	// server.
	APIBase      string
	DownloadBase string
	// Client defaults to a client that bounds the wait for response headers and
	// refuses an https to http redirect. A client the caller supplies is used as
	// given.
	Client *http.Client
	// PublicKeys verify checksums.txt: a signature by any of them is accepted
	// (more than one only while a key is being rotated). Without any, every
	// Latest call fails with update.CodeKeyMissing before any request is made.
	PublicKeys []ed25519.PublicKey
	// GOOS and GOARCH select the artefact (runtime values in production).
	GOOS, GOARCH string
	// Suffix is the artefact name ending for this installation (platform.ArtifactSuffix).
	Suffix string
	// Version goes into the User-Agent.
	Version string
}

// Source reads releases from GitHub and authenticates them.
type Source struct {
	cfg SourceConfig
}

// NewSource builds the source, filling the defaults.
func NewSource(cfg SourceConfig) *Source {
	if cfg.Repository == "" {
		cfg.Repository = DefaultRepository
	}
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.DownloadBase == "" {
		cfg.DownloadBase = DefaultDownloadBase
	}
	cfg.APIBase = strings.TrimRight(cfg.APIBase, "/")
	cfg.DownloadBase = strings.TrimRight(cfg.DownloadBase, "/")
	if cfg.Client == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ResponseHeaderTimeout = headerTimeout
		cfg.Client = &http.Client{Transport: tr, CheckRedirect: refuseDowngrade}
	}
	return &Source{cfg: cfg}
}

// refuseDowngrade stops a redirect chain from leaving https.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("updater: too many redirects")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("updater: refusing a redirect from https to http")
	}
	return nil
}

// Candidate is a release that passed every check Latest can make before the
// artefact itself is downloaded: its checksums are signed by the release key and
// list the artefact for this system.
type Candidate struct {
	Release update.Release
	// Tag is the git tag of the release, used to build the download URL.
	Tag string
	// Asset is the artefact's file name.
	Asset string
	// SHA256 is the artefact's digest from the signed checksums.
	SHA256 [sha256.Size]byte
}

// releaseJSON and assetJSON are the parts of the GitHub release payload used.
type releaseJSON struct {
	TagName     string      `json:"tag_name"`
	Draft       bool        `json:"draft"`
	Prerelease  bool        `json:"prerelease"`
	Body        string      `json:"body"`
	PublishedAt time.Time   `json:"published_at"`
	Assets      []assetJSON `json:"assets"`
}

type assetJSON struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

// artifactName is the release artefact for a version on this system:
// commitbrief-ui_<version>_<os>_<arch><suffix>. macOS ships one universal build.
// The version in the name is what ties a signed checksum to one release: signed
// checksums of an older release list older names, so replaying them under a
// newer tag finds no entry.
func artifactName(version update.Version, goos, goarch, suffix string) string {
	arch := goarch
	if goos == "darwin" {
		arch = "universal"
	}
	return AppName + "_" + version.String() + "_" + goos + "_" + arch + suffix
}

// Latest returns the newest release on channel that is higher than current, with
// its artefact authenticated, or nil when there is none. Nothing is downloaded
// except the release list and the two small trust files.
func (s *Source) Latest(ctx context.Context, current update.Version, channel update.Channel) (*Candidate, error) {
	if len(s.cfg.PublicKeys) == 0 {
		return nil, update.New(update.CodeKeyMissing, "")
	}
	if s.cfg.Suffix == "" {
		return nil, update.Wrap(update.CodeNoArtifact, errors.New("updater: this installation has no release artefact"))
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	releases, err := s.listReleases(ctx)
	if err != nil {
		return nil, err
	}

	var best *releaseJSON
	var bestVersion update.Version
	for i := range releases {
		r := &releases[i]
		if r.Draft {
			continue
		}
		v, err := update.ParseVersion(r.TagName)
		if err != nil || !channel.Accepts(v) {
			continue
		}
		if best == nil || v.Compare(bestVersion) > 0 {
			best, bestVersion = r, v
		}
	}
	if best == nil || bestVersion.Compare(current) <= 0 {
		return nil, nil
	}

	name := artifactName(bestVersion, s.cfg.GOOS, s.cfg.GOARCH, s.cfg.Suffix)
	have := map[string]assetJSON{}
	for _, a := range best.Assets {
		if a.State == "" || a.State == "uploaded" {
			have[a.Name] = a
		}
	}
	// The trust files first: a release without them is rejected whatever else
	// it holds.
	if _, ok := have[SignatureName]; !ok {
		return nil, update.Wrap(update.CodeSignatureMissing, fmt.Errorf("updater: release %s has no %s", best.TagName, SignatureName))
	}
	if _, ok := have[ChecksumsName]; !ok {
		return nil, update.Wrap(update.CodeChecksumMissing, fmt.Errorf("updater: release %s has no %s", best.TagName, ChecksumsName))
	}
	asset, ok := have[name]
	if !ok {
		return nil, update.Wrap(update.CodeNoArtifact, fmt.Errorf("updater: release %s has no %s", best.TagName, name))
	}
	if asset.Size <= 0 || asset.Size > DefaultMaxArtefactBytes {
		return nil, update.Wrap(update.CodeNoArtifact, fmt.Errorf("updater: %s has an unusable size", name))
	}

	checksums, err := s.fetchSmall(ctx, best.TagName, ChecksumsName, maxChecksumsBytes)
	if err != nil {
		return nil, err
	}
	sig, err := s.fetchSmall(ctx, best.TagName, SignatureName, 4<<10)
	if err != nil {
		return nil, err
	}
	if err := VerifyChecksumsAny(s.cfg.PublicKeys, checksums, sig); err != nil {
		code := update.CodeSignatureInvalid
		if errors.Is(err, ErrSignatureMissing) {
			code = update.CodeSignatureMissing
		}
		return nil, update.Wrap(code, err)
	}
	listed, err := ParseChecksums(checksums)
	if err != nil {
		return nil, update.Wrap(update.CodeChecksumMissing, err)
	}
	sum, ok := listed[name]
	if !ok {
		return nil, update.Wrap(update.CodeChecksumMissing, fmt.Errorf("updater: the signed checksums do not list %s", name))
	}

	return &Candidate{
		Release: update.Release{
			Version:     bestVersion.String(),
			Prerelease:  bestVersion.IsPrerelease(),
			Notes:       sanitizeNotes(best.Body),
			PublishedAt: best.PublishedAt.UTC(),
			Size:        asset.Size,
		},
		Tag:    best.TagName,
		Asset:  name,
		SHA256: sum,
	}, nil
}

// Open starts the download of a candidate's artefact. The caller hashes what it
// reads against Candidate.SHA256; Open itself trusts nothing it receives.
func (s *Source) Open(ctx context.Context, c *Candidate) (io.ReadCloser, int64, error) {
	resp, err := s.get(ctx, s.downloadURL(c.Tag, c.Asset))
	if err != nil {
		return nil, 0, update.Wrap(update.CodeDownloadFailed, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, update.Wrap(update.CodeDownloadFailed, fmt.Errorf("updater: download answered %s", resp.Status))
	}
	return resp.Body, resp.ContentLength, nil
}

func (s *Source) downloadURL(tag, name string) string {
	return s.cfg.DownloadBase + "/" + s.cfg.Repository + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

func (s *Source) listReleases(ctx context.Context) ([]releaseJSON, error) {
	u := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", s.cfg.APIBase, s.cfg.Repository, releasesPerPage)
	resp, err := s.get(ctx, u)
	if err != nil {
		return nil, update.Wrap(update.CodeCheckFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, update.Wrap(feedFailureCode(resp), fmt.Errorf("updater: the release list answered %s", resp.Status))
	}
	data, err := readLimited(resp.Body, maxReleaseListBytes)
	if err != nil {
		return nil, update.Wrap(update.CodeCheckFailed, err)
	}
	var out []releaseJSON
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, update.Wrap(update.CodeCheckFailed, fmt.Errorf("updater: the release list is not valid: %w", err))
	}
	return out, nil
}

// feedFailureCode classifies a non-200 answer of the release list. GitHub
// answers an anonymous request for a private (or missing) repository with 404,
// and 401/403 when the feed demands credentials: the feed is then unavailable to
// this application, which never carries a token (UI-0007 §Update 2026-11-12).
// That is reported as such, not as "no update". A rate limit (403/429 with the
// limit exhausted, or a Retry-After) is a transient failure of the check, not of the feed.
func feedFailureCode(resp *http.Response) update.Code {
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusUnauthorized:
		return update.CodeFeedUnavailable
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" {
			return update.CodeCheckFailed
		}
		return update.CodeFeedUnavailable
	}
	return update.CodeCheckFailed
}

// fetchSmall downloads one of the trust files.
func (s *Source) fetchSmall(ctx context.Context, tag, name string, limit int64) ([]byte, error) {
	resp, err := s.get(ctx, s.downloadURL(tag, name))
	if err != nil {
		return nil, update.Wrap(update.CodeCheckFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, update.Wrap(update.CodeCheckFailed, fmt.Errorf("updater: %s answered %s", name, resp.Status))
	}
	data, err := readLimited(resp.Body, limit)
	if err != nil {
		return nil, update.Wrap(update.CodeCheckFailed, err)
	}
	return data, nil
}

func (s *Source) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", AppName+"/"+s.cfg.Version)
	return s.cfg.Client.Do(req)
}

// readLimited reads at most limit bytes and fails, instead of truncating, when
// the body is longer: a cut-off trust file must not be verified as if whole.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("updater: a response is larger than %d bytes", limit)
	}
	return data, nil
}

// sanitizeNotes makes the release text safe to show as plain text: carriage
// returns and control characters (other than newline and tab) are dropped, and
// the text is cut on a rune boundary at maxNotesBytes. It is never rendered as
// markup.
func sanitizeNotes(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) <= maxNotesBytes {
		return s
	}
	cut := maxNotesBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
