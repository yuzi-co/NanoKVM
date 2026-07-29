package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// A device with no account file falls back to this on every login attempt, so
// deriving it must not cost a bcrypt hash each time. Reusing one hash is what
// makes that observable.
func TestDefaultAccountHashIsComputedOnce(t *testing.T) {
	first := getDefaultAccount()
	second := getDefaultAccount()

	if first.Password != second.Password {
		t.Fatal("default account hash is recomputed on every call")
	}
}

func TestDefaultAccountStillAcceptsTheDefaultPassword(t *testing.T) {
	account := getDefaultAccount()

	if account.Username != "admin" {
		t.Fatalf("default username = %q, want %q", account.Username, "admin")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte("admin")); err != nil {
		t.Fatalf("default password no longer verifies: %s", err)
	}
}
