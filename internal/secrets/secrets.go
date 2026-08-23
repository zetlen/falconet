// Package secrets is the sealed box a repository secret travels in.
//
// GitHub's secrets API accepts a value only as a libsodium "sealed box"
// (crypto_box_seal) against the repository's public key: an ephemeral
// X25519 key pair is generated, the plaintext is encrypted with XSalsa20 and
// authenticated with Poly1305 under the shared secret of the ephemeral
// private key and the repository key, and what travels is the ephemeral
// PUBLIC key (32 bytes) followed by the box (16 bytes of MAC plus the
// plaintext). The nonce is a BLAKE2b hash of the two public keys, so the
// receiver needs nothing it does not already have, and the sender keeps
// nothing: the ephemeral private key is discarded, and this process could
// not open the box it just sealed.
//
// That is golang.org/x/crypto/nacl/box.SealAnonymous — ADR-0006 D1's ONE
// dependency outside the standard library, named there because the program
// cannot avoid it: the standard library has X25519 and no XSalsa20, and
// neither Node's nor Bun's crypto had the primitives either. It is
// maintained by the Go team, and it is what `gh secret set` uses.
//
// Nothing here touches the network or the filesystem: the caller hands in
// the key's bytes and the plaintext and gets back the base64 the API wants.
// Interoperability with libsodium — that GitHub can open what this seals —
// is the thing ADR-0006 lists as unverified until the first `init` stores a
// secret a run then uses: the fake API in the tests cannot open the box, so
// the tests hold the shape (the length, the key check) and the canary holds
// the rest.
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// KeyLen is the only length a repository public key can have: a Curve25519
// point. Any other length is not a key this can seal to, and is refused
// rather than passed along — box would reject it, but the reason should be
// in the error, not the stack trace.
const KeyLen = 32

// Overhead is what sealing adds to the plaintext: the ephemeral public key
// and the Poly1305 MAC. The sealed value is Overhead + len(plaintext) bytes,
// before base64.
const Overhead = box.AnonymousOverhead

// DecodeKey reads the `key` field of GET …/actions/secrets/public-key: the
// repository's public key, base64 as GitHub encodes it.
func DecodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("the repository's public key is not base64: %v", err)
	}
	if len(key) != KeyLen {
		return nil, fmt.Errorf("the repository's public key is %d bytes, not %d", len(key), KeyLen)
	}
	return key, nil
}

// Seal encrypts plaintext to publicKey as an anonymous sealed box and returns
// it base64-encoded: the `encrypted_value` of PUT …/actions/secrets/{name}.
// Randomness is crypto/rand; a sealed box is different every time, which is
// why the tests assert on the length and not on the bytes.
func Seal(publicKey, plaintext []byte) (string, error) {
	if len(publicKey) != KeyLen {
		return "", fmt.Errorf("cannot seal to a %d-byte key; a repository public key is %d bytes", len(publicKey), KeyLen)
	}
	var key [KeyLen]byte
	copy(key[:], publicKey)
	sealed, err := box.SealAnonymous(nil, plaintext, &key, rand.Reader)
	if err != nil {
		return "", fmt.Errorf("sealing the value: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
