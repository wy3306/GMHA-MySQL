package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	machinedomain "gmha/internal/domain/machine"
)

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestTrustSignersIncludeCredentialPrivateKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := NewClient()
	signers, err := client.trustSigners(machinedomain.SSHAuth{User: "root", PrivateKey: string(testPrivateKeyPEM(t))})
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 1 {
		t.Fatalf("trustSigners() returned %d signers, want 1 credential signer", len(signers))
	}
}

func TestTrustSignersLoadPrivateKeyMatchingManagerPublicKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	privatePath := filepath.Join(home, "manager_ed25519")
	if err := os.WriteFile(privatePath, testPrivateKeyPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(privatePath + ".pub")
	signers, err := client.trustSigners(machinedomain.SSHAuth{User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 1 {
		t.Fatalf("trustSigners() returned %d signers, want 1 Manager signer", len(signers))
	}
}
