package version

import "testing"

// withBuild sets the link-time stamp for one test and restores it after.
func withBuild(t *testing.T, stamp string) {
	t.Helper()

	original := Build
	t.Cleanup(func() {
		Build = original
	})

	Build = stamp
}

// A release build carries no stamp, and its reported version has to stay
// exactly what the updater wrote to /kvmapp/version.
func TestDecorateLeavesTheVersionAloneWithoutAStamp(t *testing.T) {
	withBuild(t, "")

	if got := Decorate("2.4.3"); got != "2.4.3" {
		t.Fatalf("Decorate(%q) = %q, want it unchanged", "2.4.3", got)
	}
}

func TestDecorateAppendsTheStampAsBuildMetadata(t *testing.T) {
	withBuild(t, "dev.20260729")

	want := "2.4.3+dev.20260729"
	if got := Decorate("2.4.3"); got != want {
		t.Fatalf("Decorate(%q) = %q, want %q", "2.4.3", got, want)
	}
}

// Semver allows only one '+'. A base that already carries metadata has to be
// extended with a dot, not given a second separator.
func TestDecorateExtendsExistingBuildMetadata(t *testing.T) {
	withBuild(t, "dev")

	want := "2.4.3+abc.dev"
	if got := Decorate("2.4.3+abc"); got != want {
		t.Fatalf("Decorate(%q) = %q, want %q", "2.4.3+abc", got, want)
	}
}

// Build metadata is limited to alphanumerics, hyphens and dots. The stamp comes
// from a build command, so anything else has to be folded down rather than
// producing a version the web UI's semver parser rejects.
func TestDecorateSanitisesCharactersSemverDoesNotAllow(t *testing.T) {
	withBuild(t, "dev/branch name_1")

	want := "2.4.3+dev-branch-name-1"
	if got := Decorate("2.4.3"); got != want {
		t.Fatalf("Decorate(%q) = %q, want %q", "2.4.3", got, want)
	}
}

// A stamp that sanitises down to nothing must not leave a trailing '+', which
// is not a valid version.
func TestDecorateIgnoresAStampWithNothingUsableInIt(t *testing.T) {
	withBuild(t, "///")

	if got := Decorate("2.4.3"); got != "2.4.3" {
		t.Fatalf("Decorate(%q) = %q, want it unchanged", "2.4.3", got)
	}
}

// The stamp must never change how the version orders against the release feed,
// or the update page would advertise an upgrade forever. Build metadata is
// ignored in semver precedence, so the prefix up to '+' has to be untouched.
func TestDecorateKeepsThePrecedenceFieldsIntact(t *testing.T) {
	withBuild(t, "dev.20260729")

	got := Decorate("2.4.3")
	for i := range got {
		if got[i] == '+' {
			if got[:i] != "2.4.3" {
				t.Fatalf("precedence fields changed: %q", got[:i])
			}
			return
		}
	}

	t.Fatalf("Decorate(%q) = %q, expected build metadata", "2.4.3", got)
}
