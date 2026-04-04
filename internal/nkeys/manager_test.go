package nkeys

import (
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func TestGenerateUserNKey(t *testing.T) {
	publicKey, seed, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if publicKey == "" {
		t.Error("public key should not be empty")
	}
	if len(seed) == 0 {
		t.Error("seed should not be empty")
	}
}

func TestGenerateUserNKeyPublicKeyPrefix(t *testing.T) {
	publicKey, _, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(publicKey, "U") {
		t.Errorf("user public key should start with 'U', got %q", publicKey)
	}
}

func TestGenerateUserNKeySeedReconstructsPublicKey(t *testing.T) {
	publicKey, seed, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reconstruct from seed
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("failed to reconstruct from seed: %v", err)
	}
	reconstructed, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("failed to get public key from reconstructed keypair: %v", err)
	}
	if reconstructed != publicKey {
		t.Errorf("reconstructed key %q != original %q", reconstructed, publicKey)
	}
}

func TestGenerateInboxPrefix(t *testing.T) {
	prefix, err := GenerateInboxPrefix()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(prefix, "_I_") {
		t.Errorf("inbox prefix should start with _I_, got %q", prefix)
	}
	// Must not contain dots, wildcards, or spaces (NATS subject token rules)
	for _, ch := range prefix {
		if ch == '.' || ch == '>' || ch == '*' || ch == ' ' {
			t.Errorf("inbox prefix contains invalid character %q: %q", string(ch), prefix)
		}
	}
}

func TestGenerateInboxPrefixUniqueness(t *testing.T) {
	p1, _ := GenerateInboxPrefix()
	p2, _ := GenerateInboxPrefix()
	if p1 == p2 {
		t.Error("two generated inbox prefixes should be unique")
	}
}

func TestGenerateUserNKeyUniqueness(t *testing.T) {
	key1, _, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key2, _, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key1 == key2 {
		t.Error("two generated keys should be unique")
	}
}
