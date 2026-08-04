package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCards(t *testing.T, contents string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cards")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write the fixture: %s", err)
	}

	original := cardsPath
	cardsPath = path
	t.Cleanup(func() { cardsPath = original })
}

func TestAvailableFindsTheGadgetCard(t *testing.T) {
	writeCards(t, ` 0 [cv182xaadc     ]: cv182xa_adc - cv182xa_adc
 2 [UAC1Gadget     ]: UAC1_Gadget - UAC1_Gadget
`)

	if !Available() {
		t.Error("Available reported false while the card is listed")
	}
}

func TestAvailableIsFalseWithoutTheGadgetCard(t *testing.T) {
	writeCards(t, ` 0 [cv182xaadc     ]: cv182xa_adc - cv182xa_adc
`)

	if Available() {
		t.Error("Available reported true while only the analog codec is listed")
	}
}

func TestAvailableIsFalseWhenTheFileIsMissing(t *testing.T) {
	original := cardsPath
	cardsPath = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { cardsPath = original })

	if Available() {
		t.Error("Available reported true with no cards file at all")
	}
}

func TestStreamEncodesSourceChunksIntoFrames(t *testing.T) {
	stream := NewStream()

	// Feed one chunk of silence directly, bypassing arecord.
	go func() {
		stream.consume(make([]byte, ChunkBytes))
		stream.Stop()
	}()

	frame, ok := <-stream.Frames()
	if !ok {
		t.Fatal("the frame channel closed before delivering a frame")
	}

	if len(frame) != FrameSamples {
		t.Errorf("got a %d byte frame, want %d", len(frame), FrameSamples)
	}

	// Silence is 0xFF in mu-law, not 0x00.
	if frame[0] != 0xFF {
		t.Errorf("silence encoded to %#02x, want 0xFF", frame[0])
	}
}

func TestStreamClosesFramesOnStop(t *testing.T) {
	stream := NewStream()
	stream.Stop()

	for range stream.Frames() { //nolint:revive // draining is the point
	}
}
