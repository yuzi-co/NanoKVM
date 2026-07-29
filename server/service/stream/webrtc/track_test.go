package webrtc

import (
	"sync"
	"testing"

	"github.com/pion/rtp"
)

// recordingWriter stands in for a peer connection's track. It copies what it
// is handed, because pion rewrites the header of the packet it receives.
type recordingWriter struct {
	mutex   sync.Mutex
	written []rtp.Packet
}

func (w *recordingWriter) WriteRTP(packet *rtp.Packet) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.written = append(w.written, *packet)

	return nil
}

func (w *recordingWriter) packets() []rtp.Packet {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return append([]rtp.Packet(nil), w.written...)
}

func newTestTrack(extensionID uint8) (*Track, *recordingWriter) {
	writer := &recordingWriter{}
	track := &Track{video: writer}
	track.setPlayoutDelayExtensionID(extensionID)

	return track, writer
}

// sharedFrame is what the capture loop packetizes once and hands to everyone.
func sharedFrame() []*rtp.Packet {
	return []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1}, Payload: []byte("first")},
		{Header: rtp.Header{SequenceNumber: 2}, Payload: []byte("second")},
	}
}

// The extension id is negotiated per peer connection, so two viewers of the
// same frame can need different ones.
func TestWritePacketsAppliesEachTracksOwnExtensionID(t *testing.T) {
	packets := sharedFrame()

	trackA, writerA := newTestTrack(3)
	trackB, writerB := newTestTrack(7)

	if err := trackA.writePackets(packets); err != nil {
		t.Fatalf("writePackets failed: %s", err)
	}
	if err := trackB.writePackets(packets); err != nil {
		t.Fatalf("writePackets failed: %s", err)
	}

	for _, want := range []struct {
		writer *recordingWriter
		id     uint8
	}{{writerA, 3}, {writerB, 7}} {
		got := want.writer.packets()
		if len(got) != len(packets) {
			t.Fatalf("wrote %d packets, want %d", len(got), len(packets))
		}

		for _, packet := range got {
			if !packet.Header.Extension {
				t.Fatal("packet was written without the extension flag")
			}
			if packet.Header.GetExtension(want.id) == nil {
				t.Fatalf("packet is missing extension id %d", want.id)
			}
		}
	}
}

// The frame is packetized once and shared, so writing it must not scribble on
// the packets the next client is about to be given.
func TestWritePacketsLeavesTheSharedPacketsAlone(t *testing.T) {
	packets := sharedFrame()

	track, _ := newTestTrack(3)
	if err := track.writePackets(packets); err != nil {
		t.Fatalf("writePackets failed: %s", err)
	}

	for i, packet := range packets {
		if packet.Header.Extension {
			t.Fatalf("packet %d was mutated: extension flag set on the shared copy", i)
		}
		if len(packet.Header.Extensions) != 0 {
			t.Fatalf("packet %d was mutated: extensions appended to the shared copy", i)
		}
	}
}

func TestPlayoutDelayExtensionIDDefaultsWhenNotNegotiated(t *testing.T) {
	track := &Track{video: &recordingWriter{}}

	if got := track.playoutDelayExtensionID(); got != defaultPlayoutDelayExtensionID {
		t.Fatalf("extension id = %d, want %d", got, defaultPlayoutDelayExtensionID)
	}
}

// Signalling settles the extension id on the websocket goroutine while the
// capture goroutine is already writing frames for other viewers.
func TestExtensionIDIsSafeUnderConcurrentUpdate(t *testing.T) {
	track, _ := newTestTrack(5)
	packets := sharedFrame()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 200 {
			_ = track.writePackets(packets)
		}
	}()

	go func() {
		defer wg.Done()
		for i := range 200 {
			track.setPlayoutDelayExtensionID(uint8(i%10 + 1))
		}
	}()

	wg.Wait()
}
