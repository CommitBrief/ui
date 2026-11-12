// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestUpdateParsePublicKeys(t *testing.T) {
	pub, _ := newKey(t)
	pub2, _ := newKey(t)
	line, line2 := base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(pub2)

	got, err := ParsePublicKeys([]byte("# comment\n\n" + line + "\n# trailing\n"))
	if err != nil || len(got) != 1 || !got[0].Equal(pub) {
		t.Fatalf("a key with comments: %v, %v", got, err)
	}
	// A rotation lists two keys.
	got, err = ParsePublicKeys([]byte(line + "\n" + line2 + "\n"))
	if err != nil || len(got) != 2 || !got[0].Equal(pub) || !got[1].Equal(pub2) {
		t.Fatalf("two keys: %v, %v", got, err)
	}

	if _, err := ParsePublicKeys([]byte("# only a comment\n\n")); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("a file with no key line: %v, want ErrKeyMissing", err)
	}
	if _, err := ParsePublicKeys(nil); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("an empty file: %v, want ErrKeyMissing", err)
	}
	for name, in := range map[string]string{
		"not base64":   "!!!not-base64!!!",
		"wrong length": base64.StdEncoding.EncodeToString(pub[:16]),
		"one bad line": line + "\nnot-a-key\n",
		"too many":     strings.Repeat(line+"\n", maxPublicKeys+1),
	} {
		_, err := ParsePublicKeys([]byte(in))
		if err == nil || errors.Is(err, ErrKeyMissing) {
			t.Errorf("%s: %v, want a parse error that is not ErrKeyMissing", name, err)
		}
	}
}

func TestUpdateEmbeddedKeyFileIsEmptyOrValid(t *testing.T) {
	// The repository ships release_key.pub without a key until the maintainer
	// generates the pair (the private half is a secret), and a build from it then
	// refuses to update. Once a key is committed it must parse: a malformed file
	// is neither "no key" nor a usable one.
	keys, err := ReleasePublicKeys()
	switch {
	case errors.Is(err, ErrKeyMissing):
		if len(keys) != 0 {
			t.Errorf("ErrKeyMissing with %d keys", len(keys))
		}
	case err != nil:
		t.Fatalf("release_key.pub is malformed: %v", err)
	default:
		if len(keys) == 0 {
			t.Fatal("no error and no key")
		}
		for _, k := range keys {
			if len(k) != ed25519.PublicKeySize {
				t.Errorf("key of %d bytes", len(k))
			}
		}
	}
}

func TestVerifyChecksumsAny(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, _ := newKey(t)
	msg := []byte("abc  file\n")
	sig := EncodeSignature(ed25519.Sign(priv, msg))
	if err := VerifyChecksumsAny([]ed25519.PublicKey{otherPub, pub}, msg, sig); err != nil {
		t.Errorf("a member of the set: %v", err)
	}
	if err := VerifyChecksumsAny([]ed25519.PublicKey{otherPub}, msg, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("no member matches: %v", err)
	}
	if err := VerifyChecksumsAny(nil, msg, sig); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("an empty set: %v", err)
	}
	if err := VerifyChecksumsAny([]ed25519.PublicKey{pub}, msg, nil); !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("no signature: %v", err)
	}
}

func TestVerifyChecksums(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, otherPriv := newKey(t)
	msg := []byte("abc  file\n")
	good := EncodeSignature(ed25519.Sign(priv, msg))

	if err := VerifyChecksums(pub, msg, good); err != nil {
		t.Fatalf("a valid signature: %v", err)
	}
	// The format is one base64 line, whitespace around it is ignored.
	if err := VerifyChecksums(pub, msg, []byte("  "+strings.TrimSpace(string(good))+"\r\n\n")); err != nil {
		t.Errorf("a padded signature: %v", err)
	}

	tests := []struct {
		name string
		pub  ed25519.PublicKey
		msg  []byte
		sig  []byte
		want error
	}{
		{"no signature", pub, msg, nil, ErrSignatureMissing},
		{"blank signature", pub, msg, []byte(" \n"), ErrSignatureMissing},
		{"not base64", pub, msg, []byte("%%%"), ErrSignatureInvalid},
		{"short signature", pub, msg, []byte(base64.StdEncoding.EncodeToString([]byte("short"))), ErrSignatureInvalid},
		{"long signature", pub, msg, []byte(base64.StdEncoding.EncodeToString(make([]byte, 65))), ErrSignatureInvalid},
		{"zero signature", pub, msg, []byte(base64.StdEncoding.EncodeToString(make([]byte, 64))), ErrSignatureInvalid},
		{"wrong key", otherPub, msg, good, ErrSignatureInvalid},
		{"signed by another key", pub, msg, EncodeSignature(ed25519.Sign(otherPriv, msg)), ErrSignatureInvalid},
		{"tampered message", pub, []byte("abd  file\n"), good, ErrSignatureInvalid},
		{"truncated message", pub, msg[:len(msg)-1], good, ErrSignatureInvalid},
		{"no key", nil, msg, good, ErrKeyMissing},
		{"short key", pub[:8], msg, good, ErrKeyMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyChecksums(tc.pub, tc.msg, tc.sig); !errors.Is(err, tc.want) {
				t.Errorf("VerifyChecksums = %v, want %v", err, tc.want)
			}
		})
	}
}

func hexOf(b byte) string { return strings.Repeat(string("0123456789abcdef"[b%16]), 64) }

func TestUpdateParseChecksums(t *testing.T) {
	sum := sha256.Sum256([]byte("payload"))
	line := hex.EncodeToString(sum[:])

	got, err := ParseChecksums([]byte(line + "  a_1.zip\r\n\n" + hexOf(1) + " *b.AppImage\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a_1.zip"] != sum {
		t.Errorf("parsed %v", got)
	}
	if _, ok := got["b.AppImage"]; !ok {
		t.Error("binary-mode marker not handled")
	}

	bad := map[string]string{
		"empty":                "",
		"only blanks":          "\n\n",
		"short digest":         "abcd  file\n",
		"non hex digest":       strings.Repeat("g", 64) + "  file\n",
		"one space":            line + " file\n",
		"no name":              line + "  \n",
		"path separator":       line + "  dir/file\n",
		"backslash":            line + `  dir\file` + "\n",
		"dot name":             line + "  .hidden\n",
		"parent":               line + "  ..\n",
		"duplicate name":       line + "  f\n" + hexOf(2) + "  f\n",
		"control char in name": line + "  a\x01b\n",
		"space in name":        line + "  a b\n",
		"non ascii name":       line + "  ü\n",
		"too many lines":       strings.Repeat(line+"  f\n", maxChecksumLines+1),
	}
	for name, in := range bad {
		if _, err := ParseChecksums([]byte(in)); !errors.Is(err, ErrChecksumsInvalid) {
			t.Errorf("%s: %v, want ErrChecksumsInvalid", name, err)
		}
	}
}

func TestUpdateDigestEqual(t *testing.T) {
	a := sha256.Sum256([]byte("a"))
	b := sha256.Sum256([]byte("b"))
	if !digestEqual(a, a) || digestEqual(a, b) {
		t.Error("digestEqual")
	}
}
