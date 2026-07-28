package picoclaw

import "testing"

func TestSessionFileBaseMapsAPicoSession(t *testing.T) {
	base, ok := sessionFileBase("abc-123")
	if !ok {
		t.Fatal("expected a normal session id to be accepted")
	}

	if base != sanitizedPicoSessionPrefix+"abc-123" {
		t.Fatalf("unexpected base %q", base)
	}
}

func TestSessionFileBaseMapsAnOpaqueSession(t *testing.T) {
	base, ok := sessionFileBase("sk_v1_abcdef")
	if !ok {
		t.Fatal("expected an opaque session key to be accepted")
	}

	if base != "sk_v1_abcdef" {
		t.Fatalf("unexpected base %q", base)
	}
}

func TestSessionFileBaseRejectsAnythingThatLeavesTheDirectory(t *testing.T) {
	// The id decides which file gets read and deleted. Today gin never puts a
	// separator in a path parameter, so this is the belt rather than the
	// braces, but the code should not depend on the router for it.
	for _, id := range []string{
		"sk_v1_../../etc/passwd",
		"sk_v1_/etc/passwd",
		"../../etc/passwd",
		"a/b",
		`a\b`,
		"",
		"sk_v1_",
	} {
		if base, ok := sessionFileBase(id); ok {
			t.Fatalf("expected %q to be rejected, got base %q", id, base)
		}
	}
}

func TestSessionFileBaseKeepsCharactersARuntimeMightUse(t *testing.T) {
	// The runtime, not this server, names these files. Rejecting characters it
	// legitimately uses would make real sessions impossible to open or delete.
	for _, id := range []string{"abc+def", "abc=def", "ABC.123", "a_b-c"} {
		if _, ok := sessionFileBase(id); !ok {
			t.Fatalf("expected %q to be accepted", id)
		}
	}
}
