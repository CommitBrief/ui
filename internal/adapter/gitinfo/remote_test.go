// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import (
	"errors"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

func TestParseRemoteURL(t *testing.T) {
	gh := func(owner, name string) port.RemoteRef {
		return port.RemoteRef{Host: "github.com", Owner: owner, Name: name}
	}
	tests := []struct {
		name string
		in   string
		want port.RemoteRef
	}{
		{"scp ssh with .git", "git@github.com:CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"scp ssh without .git", "git@github.com:CommitBrief/ui", gh("CommitBrief", "ui")},
		{"scp without user", "github.com:CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"ssh url", "ssh://git@github.com/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"ssh url with port", "ssh://git@github.com:22/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"https with .git", "https://github.com/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"https without .git", "https://github.com/CommitBrief/ui", gh("CommitBrief", "ui")},
		{"https trailing slash", "https://github.com/CommitBrief/ui/", gh("CommitBrief", "ui")},
		{"https with token", "https://x-access-token:ghp_secret@github.com/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"git protocol", "git://github.com/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"upper-case host", "https://GitHub.com/CommitBrief/ui.git", gh("CommitBrief", "ui")},
		{"dots in name", "git@github.com:muhammetsafak/commitbrief.com.git", gh("muhammetsafak", "commitbrief.com")},
		{"enterprise host", "git@git.example.com:team/svc.git", port.RemoteRef{Host: "git.example.com", Owner: "team", Name: "svc"}},
		{"surrounding spaces", "  git@github.com:a/b.git \n", gh("a", "b")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tc.in)
			if err != nil {
				t.Fatalf("ParseRemoteURL(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRemoteURL(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRemoteURLRejects(t *testing.T) {
	unsupported := []string{
		"/srv/git/repo.git",
		"../repo",
		"file:///srv/git/repo.git",
		`C:\Users\me\repo`,
		"C:/Users/me/repo",
		"git@gitlab.com:group/sub/repo.git", // subgroup: three segments
		"https://github.com/onlyowner",
		"https://github.com/o/r/extra",
		"https://github.com//",
		"git@github.com:ow ner/repo.git",
		"git@github.com:owner/re$po.git",
		"ftp://github.com/o/r.git",
		"https://[::1",
		"nonsense",
	}
	for _, in := range unsupported {
		_, err := ParseRemoteURL(in)
		var e *repo.Error
		if !errors.As(err, &e) || e.Code != repo.CodeRemoteUnsupported {
			t.Errorf("ParseRemoteURL(%q) = %v, want repo_remote_unsupported", in, err)
		}
	}
	_, err := ParseRemoteURL("  ")
	var e *repo.Error
	if !errors.As(err, &e) || e.Code != repo.CodeNoRemote {
		t.Errorf("empty URL = %v, want repo_no_remote", err)
	}
}

func TestParseRemoteURLErrorDoesNotLeakCredentials(t *testing.T) {
	_, err := ParseRemoteURL("https://user:ghp_topsecret@github.com/a/b/c.git")
	if err == nil || strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("expected an error without the token, got %v", err)
	}
}
