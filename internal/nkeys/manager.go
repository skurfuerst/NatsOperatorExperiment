package nkeys

import (
	"crypto/rand"
	"encoding/base32"

	"github.com/nats-io/nkeys"
)

// GenerateUserNKey creates a new NATS user NKey pair.
// Returns the public key (starts with 'U'), the seed (starts with 'SU'), and any error.
func GenerateUserNKey() (publicKey string, seed []byte, err error) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		return "", nil, err
	}

	publicKey, err = kp.PublicKey()
	if err != nil {
		return "", nil, err
	}

	seed, err = kp.Seed()
	if err != nil {
		return "", nil, err
	}

	return publicKey, seed, nil
}

// GenerateInboxPrefix creates a random inbox prefix suitable for NATS subject use.
// Returns a string of the form "_I_<16 uppercase base32 chars>" (no trailing dot).
// The caller is responsible for appending ".>" in subscribe permissions.
func GenerateInboxPrefix() (string, error) {
	b := make([]byte, 10) // 10 bytes → 16 base32 chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "_I_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}
