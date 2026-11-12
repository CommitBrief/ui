// SPDX-License-Identifier: GPL-3.0-or-later

//go:build ignore

// Command sign-checksums is the release signing tool of the self-update
// (UI-0007). It runs only in release CI, and once on the maintainer's machine to
// create the key pair. It is a script (go run), not part of the application:
//
//	go run scripts/sign-checksums.go keygen -private-out update-private.key
//	    Creates an Ed25519 key pair. The private key goes to the file (0600,
//	    never overwritten) to be stored as the UPDATE_ED25519_PRIVATE_KEY
//	    secret and then deleted; the public key line is printed, to be pasted
//	    into internal/adapter/updater/release_key.pub.
//
//	UPDATE_ED25519_PRIVATE_KEY=... go run scripts/sign-checksums.go sign -in checksums.txt -out checksums.txt.sig
//	    Signs the exact bytes of the file. The private key is read from the
//	    environment only (never from an argument, never printed). The signature
//	    is verified against the committed release_key.pub before it is written, so
//	    a key that does not belong to the binary cannot produce a release. During
//	    a key rotation release_key.pub lists the old and the new key and the old
//	    private key signs (docs/RELEASING.md).
//
//	go run scripts/sign-checksums.go verify -in checksums.txt -sig checksums.txt.sig
//	    Verifies a signature with the committed release_key.pub (what the
//	    application will do: a signature by any listed key is accepted).
//
//	go run scripts/sign-checksums.go selftest
//	    Exercises sign and verify with a throw-away key (part of `make check`).
//
// The signing and verifying code is the application's own
// (internal/adapter/updater), so the tool and the updater cannot drift apart.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/CommitBrief/ui/internal/adapter/updater"
)

const privateKeyEnv = "UPDATE_ED25519_PRIVATE_KEY"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "selftest":
		err = selftest()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign-checksums:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sign-checksums keygen -private-out FILE | sign -in FILE -out FILE | verify -in FILE -sig FILE | selftest")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("private-out", "", "file to write the private key to (must not exist)")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("keygen: -private-out is required (the private key is never printed)")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "private key written to %s: store it as the %s secret, then delete the file. Never commit it.\n", *out, privateKeyEnv)
	fmt.Fprintln(os.Stderr, "public key line for internal/adapter/updater/release_key.pub:")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

// privateKeyFromEnv reads the signing key: base64 of the 32-byte seed or of the
// 64-byte Ed25519 private key.
func privateKeyFromEnv() (ed25519.PrivateKey, error) {
	text := strings.TrimSpace(os.Getenv(privateKeyEnv))
	if text == "" {
		return nil, fmt.Errorf("%s is not set", privateKeyEnv)
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("%s is not base64", privateKeyEnv)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	}
	return nil, fmt.Errorf("%s has the wrong length", privateKeyEnv)
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	in := fs.String("in", "", "checksums file to sign")
	out := fs.String("out", "", "signature file to write")
	_ = fs.Parse(args)
	if *in == "" || *out == "" {
		return errors.New("sign: -in and -out are required")
	}
	priv, err := privateKeyFromEnv()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	// Refuse to sign anything the application would not accept.
	if _, err := updater.ParseChecksums(data); err != nil {
		return err
	}
	committed, err := updater.ReleasePublicKeys()
	if err != nil {
		return fmt.Errorf("internal/adapter/updater/release_key.pub: %w (commit the public key first)", err)
	}
	sig := ed25519.Sign(priv, data)
	if err := updater.VerifyChecksumsAny(committed, data, updater.EncodeSignature(sig)); err != nil {
		return errors.New("the private key matches none of the public keys compiled into the application (release_key.pub); refusing to sign")
	}
	return os.WriteFile(*out, updater.EncodeSignature(sig), 0o644)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fs.String("in", "", "checksums file")
	sigFile := fs.String("sig", "", "signature file")
	_ = fs.Parse(args)
	if *in == "" || *sigFile == "" {
		return errors.New("verify: -in and -sig are required")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigFile)
	if err != nil {
		return err
	}
	keys, err := updater.ReleasePublicKeys()
	if err != nil {
		return err
	}
	if err := updater.VerifyChecksumsAny(keys, data, sig); err != nil {
		return err
	}
	fmt.Println("signature ok")
	return nil
}

// selftest signs and verifies with a throw-away key pair, and checks that the
// ways a release can go wrong are all rejected.
func selftest() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte("artefact"))
	checksums := []byte(hex.EncodeToString(sum[:]) + "  commitbrief-ui_0.0.1_linux_amd64.AppImage\n")
	if _, err := updater.ParseChecksums(checksums); err != nil {
		return fmt.Errorf("selftest: the sample checksums are not accepted: %w", err)
	}
	sig := updater.EncodeSignature(ed25519.Sign(priv, checksums))

	if err := updater.VerifyChecksums(pub, checksums, sig); err != nil {
		return fmt.Errorf("selftest: a good signature was rejected: %w", err)
	}
	// A key set (a rotation in progress) accepts a signature by any member, and
	// only by a member.
	if err := updater.VerifyChecksumsAny([]ed25519.PublicKey{otherPub, pub}, checksums, sig); err != nil {
		return fmt.Errorf("selftest: a signature by a key in the set was rejected: %w", err)
	}
	if updater.VerifyChecksumsAny([]ed25519.PublicKey{otherPub}, checksums, sig) == nil {
		return errors.New("selftest: a signature by a key outside the set was accepted")
	}
	for name, run := range map[string]func() error{
		"tampered checksums": func() error { return updater.VerifyChecksums(pub, append([]byte("x"), checksums...), sig) },
		"wrong key":          func() error { return updater.VerifyChecksums(otherPub, checksums, sig) },
		"missing signature":  func() error { return updater.VerifyChecksums(pub, checksums, nil) },
		"garbled signature":  func() error { return updater.VerifyChecksums(pub, checksums, []byte("garbage")) },
	} {
		if run() == nil {
			return fmt.Errorf("selftest: %s was accepted", name)
		}
	}
	fmt.Println("sign-checksums selftest: ok")
	return nil
}
