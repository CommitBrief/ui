// SPDX-License-Identifier: GPL-3.0-or-later

package gh

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

// maxAccountLen bounds a login read from gh's output.
const maxAccountLen = 100

// authStatusArgs asks about github.com only (UI-0005): another host's state is
// none of the UI's business. `--show-token` is never passed; gh prints the
// token masked, and nothing of that line is kept.
var authStatusArgs = []string{"auth", "status", "--hostname", port.GitHubHost}

// AuthStatus implements port.PRSource: `gh auth status --hostname github.com`.
//
// gh exits 0 when every account of the host works and 1 when any account has a
// problem, so the exit code is not the answer; the active account's own line
// is. A missing gh and a missing login are reported as a status, not an error.
func (c *Client) AuthStatus(ctx context.Context) (port.AuthStatus, error) {
	st := port.AuthStatus{Host: port.GitHubHost, Scopes: []string{}}

	bin, ok := c.locate(ctx)
	if ctx.Err() != nil {
		return st, ctxError(ctx)
	}
	if !ok {
		return st, nil
	}
	st.Installed, st.Path = true, bin.bin

	res, err := c.run(ctx, runSpec{Bin: bin.bin, Args: authStatusArgs, Env: bin.env, Timeout: c.cfg.Timeout})
	if err != nil {
		return st, errcode.Wrap(errcode.Unknown, "running gh", err)
	}
	if res.Canceled || res.TimedOut {
		return st, interrupted(res)
	}

	// gh writes the report to stdout on success and to stderr on a problem.
	out := string(res.Stdout) + "\n" + string(res.Stderr)
	parsed := parseAuthStatus(out)

	switch {
	case parsed.account != nil:
		a := parsed.account
		st.Account = a.login
		st.Scopes = a.scopes
		switch {
		case a.ok:
			st.Authenticated = true
		case a.unreachable:
			st.Problem = port.AuthProblemUnreachable
		default:
			st.Problem = port.AuthProblemTokenInvalid
		}
		return st, nil
	case parsed.notLoggedIn:
		st.Problem = port.AuthProblemNotLoggedIn
		return st, nil
	}

	// gh's wording for "no account" differs between versions and hosts. With
	// nothing placed for github.com and a failing exit, its own hint to run
	// `gh auth login` is the stable part: that is a missing login, not a failed
	// check.
	if res.ExitCode != 0 && strings.Contains(strings.ToLower(out), "gh auth login") {
		st.Problem = port.AuthProblemNotLoggedIn
		return st, nil
	}

	if res.ExitCode == 0 {
		return st, errcode.New(errcode.Unknown, "gh auth status printed nothing recognizable").
			WithDetail(tail(out))
	}
	return st, errcode.New(errcode.Unknown, "gh auth status failed").WithDetail(tail(out))
}

func ctxError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errcode.Wrap(errcode.Timeout, "the gh call exceeded its time limit", ctx.Err())
	}
	return errcode.Wrap(errcode.Canceled, "the gh call was canceled", ctx.Err())
}

// accountStatus is one account block of the report.
type accountStatus struct {
	host  string
	login string
	ok    bool
	// unreachable is true when gh could not reach GitHub to check the token.
	unreachable bool
	active      bool
	// activeKnown is true when gh printed an "Active account" line for it.
	activeKnown bool
	scopes      []string
}

// authReport is what parseAuthStatus found.
type authReport struct {
	// account is the account that counts: the one marked active, else the
	// first. nil when gh listed none for the host.
	account     *accountStatus
	notLoggedIn bool
}

