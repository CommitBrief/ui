// SPDX-License-Identifier: GPL-3.0-or-later

// Package updater is the self-update adapter (UI-0007). An update is accepted
// only through this chain, and a failing or missing link rejects it (the running
// version is left alone, fail-closed):
//
//	release feed (GitHub Releases, https only)  lists  checksums.txt
//	checksums.txt                               lists  the SHA-256 of every artefact
//	artefact                                    must hash to its line in checksums.txt
//
// The hashes come from the same release as the artefact, so the chain protects
// against a corrupt or truncated download, not against someone who can write to
// the release (UI-0007 section Update 2026-11-12, phase 14: accepted by decision).
// The operating-system signature of the new bundle is not checked here either (the
// Wails updater does not require one), so this hash is the only check.
// The package touches the network and the file system (staging) but never the
// Wails runtime; the swap-and-restart step is a Backend the desktop adapter
// provides.
package updater

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Verification failures. Callers map them to update.Code values; the text is
// for logs.
var (
	// ErrChecksumsInvalid: the checksums file is not in the expected format.
	ErrChecksumsInvalid = errors.New("updater: the checksums file is malformed")
	// ErrDigestMismatch: an artefact does not hash to its checksums.txt line.
	ErrDigestMismatch = errors.New("updater: the artefact does not match its checksum")
)

// maxChecksumLines bounds the checksums file (a release lists a handful of
// artefacts).
const maxChecksumLines = 256

// ParseChecksums reads a sha256sum-style file: "<64 hex digits>  <name>" per
// line (a "*" before the name, binary mode, is accepted). A name is a bare file
// name: no path separators. A name listed twice is refused, so no later line can
// quietly replace an earlier one.
func ParseChecksums(data []byte) (map[string][sha256.Size]byte, error) {
	out := make(map[string][sha256.Size]byte)
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxChecksumLines {
		return nil, fmt.Errorf("%w: too many lines", ErrChecksumsInvalid)
	}
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l) < 64+2+1 || (l[64] != ' ') || (l[65] != ' ' && l[65] != '*') {
			return nil, fmt.Errorf("%w: bad line", ErrChecksumsInvalid)
		}
		sum, err := hex.DecodeString(l[:64])
		if err != nil {
			return nil, fmt.Errorf("%w: bad digest", ErrChecksumsInvalid)
		}
		name := l[66:]
		if !validArtefactName(name) {
			return nil, fmt.Errorf("%w: bad file name", ErrChecksumsInvalid)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%w: %q is listed twice", ErrChecksumsInvalid, name)
		}
		var d [sha256.Size]byte
		copy(d[:], sum)
		out[name] = d
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no entries", ErrChecksumsInvalid)
	}
	return out, nil
}

// validArtefactName accepts a plain file name: printable ASCII without
// separators, and not a dot name.
func validArtefactName(n string) bool {
	if n == "" || len(n) > 200 || n[0] == '.' {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x21 || c > 0x7e || c == '/' || c == '\\' {
			return false
		}
	}
	return true
}

// digestEqual compares two digests in constant time.
func digestEqual(a, b [sha256.Size]byte) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
