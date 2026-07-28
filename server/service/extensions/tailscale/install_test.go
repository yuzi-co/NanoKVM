package tailscale

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedURLMustStayOnThePackageHost(t *testing.T) {
	// The download URL comes from following a redirect, so without this the
	// package host decides where a root-run binary is fetched from.
	if err := checkDownloadHost("https://pkgs.tailscale.com/stable/tailscale_1.2.3_riscv64.tgz"); err != nil {
		t.Fatalf("expected the real host to be accepted: %s", err)
	}

	for _, raw := range []string{
		"https://evil.example.com/tailscale.tgz",
		"http://pkgs.tailscale.com/stable/x.tgz",
		"https://pkgs.tailscale.com.evil.example.com/x.tgz",
		"://nonsense",
	} {
		if err := checkDownloadHost(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestParseSHA256ChecksumReadsADigest(t *testing.T) {
	digest := "69553952aa9d7c079763d18e33ceb1dcf50ebc5c0b1b429c0d8367eed38b8888"

	for _, body := range []string{
		digest,
		digest + "\n",
		digest + "  tailscale_1.2.3_riscv64.tgz\n",
	} {
		sum, err := parseSHA256Checksum(body)
		if err != nil {
			t.Fatalf("expected %q to parse: %s", body, err)
		}

		if hex.EncodeToString(sum) != digest {
			t.Fatalf("expected %s, got %s", digest, hex.EncodeToString(sum))
		}
	}
}

func TestParseSHA256ChecksumRejectsGarbage(t *testing.T) {
	for _, body := range []string{"", "not-a-digest", "abcd", "zz553952aa9d7c079763d18e33ceb1dcf50ebc5c0b1b429c0d8367eed38b8888"} {
		if _, err := parseSHA256Checksum(body); err == nil {
			t.Fatalf("expected %q to be rejected", body)
		}
	}
}

func TestVerifyFileSHA256AcceptsAMatchingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.tgz")
	content := []byte("tailscale release")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	sum := sha256.Sum256(content)

	if err := verifyFileSHA256(path, sum[:]); err != nil {
		t.Fatalf("expected the matching file to be accepted: %s", err)
	}
}

func TestVerifyFileSHA256RejectsATamperedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.tgz")
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	sum := sha256.Sum256([]byte("tailscale release"))

	if err := verifyFileSHA256(path, sum[:]); err == nil {
		t.Fatal("expected a tampered file to be rejected")
	}
}
