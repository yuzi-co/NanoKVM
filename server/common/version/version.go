// Package version carries the build stamp reported alongside the application
// version.
//
// The application version itself lives in /kvmapp/version and is written by the
// updater, so it says nothing about which server binary is running. A binary
// dropped onto a device by hand is otherwise indistinguishable from the one it
// replaced.
package version

import "strings"

// Build is the stamp for this binary. It is empty in a release build and is set
// at link time for anything else:
//
//	go build -ldflags "-X NanoKVM-Server/common/version.Build=dev.20260729"
var Build string

// Decorate returns the version string to report for base.
//
// The stamp is attached as semver build metadata, which is ignored when
// versions are compared. The web UI decides whether an update is available with
// semver.gte(current, latest); a prerelease suffix would sort below the release
// it was built from and leave that page advertising an upgrade forever.
func Decorate(base string) string {
	stamp := sanitize(Build)
	if stamp == "" {
		return base
	}

	// Semver permits a single '+', so a base that already carries metadata is
	// extended rather than given a second separator.
	if strings.Contains(base, "+") {
		return base + "." + stamp
	}

	return base + "+" + stamp
}

// sanitize folds a stamp down to the alphabet semver allows in build metadata:
// alphanumerics, hyphens and dots. Runs of rejected characters collapse into a
// single hyphen, and a stamp with nothing usable in it yields "" so Decorate
// leaves the version alone instead of emitting a trailing '+'.
func sanitize(stamp string) string {
	var b strings.Builder

	for _, r := range stamp {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	return strings.Trim(b.String(), "-.")
}
