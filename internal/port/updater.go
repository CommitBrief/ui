// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// Updater is the application's own self-update (UI-0007). The release feed it
// reads is authenticated before anything is downloaded: a release whose
// checksums are not signed by the pinned release key is rejected, and so is an
// artefact that does not hash to a signed checksum. A rejected update leaves
// the running version untouched.
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
