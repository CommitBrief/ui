// SPDX-License-Identifier: GPL-3.0-or-later

//go:build production

package desktop

import "log/slog"

// responseCSP returns the policy to send as a header. In production builds it
// is unconditional: nothing in the process environment can switch it off.
func responseCSP(*slog.Logger) string {
	return contentSecurityPolicy
}
