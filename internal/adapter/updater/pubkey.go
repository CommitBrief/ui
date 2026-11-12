// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"crypto/ed25519"
	_ "embed" // release_key.pub
)

// releaseKeyFile is the embedded release public key (UI-0007 section 2). The
// private half lives only in the release CI secret UPDATE_ED25519_PRIVATE_KEY.
//
//go:embed release_key.pub
var releaseKeyFile []byte

// ReleasePublicKeys returns the compiled-in release public keys, or ErrKeyMissing
// while release_key.pub holds none. A rotation lists the old and the new key for
// one release, signed with the old one (docs/RELEASING.md).
func ReleasePublicKeys() ([]ed25519.PublicKey, error) {
	return ParsePublicKeys(releaseKeyFile)
}
