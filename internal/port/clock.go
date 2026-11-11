// SPDX-License-Identifier: GPL-3.0-or-later

package port

import "time"

// Clock is the application's source of "now". The scheduler and the budget read
// time through it so simulations run on a fake clock. The Location of the
// returned time is the zone "today" is measured in (the budget day starts at
// local midnight there).
type Clock interface {
	Now() time.Time
}
