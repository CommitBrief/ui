// SPDX-License-Identifier: GPL-3.0-or-later

// Package gitinfo reads facts about a local git checkout straight from the
// files under .git, without running git (so it needs no os/exec exception).
// It answers two questions for the repository screen: is this directory the
// root of a work tree, and which owner/name does its "origin" remote point at.
package gitinfo

import (
	"net/url"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

// ParseRemoteURL extracts host, owner and repository name from a git remote
// URL in any of the usual shapes:
//
//	git@github.com:owner/repo.git            (scp-like SSH)
//	ssh://git@github.com[:22]/owner/repo.git
//	https://[user[:token]@]github.com/owner/repo[.git][/]
//	git://github.com/owner/repo.git
//
// Local paths, file:// URLs and paths with more or fewer than two segments
// (for example GitLab subgroups) yield repo_remote_unsupported. The error never
// repeats the URL: it may carry a token.
func ParseRemoteURL(raw string) (port.RemoteRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return port.RemoteRef{}, repo.New(repo.CodeNoRemote)
	}
	host, p, ok := splitRemote(raw)
	if !ok {
		return port.RemoteRef{}, repo.New(repo.CodeRemoteUnsupported)
	}
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	segs := strings.Split(p, "/")
	if host == "" || len(segs) != 2 {
		return port.RemoteRef{}, repo.New(repo.CodeRemoteUnsupported)
	}
	ref := port.RemoteRef{Host: strings.ToLower(host), Owner: segs[0], Name: segs[1]}
	if repo.ValidateOwner(ref.Owner) != nil || repo.ValidateName(ref.Name) != nil {
		return port.RemoteRef{}, repo.New(repo.CodeRemoteUnsupported)
	}
	return ref, nil
}

// splitRemote returns the host and the path part of a remote URL.
func splitRemote(raw string) (host, path string, ok bool) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
			return u.Hostname(), u.Path, true
		default: // file://, ftp://, ...
			return "", "", false
		}
	}
	// scp-like: [user@]host:path. A colon that follows a slash belongs to a
	// local path; a single letter before the colon is a Windows drive.
	colon := strings.IndexByte(raw, ':')
	if colon <= 0 || strings.ContainsAny(raw[:colon], `/\`) {
		return "", "", false
	}
	hostPart := raw[:colon]
	if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if len(hostPart) == 1 { // "C:..." drive letter
		return "", "", false
	}
	return hostPart, raw[colon+1:], true
}
