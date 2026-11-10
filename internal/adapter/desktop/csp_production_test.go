// SPDX-License-Identifier: GPL-3.0-or-later

//go:build production

package desktop

import "testing"

// Run with: go test -tags production ./internal/adapter/desktop
func TestResponseCSPIgnoresTheEnvironmentInProduction(t *testing.T) {
	t.Setenv("FRONTEND_DEVSERVER_URL", "http://127.0.0.1:9245")
	if got := responseCSP(testLogger); got != contentSecurityPolicy {
		t.Errorf("production builds must always send the CSP, got %q", got)
	}
}