var (
	// "✓ Logged in to github.com account octocat (keyring)" (gh >= 2.40) and
	// "✓ Logged in to github.com as octocat (keyring)" (older).
	loggedInRe = regexp.MustCompile(`(?i)\blogged in to (\S+) (?:account|as) (\S+)`)
	// "X Failed to log in to github.com account octocat (keyring)".
	failedRe = regexp.MustCompile(`(?i)\bfailed to log in to (\S+) (?:account|as) (\S+)`)
	// "X Timeout trying to log in to github.com account octocat (keyring)": gh
	// could not reach the API.
	timeoutRe = regexp.MustCompile(`(?i)\btimeout trying to log in to (\S+) (?:account|as) (\S+)`)
	// Older gh: "X github.com: authentication failed" and "X The token in
	// GH_TOKEN is invalid".
	oldFailedRe = regexp.MustCompile(`(?i)^\S?\s*(\S+): authentication failed`)
	tokenBadRe  = regexp.MustCompile(`(?i)\btoken in .+ is invalid\b`)
	activeRe    = regexp.MustCompile(`(?i)\bactive account:\s*(true|false)\b`)
	scopesRe    = regexp.MustCompile(`(?i)\btoken scopes:\s*(.*)$`)
	// "You are not logged into any GitHub hosts." and, when only another host
	// has an account, "You are not logged into any accounts on github.com".
	notLoggedRe  = regexp.MustCompile(`(?i)\bnot logged into any (?:github hosts|accounts on \S+)`)
	hostHeaderRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

// parseAuthStatus reads gh's text report. It keeps only github.com, drops the
// masked token line unread, and never fails: whatever it cannot place stays
// unplaced.
func parseAuthStatus(out string) authReport {
	var (
		rep     authReport
		blocks  []*accountStatus
		cur     *accountStatus
		hostNow string
	)
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A host header is an unindented bare host name.
		if line == trimmed && hostHeaderRe.MatchString(trimmed) && strings.Contains(trimmed, ".") {
			hostNow = strings.ToLower(trimmed)
			cur = nil
			continue
		}
		if notLoggedRe.MatchString(trimmed) {
			rep.notLoggedIn = true
			continue
		}
		if m := loggedInRe.FindStringSubmatch(trimmed); m != nil {
			cur = &accountStatus{host: strings.ToLower(m[1]), login: cleanLogin(m[2]), ok: true}
			blocks = append(blocks, cur)
			continue
		}
		if m := failedRe.FindStringSubmatch(trimmed); m != nil {
			cur = &accountStatus{host: strings.ToLower(m[1]), login: cleanLogin(m[2])}
			blocks = append(blocks, cur)
			continue
		}
		if m := timeoutRe.FindStringSubmatch(trimmed); m != nil {
			cur = &accountStatus{host: strings.ToLower(m[1]), login: cleanLogin(m[2]), unreachable: true}
			blocks = append(blocks, cur)
			continue
		}
		if m := oldFailedRe.FindStringSubmatch(trimmed); m != nil {
			cur = &accountStatus{host: strings.ToLower(m[1])}
			blocks = append(blocks, cur)
			continue
		}
		if tokenBadRe.MatchString(trimmed) {
			if cur == nil {
				cur = &accountStatus{host: hostNow}
				blocks = append(blocks, cur)
			}
			cur.ok = false
			cur.unreachable = false
			continue
		}
		if cur == nil {
			continue
		}
		if m := activeRe.FindStringSubmatch(trimmed); m != nil {
			cur.activeKnown = true
			cur.active = strings.EqualFold(m[1], "true")
			continue
		}
		if m := scopesRe.FindStringSubmatch(trimmed); m != nil {
			cur.scopes = parseScopes(m[1])
		}
	}

	var forHost []*accountStatus
	for _, b := range blocks {
		if b.host == port.GitHubHost || (b.host == "" && hostNow == port.GitHubHost) {
			forHost = append(forHost, b)
		}
	}
	for _, b := range forHost {
		if b.activeKnown && b.active {
			rep.account = b
			break
		}
	}
	if rep.account == nil {
		for _, b := range forHost {
			if !b.activeKnown {
				rep.account = b
				break
			}
		}
	}
	if rep.account == nil && len(forHost) > 0 {
		rep.account = forHost[0]
	}
	if rep.account != nil {
		rep.notLoggedIn = false
	}
	return rep
}

// cleanLogin drops the "(keyring)" style suffix that sticks to a login when gh
// separates them with no space, and bounds the length.
func cleanLogin(s string) string {
	s = strings.TrimRight(s, ".,;:()")
	if i := strings.IndexByte(s, '('); i > 0 {
		s = s[:i]
	}
	if len(s) > maxAccountLen {
		s = s[:maxAccountLen]
	}
	return s
}

// parseScopes splits "'gist', 'read:org', 'repo'" (or the unquoted older form)
// into scope names.
func parseScopes(s string) []string {
	scopes := []string{}
	for _, part := range strings.Split(s, ",") {
		p := strings.Trim(strings.TrimSpace(part), `'"`)
		if p == "" || strings.EqualFold(p, "none") || strings.EqualFold(p, "(none)") {
			continue
		}
		scopes = append(scopes, p)
	}
	return scopes
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	const max = 2048
	if len(s) <= max {
		return s
	}
	cut := len(s) - max
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	return s[cut:]
}
