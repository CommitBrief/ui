// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package gh

import (
	"context"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

// Runs the real gh that is installed on this machine (`go test -tags
// integration ./internal/adapter/github/gh`); not part of CI. gh gets an empty
// config directory and no token variables, so the answer is "not logged in"
// whatever the user's own login is, and the user's gh configuration is not read.
func TestIntegrationAuthStatusWithNoLogin(t *testing.T) {
	home := t.TempDir()
	env := testEnv{
		vars: map[string]string{
			"HOME":          home,
			"USERPROFILE":   home,
			"GH_CONFIG_DIR": home,
			"GH_TOKEN":      "",
			"GITHUB_TOKEN":  "",
			"GH_HOST":       "",
		},
		home: home,
	}
	c := New(Config{Env: env, Probe: func(context.Context) (string, error) { return "", nil }})

	st, err := c.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if !st.Installed {
		t.Skip("no gh on this machine")
	}
	if st.Authenticated || st.Problem != port.AuthProblemNotLoggedIn {
		t.Errorf("status = %+v, want not logged in", st)
	}
}

// With no login, the real gh refuses a search and a view, and the adapter files
// both under gh_not_authenticated (the bot's "needs attention" state), not under
// an unknown failure. gh gets an empty config directory and no token variables.
func TestIntegrationDiscoveryWithNoLogin(t *testing.T) {
	home := t.TempDir()
	env := testEnv{
		vars: map[string]string{
			"HOME": home, "USERPROFILE": home, "GH_CONFIG_DIR": home,
			"GH_TOKEN": "", "GITHUB_TOKEN": "", "GH_HOST": "",
		},
		home: home,
	}
	c := New(Config{Env: env, Probe: func(context.Context) (string, error) { return "", nil }})
	if st, err := c.AuthStatus(context.Background()); err != nil || !st.Installed {
		t.Skip("no gh on this machine")
	}

	_, err := c.SearchReviewRequested(context.Background(), []string{"cli/cli"})
	if !errcode.Is(err, errcode.GHNotAuthenticated) {
		t.Errorf("SearchReviewRequested = %v, want gh_not_authenticated", err)
	}
	_, err = c.ViewPR(context.Background(), "cli/cli", 1)
	if !errcode.Is(err, errcode.GHNotAuthenticated) {
		t.Errorf("ViewPR = %v, want gh_not_authenticated", err)
	}
}
