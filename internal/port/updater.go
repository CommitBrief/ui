// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// Updater is the application's own self-update (UI-0007). An artefact is
// installed only when it hashes to its line in the release's checksums.txt (a
// missing file, a missing line or a different hash rejects it before anything is
// unpacked). That protects against a corrupt download, not against someone who
// can write to the release; the risk is accepted (UI-0007 section Update
// 2026-11-12, phase 14). The OS signature of the new bundle is not verified on this
// path (it only matters at the first install), so the hash is the only check. A rejected update leaves the running version untouched.
//
// Every error is an *update.Error.
type Updater interface {
	// Current is the running version.
	Current() string
	// Delegation says why this installation does not update itself; empty when
	// it does. Check and Install fail with update.CodeDelegated otherwise.
	Delegation() update.Delegation
	// Status is the state right now.
	Status() update.Status
	// Check looks for a release on the channel that is newer than the running
	// version. It returns nil when the application is up to date.
	Check(ctx context.Context, channel update.Channel) (*update.Release, error)
	// Forget drops the update the last Check found, so Install has nothing to
	// install until the next Check. The application calls it when the update
	// channel changes: a release found on the old channel must not be installable
	// on the new one. It does nothing while an install is under way.
	Forget()
	// Install downloads the release the last Check found, verifies it, puts it
	// in place and restarts into it. It returns once the restart has been
	// handed over; on failure the running version is untouched.
	Install(ctx context.Context) error
}
