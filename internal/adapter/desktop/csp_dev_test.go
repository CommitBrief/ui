// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !production

package desktop

import "testing"

func TestResponseCSPIsDisabledOnlyUnderTheDevServer(t *testing.T) {
	t.Setenv(devServerEnv, "")
	if got := responseCSP(testLogger); got != contentSecurityPolicy {
		t.Errorf("without a dev server the CSP must be sent, got %q", got)
	}

	t.Setenv(devServerEnv, "http://127.0.0.1:9245")
	if got := responseCSP(testLogger); got != "" {
		t.Errorf("with a dev server the header must be omitted, got %q", got)
	}
}
