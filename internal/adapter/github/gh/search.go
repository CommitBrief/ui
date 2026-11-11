// SPDX-License-Identifier: GPL-3.0-or-later

package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

const (
	// searchLimit is how many pull requests one search returns at most (gh's
	// own default is 30, which a busy day can exceed).
	searchLimit = 200
	// maxSearchRepos bounds the repositories of one query, so the argument list
	// stays short.
	maxSearchRepos = 100
	// maxPRNumber is the largest pull request number accepted.
	maxPRNumber = 1<<31 - 1
	// jsonLimit caps what is parsed from a gh answer: 200 results are a few
	// hundred KiB at most.
	jsonLimit = 1 << 20
)

// repoRe is "owner/name". The first character of the owner is alphanumeric, so a
// slug can never be read as an option.
var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

func validRepo(repo string) bool {
	if len(repo) > 256 || !repoRe.MatchString(repo) {
		return false
	}
	name := repo[strings.IndexByte(repo, '/')+1:]
	return name != "." && name != ".."
}

func invalidArg(format string, a ...any) error {
	return errcode.New(errcode.Unknown, "refused: "+fmt.Sprintf(format, a...))
}

// searchArgs builds `gh search prs --review-requested=@me --state=open ...` for
// the repositories: one combined query (UI-0005 section 2). Values are attached
// to their flags with "=", so nothing can be taken for another option.
func searchArgs(repos []string) []string {
	args := []string{
		"search", "prs",
		"--review-requested=@me",
		"--state=open",
		fmt.Sprintf("--limit=%d", searchLimit),
		"--json=number,repository,isDraft,updatedAt,url",
	}
	for _, r := range repos {
		args = append(args, "--repo="+r)
	}
	return args
}

// SearchReviewRequested implements port.PRSource.
func (c *Client) SearchReviewRequested(ctx context.Context, repos []string) ([]port.PRRef, error) {
	if len(repos) == 0 {
		return []port.PRRef{}, nil
	}
	if len(repos) > maxSearchRepos {
		return nil, invalidArg("at most %d repositories per search", maxSearchRepos)
	}
	want := make(map[string]string, len(repos)) // lower-case slug -> as given
	for _, r := range repos {
		if !validRepo(r) {
			return nil, invalidArg("repository must look like owner/name")
		}
		want[strings.ToLower(r)] = r
	}
	out, err := c.runJSON(ctx, searchArgs(repos))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Number     int  `json:"number"`
		IsDraft    bool `json:"isDraft"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		UpdatedAt time.Time `json:"updatedAt"`
		URL       string    `json:"url"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, errcode.Wrap(errcode.Unknown, "gh search printed something that is not the expected JSON", err)
	}
	refs := make([]port.PRRef, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		slug, ok := want[strings.ToLower(r.Repository.NameWithOwner)]
		if !ok || r.Number <= 0 || r.Number > maxPRNumber {
			continue // not something asked for: never trusted blindly
		}
		key := fmt.Sprintf("%s#%d", strings.ToLower(slug), r.Number)
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, port.PRRef{Repo: slug, Number: r.Number, IsDraft: r.IsDraft, UpdatedAt: r.UpdatedAt, URL: r.URL})
	}
	return refs, nil
}

// runJSON locates gh, runs it with args and returns its stdout. A failure is
// classified into the closed code set: a missing gh, a missing or invalid login,
// a timeout, a cancel, or unknown with gh's own words as detail.
func (c *Client) runJSON(ctx context.Context, args []string) ([]byte, error) {
	bin, ok := c.locate(ctx)
	if ctx.Err() != nil {
		return nil, ctxError(ctx)
	}
	if !ok {
		return nil, errcode.New(errcode.GHNotFound, "the gh CLI was not found on the discovered PATH")
	}
	res, err := c.run(ctx, runSpec{Bin: bin.bin, Args: args, Env: bin.env, Timeout: c.cfg.Timeout, StdoutLimit: jsonLimit})
	if err != nil {
		return nil, errcode.Wrap(errcode.Unknown, "running gh", err)
	}
	if res.Canceled || res.TimedOut {
		return nil, interrupted(res)
	}
	if res.ExitCode != 0 {
		return nil, classifyFailure(res)
	}
	if int64(len(res.Stdout)) >= jsonLimit {
		return nil, errcode.New(errcode.Unknown, "gh printed more than the allowed size")
	}
	return bytes.TrimSpace(res.Stdout), nil
}

// authFragments are the phrases gh uses when it has no usable login. They are
// matched against a lower-cased stderr.
var authFragments = []string{
	"gh auth login",
	"not logged in",
	"bad credentials",
	"http 401",
	"authentication required",
	"authentication failed",
	"token in gh_token",
	"requires authentication",
}

// classifyFailure reduces a gh run that exited non-zero to one closed code.
func classifyFailure(res runResult) error {
	text := tail(string(res.Stderr))
	lower := strings.ToLower(text)
	for _, f := range authFragments {
		if strings.Contains(lower, f) {
			return errcode.New(errcode.GHNotAuthenticated, "gh has no usable login").WithDetail(text)
		}
	}
	return errcode.New(errcode.Unknown, fmt.Sprintf("gh exited with code %d", res.ExitCode)).WithDetail(text)
}
