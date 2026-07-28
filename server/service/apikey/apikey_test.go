package apikey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempKeyStore points the key file at a temporary path for one test.
func withTempKeyStore(t *testing.T) string {
	t.Helper()

	original := File
	path := filepath.Join(t.TempDir(), "api_keys.json")
	File = path
	t.Cleanup(func() { File = original })

	return path
}

func TestCreatedKeyAuthenticates(t *testing.T) {
	withTempKeyStore(t)

	secret, _, err := Create("automation")
	if err != nil {
		t.Fatalf("expected the key to be created: %s", err)
	}

	if !Verify(secret) {
		t.Fatal("expected the created key to authenticate")
	}
}

func TestUnknownKeyIsRejected(t *testing.T) {
	withTempKeyStore(t)

	if _, _, err := Create("automation"); err != nil {
		t.Fatalf("setup: %s", err)
	}

	for _, secret := range []string{"", "nkvm_wrong", "admin"} {
		if Verify(secret) {
			t.Fatalf("expected %q to be rejected", secret)
		}
	}
}

func TestNoKeysMeansNoAccess(t *testing.T) {
	// An empty store must not read as "nothing to check, let them in".
	withTempKeyStore(t)

	if Verify("anything") {
		t.Fatal("expected an empty key store to reject everything")
	}
}

func TestSecretIsNeverStored(t *testing.T) {
	// The file is readable by root, and root is what a compromised update
	// path gets. Storing only a digest means a stolen file cannot be replayed.
	path := withTempKeyStore(t)

	secret, _, err := Create("automation")
	if err != nil {
		t.Fatalf("setup: %s", err)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the key file to exist: %s", err)
	}

	if strings.Contains(string(stored), secret) {
		t.Fatal("the plaintext key was written to disk")
	}
}

func TestKeyFileIsNotReadableByOthers(t *testing.T) {
	path := withTempKeyStore(t)

	if _, _, err := Create("automation"); err != nil {
		t.Fatalf("setup: %s", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected the key file to exist: %s", err)
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("key file mode is %#o, want no access for group or others", mode)
	}
}

func TestEveryKeyIsDifferent(t *testing.T) {
	withTempKeyStore(t)

	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		secret, _, err := Create("automation")
		if err != nil {
			t.Fatalf("expected the key to be created: %s", err)
		}

		if seen[secret] {
			t.Fatal("the same key was issued twice")
		}
		seen[secret] = true
	}
}

func TestRevokedKeyStopsWorking(t *testing.T) {
	withTempKeyStore(t)

	kept, _, err := Create("kept")
	if err != nil {
		t.Fatalf("setup: %s", err)
	}

	revoked, record, err := Create("revoked")
	if err != nil {
		t.Fatalf("setup: %s", err)
	}

	if err := Revoke(record.ID); err != nil {
		t.Fatalf("expected the key to be revoked: %s", err)
	}

	if Verify(revoked) {
		t.Fatal("expected the revoked key to stop working")
	}

	if !Verify(kept) {
		t.Fatal("revoking one key must not disturb the others")
	}
}

func TestListingNeverExposesASecret(t *testing.T) {
	withTempKeyStore(t)

	secret, _, err := Create("automation")
	if err != nil {
		t.Fatalf("setup: %s", err)
	}

	keys, err := List()
	if err != nil {
		t.Fatalf("expected the keys to be listed: %s", err)
	}

	if len(keys) != 1 {
		t.Fatalf("listed %d keys, want 1", len(keys))
	}

	if keys[0].Name != "automation" {
		t.Fatalf("listed name %q, want the name it was created with", keys[0].Name)
	}

	if keys[0].Hash != "" {
		t.Fatal("the stored digest must not be handed back to a client")
	}

	if strings.Contains(keys[0].ID, secret) {
		t.Fatal("the identifier must not embed the secret")
	}
}
