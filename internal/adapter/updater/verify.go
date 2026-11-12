// SPDX-License-Identifier: GPL-3.0-or-later

// Package updater is the self-update adapter (UI-0007). A release is trusted
// only through a chain that starts at a public key compiled into the binary:
//
//	release key (private, CI secret only)  signs  checksums.txt
//	checksums.txt                          lists  the SHA-256 of every artefact
//	artefact                               must hash to its line in checksums.txt
//
// Every link is checked before the next one is used, and a missing or failing
// link rejects the update: the running version is left alone (fail-closed).
// The package touches the network (GitHub Releases) and the file system
// (staging) but never the Wails runtime; the swap-and-restart step is a Backend
// the desktop adapter provides.
package updater

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Verification failures. Callers map them to update.Code values; the text is
// for logs.
var (
	// ErrKeyMissing: no usable public key is compiled in.
	ErrKeyMissing = errors.New("updater: no release public key is configured")
	// ErrSignatureMissing: the signature is absent or empty.
	ErrSignatureMissing = errors.New("updater: the checksums have no signature")
	// ErrSignatureInvalid: the signature is malformed or does not verify.
	ErrSignatureInvalid = errors.New("updater: the checksums signature does not verify")
	// ErrChecksumsInvalid: the checksums file is not in the expected format.
	ErrChecksumsInvalid = errors.New("updater: the checksums file is malformed")
	// ErrDigestMismatch: an artefact does not hash to its signed checksum.
	ErrDigestMismatch = errors.New("updater: the artefact does not match its signed checksum")
)

// SignatureSize is the length of an Ed25519 signature.
const SignatureSize = ed25519.SignatureSize

// maxChecksumLines bounds the checksums file (a release lists a handful of
// artefacts).
const maxChecksumLines = 256

// maxPublicKeys bounds the key set: the current key and, during a rotation, the
// one it replaces.
const maxPublicKeys = 4

// ParsePublicKeys reads the release public keys: one line of standard base64 per
// key, each decoding to 32 bytes. Blank lines and lines starting with "#" are
// skipped. More than one key is a rotation in progress (docs/RELEASING.md): a
// signature by any of them is accepted. A file with no key line at all is
// ErrKeyMissing (the repository ships it that way until the maintainer generates
// the key pair); anything else that is not a key is an error of its own.
func ParsePublicKeys(text []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, l := range strings.Split(string(text), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(l)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, errors.New("updater: a public key line is not base64 of 32 bytes")
		}
		if len(keys) == maxPublicKeys {
			return nil, fmt.Errorf("updater: more than %d public keys", maxPublicKeys)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	if len(keys) == 0 {
		return nil, ErrKeyMissing
	}
	return keys, nil
}

// EncodeSignature renders a signature the way checksums.txt.sig stores it:
// standard base64 on one line.
func EncodeSignature(sig []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// VerifyChecksums checks that sig is a signature of exactly the bytes of
// checksums made by pub. The message is the file as published, not a parsed
// form of it, so there is nothing for a second parser to disagree about.
func VerifyChecksums(pub ed25519.PublicKey, checksums, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrKeyMissing
	}
	text := strings.TrimSpace(string(sig))
	if text == "" {
		return ErrSignatureMissing
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(raw) != SignatureSize {
		return ErrSignatureInvalid
	}
	if !ed25519.Verify(pub, checksums, raw) {
		return ErrSignatureInvalid
	}
	return nil
}

// VerifyChecksumsAny accepts a signature made by any of keys. With no key it is
// ErrKeyMissing; otherwise the error is that of the last key tried, so a
// signature that matches none is ErrSignatureInvalid (or ErrSignatureMissing for
// an absent one).
func VerifyChecksumsAny(keys []ed25519.PublicKey, checksums, sig []byte) error {
	err := ErrKeyMissing
	for _, k := range keys {
		if err = VerifyChecksums(k, checksums, sig); err == nil {
			return nil
		}
	}
	return err
}

// ParseChecksums reads a sha256sum-style file: "<64 hex digits>  <name>" per
// line (a "*" before the name, binary mode, is accepted). A name is a bare file
// name: no path separators. A name listed twice is refused, so no later line can
// quietly replace the one the signature covered.
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
