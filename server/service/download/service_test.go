package download

import (
	"testing"
)

func TestImageFilenameFromURL(t *testing.T) {
	name, err := imageFilenameFromURL("https://example.com/isos/debian-13.iso")
	if err != nil {
		t.Fatalf("expected a normal iso url to be accepted: %s", err)
	}

	if name != "debian-13.iso" {
		t.Fatalf("unexpected filename %q", name)
	}
}

func TestImageFilenameFromURLAppliesTheSameRulesAsUploads(t *testing.T) {
	// The upload path validates the name; the remote path used only
	// filepath.Base, so it accepted names the rest of the service rejects.
	for _, raw := range []string{
		"https://example.com/",
		"https://example.com/..",
		"https://example.com/passwd",
		"https://example.com/a%20b.iso",
		"ftp://example.com/x.iso",
		"https:///x.iso",
		"not a url",
	} {
		if name, err := imageFilenameFromURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected, got %q", raw, name)
		}
	}
}

func TestFitsOnDiskLeavesHeadroom(t *testing.T) {
	// Filling the card completely takes the whole device down, not just the
	// download, so a chunk is kept back.
	available := int64(reservedFreeBytes) + 1000

	if !fitsOnDisk(1000, available) {
		t.Fatal("expected a download that fits to be allowed")
	}

	if fitsOnDisk(1001, available) {
		t.Fatal("expected a download that eats the headroom to be refused")
	}
}

func TestFitsOnDiskAllowsAnUnknownSize(t *testing.T) {
	// A server that sends no Content-Length reports -1; the copy is bounded
	// separately, so this must not fail up front.
	if !fitsOnDisk(-1, reservedFreeBytes*4) {
		t.Fatal("expected an unknown size to be allowed through")
	}
}

func TestAvailableBytesReportsFreeSpace(t *testing.T) {
	available, err := availableBytes(t.TempDir())
	if err != nil {
		t.Fatalf("expected the free space to be readable: %s", err)
	}

	if available <= 0 {
		t.Fatalf("expected a positive amount of free space, got %d", available)
	}
}
