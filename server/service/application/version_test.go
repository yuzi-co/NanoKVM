package application

import (
	"encoding/base64"
	"fmt"
	"testing"
)

const baseURL = "https://cdn.example.com/nanokvm"

// validPackageName satisfies the pattern validateLatest enforces. The pattern is
// stricter than "a plain file name" - it pins the whole shape - so a fixture
// that only avoided path separators would now be rejected for the wrong reason
// and would prove nothing about the case under test.
const validPackageName = "nanokvm_2.0.0.tar.gz"

// validSha512 is a well-formed digest rather than a real one: validateLatest
// checks that it decodes to 64 bytes and nothing here checks the contents.
var validSha512 = base64.StdEncoding.EncodeToString(make([]byte, 64))

func latestJSON(name string) []byte {
	return []byte(fmt.Sprintf(
		`{"version":"2.0.0","name":%q,"sha512":%q,"size":100}`, name, validSha512))
}

func TestParseLatestBuildsTheDownloadURL(t *testing.T) {
	latest, err := parseLatest(latestJSON(validPackageName), baseURL)
	if err != nil {
		t.Fatalf("expected a normal package to be accepted: %s", err)
	}

	if latest.Url != baseURL+"/"+validPackageName {
		t.Fatalf("unexpected url %q", latest.Url)
	}

	if latest.Version != "2.0.0" {
		t.Fatalf("unexpected version %q", latest.Version)
	}
}

func TestParseLatestRejectsANameThatIsNotAPlainFile(t *testing.T) {
	// The name is chosen by whoever serves latest.json, and it lands on disk
	// before the checksum has had a chance to reject the package.
	for _, name := range []string{
		"../../etc/init.d/S99evil",
		"/etc/init.d/S99evil",
		"sub/dir/app.tar.gz",
		"..",
		"",
		".hidden",
		"app.tar.gz; reboot",
	} {
		if _, err := parseLatest(latestJSON(name), baseURL); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestParseLatestRejectsMalformedJSON(t *testing.T) {
	if _, err := parseLatest([]byte("not json"), baseURL); err == nil {
		t.Fatal("expected malformed json to be rejected")
	}
}

func TestParseLatestRejectsAnOversizedPackage(t *testing.T) {
	body := []byte(fmt.Sprintf(`{"version":"2.0.0","name":"app.tar.gz","sha512":"abc","size":%d}`, maxPackageSize+1))

	if _, err := parseLatest(body, baseURL); err == nil {
		t.Fatal("expected an oversized package to be rejected before downloading")
	}
}
