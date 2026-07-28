package application

import (
	"fmt"
	"testing"
)

const baseURL = "https://cdn.example.com/nanokvm"

func latestJSON(name string) []byte {
	return []byte(fmt.Sprintf(`{"version":"2.0.0","name":%q,"sha512":"abc","size":100}`, name))
}

func TestParseLatestBuildsTheDownloadURL(t *testing.T) {
	latest, err := parseLatest(latestJSON("NanoKVM.tar.gz"), baseURL)
	if err != nil {
		t.Fatalf("expected a normal package to be accepted: %s", err)
	}

	if latest.Url != baseURL+"/NanoKVM.tar.gz" {
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
