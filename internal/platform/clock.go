// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

// SystemClock is the real port.Clock. Now is in the machine's local zone, so the
// budget day starts at the user's midnight.
type SystemClock struct{}

var _ port.Clock = SystemClock{}

// Now implements port.Clock.
func (SystemClock) Now() time.Time { return time.Now() }
