package webrtc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
)

func audioFrame() []*rtp.Packet {
	return []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 7}, Payload: []byte("mulaw")},
	}
}

// Audio uses Replace, not TryPut: there is no keyframe to wait for, so the
// newest 20 ms is always the right one to keep.
func TestEnqueueAudioKeepsTheNewestFrame(t *testing.T) {
	client := newTestClient()

	client.enqueueAudio(audioFrame())
	client.enqueueAudio([]*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 8}, Payload: []byte("newer")},
	})

	packets, ok := client.audioSlot.Take()
	if !ok {
		t.Fatal("the audio slot delivered nothing")
	}

	if got := string(packets[0].Payload); got != "newer" {
		t.Errorf("the slot held %q, want the newest frame", got)
	}
}

func TestWriteAudioPacketsCarriesNoHeaderExtension(t *testing.T) {
	writer := &recordingWriter{}
	track := &Track{audio: writer}

	if err := track.writeAudioPackets(audioFrame()); err != nil {
		t.Fatalf("writeAudioPackets returned %s", err)
	}

	written := writer.packets()
	if len(written) != 1 {
		t.Fatalf("got %d packets, want 1", len(written))
	}

	// The playout delay extension is a video hint and has no meaning here.
	if written[0].Header.Extension {
		t.Error("an audio packet carried a header extension")
	}

	if got := string(written[0].Payload); got != "mulaw" {
		t.Errorf("payload is %q, want %q", got, "mulaw")
	}
}

func TestWriteAudioDrainsTheSlotUntilClosed(t *testing.T) {
	writer := &recordingWriter{}

	client := NewClient(nil, nil)
	client.track = &Track{audio: writer}

	go client.writeAudio()

	client.enqueueAudio(audioFrame())
	client.audioSlot.Close()
	<-client.audioDone

	if len(writer.packets()) != 1 {
		t.Errorf("got %d packets written, want 1", len(writer.packets()))
	}
}

// stop() waits on both audioDone and done. Only AddClient starts the
// goroutines that close them, so this drives the real production entry
// points rather than starting the writers by hand - a test that starts
// writeAudio() itself would pass even if AddClient never did.
func TestRemoveClientReturnsAfterAddClientStartsTheWriters(t *testing.T) {
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		accepted <- conn
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer dialerConn.Close()

	serverConn := <-accepted
	defer serverConn.Close()

	client := NewClient(serverConn, nil)
	client.track, _ = newTestTrack(5)

	manager := NewWebRTCManager()
	manager.AddClient(serverConn, client)

	done := make(chan struct{})
	go func() {
		manager.RemoveClient(serverConn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveClient did not return within 2s: AddClient must start every writer that stop() waits on")
	}
}
