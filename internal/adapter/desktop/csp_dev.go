// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !production

package desktop

import (
	"log/slog"
	"os"
)

// devServerEnv is set by `wails3 dev` when the frontend is served by Vite.
// Wails itself honours it only in non-production builds; so does this file.
const devServerEnv = "FRONTEND_DEVSERVER_URL"

// responseCSP returns the policy to send as a header. Under `wails3 dev` the
// frontend comes from Vite, which needs inline scripts and a WebSocket, so no
// header is sent. Production builds (-tags production) never reach this file.
func responseCSP(log *slog.Logger) string {
	if os.Getenv(devServerEnv) != "" {
		log.Warn("frontend dev server in use; Content-Security-Policy header disabled")
		return ""
	}
	return contentSecurityPolicy
}
