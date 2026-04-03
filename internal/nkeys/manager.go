package nkeys

import (
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
