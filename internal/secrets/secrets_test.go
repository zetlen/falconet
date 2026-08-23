package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"testing/quick"

	"golang.org/x/crypto/nacl/box"
)

const maxCount = 2000

// The fake API's key: 32 bytes, 1 through 32, which is what a test of the
// verb seals to. The shape holds for any key, which the property below says.
func fakeKey() []byte {
	key := make([]byte, KeyLen)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func TestSealedLengthIsOverheadPlusPlaintext(t *testing.T) {
	for _, plaintext := range []string{"", "x", "sk-ant-test", strings.Repeat("a", 10000)} {
		encoded, err := Seal(fakeKey(), []byte(plaintext))
		if err != nil {
			t.Fatalf("%d bytes: %v", len(plaintext), err)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("%d bytes: not base64: %v", len(plaintext), err)
		}
		if len(raw) != Overhead+len(plaintext) {
			t.Errorf("%d bytes: sealed to %d, want %d", len(plaintext), len(raw), Overhead+len(plaintext))
		}
	}
	if Overhead != 48 {
		t.Errorf("the overhead is %d, and the sealed-box format says 32 + 16", Overhead)
	}
}

func TestAWrongLengthKeyIsRefused(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		if _, err := Seal(make([]byte, n), []byte("x")); err == nil {
			t.Errorf("a %d-byte key was accepted", n)
		} else if !strings.Contains(err.Error(), "32") {
			t.Errorf("a %d-byte key's refusal does not say what a key is: %v", n, err)
		}
	}
}

func TestDecodeKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(fakeKey())
	key, err := DecodeKey(good)
	if err != nil || !bytes.Equal(key, fakeKey()) {
		t.Errorf("a good key: %v %v", key, err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := DecodeKey(bad); err == nil {
			t.Errorf("DecodeKey(%q) accepted", bad)
		}
	}
}

// For any key and any plaintext, the sealed value is base64 of exactly
// Overhead + len(plaintext) bytes, and — the one thing the fake cannot check
// — the holder of the private key opens it to the same bytes.
func TestAnyPlaintextSealsAndOpens(t *testing.T) {
	f := func(plaintext []byte) bool {
		pub, priv, err := box.GenerateKey(rand.Reader)
		if err != nil {
			return false
		}
		encoded, err := Seal(pub[:], plaintext)
		if err != nil {
			return false
		}
		sealed, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(sealed) != Overhead+len(plaintext) {
			return false
		}
		opened, ok := box.OpenAnonymous(nil, sealed, pub, priv)
		return ok && bytes.Equal(opened, plaintext)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// Two seals of one value differ: the ephemeral key is fresh each time, so a
// recorded request says nothing about the next.
func TestSealingIsNotDeterministic(t *testing.T) {
	a, _ := Seal(fakeKey(), []byte("same"))
	b, _ := Seal(fakeKey(), []byte("same"))
	if a == b {
		t.Error("two seals of one value are identical")
	}
}
