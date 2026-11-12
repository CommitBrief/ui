// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

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
