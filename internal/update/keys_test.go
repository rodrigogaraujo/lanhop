package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func TestTheCompiledReleaseKeysAreTheOnesPublishedInTheRepository(t *testing.T) {
	published := []string{"../../packaging/release-keys/current.pem", "../../packaging/release-keys/next.pem"}
	keys := ProjectKeys()
	if len(keys) != len(published) {
		t.Fatalf("lanhop carries %d release keys, want the current one and the next one", len(keys))
	}
	for i, file := range published {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		block, rest := pem.Decode(content)
		if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
			t.Fatalf("%s holds no single PEM public key", file)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			t.Fatalf("%s is not an Ed25519 key", file)
		}
		if !key.Equal(keys[i]) {
			t.Errorf("%s is not the release key number %d that lanhop carries", file, i+1)
		}
	}
	if keys[0].Equal(keys[1]) {
		t.Error("the next release key is the current one")
	}
}

func TestProjectKeysCannotBeChangedFromOutside(t *testing.T) {
	keys := ProjectKeys()
	keys[0][0] ^= 0xff
	keys[1] = make(ed25519.PublicKey, ed25519.PublicKeySize)
	if again := ProjectKeys(); again[0].Equal(keys[0]) || again[1].Equal(keys[1]) {
		t.Error("changing what ProjectKeys returned changed the keys of lanhop")
	}
}
