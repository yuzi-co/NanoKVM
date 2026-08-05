package webrtc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/service/stream/audio"

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

// One audio frame is packetized once and handed to every listener, so these
// packets are shared. writeAudioPackets clears the extension flag and the
// extension slice on the header it writes, and doing that to the shared packet
// rather than to a copy would reach into the frame the next client is about to
// be given. Video carries the same test because the equivalent was once a real
// bug there.
func TestWriteAudioPacketsLeavesTheSharedPacketsAlone(t *testing.T) {
	packets := audioFrame()

	// A shared frame can arrive carrying an extension: the packetizer is not
	// the only thing that ever touches these headers, and a test whose input
	// already matches the output proves nothing.
	packets[0].Header.Extension = true
	packets[0].Header.ExtensionProfile = rtpExtensionProfile
	if err := packets[0].Header.SetExtension(5, []byte{1, 2}); err != nil {
		t.Fatalf("failed to set up the shared packet: %s", err)
	}

	track := &Track{audio: &recordingWriter{}}
	if err := track.writeAudioPackets(packets); err != nil {
		t.Fatalf("writeAudioPackets returned %s", err)
	}

	for i, packet := range packets {
		if !packet.Header.Extension {
			t.Fatalf("packet %d was mutated: the extension flag was cleared on the shared copy", i)
		}
		if len(packet.Header.Extensions) != 1 {
			t.Fatalf("packet %d was mutated: extensions dropped from the shared copy", i)
		}
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

// newConnectedTestConn returns the server side of a live websocket
// connection, the same way TestRemoveClientReturnsAfterAddClientStartsTheWriters
// builds one. AddClient logs ws.RemoteAddr(), so a real connection is needed
// rather than a bare struct.
func newConnectedTestConn(t *testing.T) *websocket.Conn {
	t.Helper()

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
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = dialerConn.Close() })

	serverConn := <-accepted
	t.Cleanup(func() { _ = serverConn.Close() })

	return serverConn
}

// ICE reports Connected and then Completed for an ordinary handshake, and
// signalling calls AddClient on both. This drives the real AddClient path,
// not the storeClient helper, so it would fail to catch a regression where
// AddClient ignored the guard entirely.
func TestAddClientTwiceInARowDoesNotPanic(t *testing.T) {
	conn := newConnectedTestConn(t)

	client := NewClient(conn, nil)
	client.track, _ = newTestTrack(5)

	manager := NewWebRTCManager()

	manager.AddClient(conn, client)
	manager.AddClient(conn, client)

	done := make(chan struct{})
	go func() {
		manager.RemoveClient(conn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveClient did not return within 2s after a repeated AddClient")
	}

	// Give a wrongly-duplicated writer a moment to reach the closed slot and
	// panic before the test exits.
	time.Sleep(100 * time.Millisecond)
}

// ICE can flap Connected -> Disconnected -> Connected on a transient network
// blip. The middle state drives RemoveClient, which closes both slots and
// waits for both writers to exit; recovery then calls AddClient again with
// the same *Client pointer. Gating the writer start on the manager's map
// (keyed by the websocket, which RemoveClient just deleted) would start them
// a second time: each writer's first Take() would return immediately from the
// already-closed slot and run its "defer close" on an already-closed channel.
func TestAddClientAfterRemoveClientDoesNotPanic(t *testing.T) {
	conn := newConnectedTestConn(t)

	client := NewClient(conn, nil)
	client.track, _ = newTestTrack(5)

	manager := NewWebRTCManager()

	manager.AddClient(conn, client)
	manager.RemoveClient(conn)

	done := make(chan struct{})
	go func() {
		manager.AddClient(conn, client)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AddClient did not return within 2s after a reconnect")
	}

	// Give a wrongly-restarted writer a moment to reach the closed slot and
	// panic before the test exits.
	time.Sleep(100 * time.Millisecond)
}

func TestStartAudioStreamDoesNothingWithoutClients(t *testing.T) {
	manager := NewWebRTCManager()

	manager.StartAudioStream()

	manager.mutex.Lock()
	sending := manager.audioSending
	manager.mutex.Unlock()

	if sending {
		t.Error("the audio stream started with no clients connected")
	}
}

// The audio loop blocks on a read while the host plays nothing, so it cannot
// notice an empty client list by itself. Stopping has to come from outside.
func TestStopAudioStreamIfIdleClearsTheFlag(t *testing.T) {
	manager := NewWebRTCManager()

	manager.mutex.Lock()
	manager.audioSending = true
	manager.mutex.Unlock()

	manager.stopAudioStreamIfIdle()

	manager.mutex.Lock()
	sending := manager.audioSending
	manager.mutex.Unlock()

	if sending {
		t.Error("stopAudioStreamIfIdle left the stream marked as sending")
	}
}

// The stop condition mirrors the start condition, and a client alone is not
// the start condition. A viewer that connected while the gadget had no card
// carries no audio track for its whole life, so capture that kept running for
// it would run arecord, the FIR and the packetizer with nobody able to hear
// any of it, for as long as that viewer stayed.
func TestStopAudioStreamIfIdleStopsWhileAVideoOnlyClientRemains(t *testing.T) {
	manager := NewWebRTCManager()

	videoOnly := newTestClient()
	manager.storeClient(&websocket.Conn{}, videoOnly)

	manager.mutex.Lock()
	manager.audioSending = true
	manager.mutex.Unlock()

	manager.stopAudioStreamIfIdle()

	manager.mutex.Lock()
	sending := manager.audioSending
	manager.mutex.Unlock()

	if sending {
		t.Error("capture kept running for a client that negotiated no audio track")
	}
}

// The other half of the same condition: a listener that is still connected
// keeps capture alive when some other viewer leaves.
func TestStopAudioStreamIfIdleKeepsCaptureForAListener(t *testing.T) {
	manager := NewWebRTCManager()

	listener := newTestClient()
	listener.mutex.Lock()
	listener.track.audio = &recordingWriter{}
	listener.mutex.Unlock()
	manager.storeClient(&websocket.Conn{}, listener)

	manager.mutex.Lock()
	manager.audioSending = true
	manager.mutex.Unlock()

	manager.stopAudioStreamIfIdle()

	manager.mutex.Lock()
	sending := manager.audioSending
	manager.mutex.Unlock()

	if !sending {
		t.Error("capture stopped while a client with an audio track was still connected")
	}
}

// ICE reports Connected and then Completed, and signalling calls AddClient on
// both. The second call must not start a second writer goroutine.
func TestStoreClientReportsRepeatedAdds(t *testing.T) {
	manager := NewWebRTCManager()
	client := &Client{}

	if _, _, isNew := manager.storeClient(nil, client); !isNew {
		t.Error("the first add was not reported as new")
	}

	if _, _, isNew := manager.storeClient(nil, client); isNew {
		t.Error("a repeated add was reported as new, so the writers would start twice")
	}
}

// Capture is worth starting only for a client that negotiated an audio track.
// A viewer that connected before the switch was thrown has none.
func TestHasAudioListenerIgnoresClientsWithoutAnAudioTrack(t *testing.T) {
	manager := NewWebRTCManager()

	client := newTestClient()
	manager.storeClient(nil, client)

	if manager.hasAudioListener() {
		t.Error("a client with a video-only track was counted as a listener")
	}

	client.mutex.Lock()
	client.track.audio = &recordingWriter{}
	client.mutex.Unlock()

	if !manager.hasAudioListener() {
		t.Error("a client with an audio track was not counted as a listener")
	}
}

// The stream-lifecycle tests below stop the stream before calling the send
// loop, so the packetize-and-fan-out step never runs there. This test drives
// that step directly, bypassing the capture channel, so a nil packetizer or a
// wrong sample count would not sail through untested. It also checks that a
// client without an audio track is skipped, not handed a frame it can only
// discard.
func TestDeliverAudioFrameReachesOnlyClientsWithAudio(t *testing.T) {
	manager := NewWebRTCManager()

	videoOnly := newTestClient()
	manager.storeClient(&websocket.Conn{}, videoOnly)

	withAudio := newTestClient()
	withAudio.mutex.Lock()
	withAudio.track.audio = &recordingWriter{}
	withAudio.mutex.Unlock()
	manager.storeClient(&websocket.Conn{}, withAudio)

	manager.deliverAudioFrame(make([]byte, audio.FrameSamples))

	if videoOnly.audioSlot.Pending() {
		t.Error("a client without an audio track was handed a frame")
	}

	packets, ok := withAudio.audioSlot.Take()
	if !ok {
		t.Fatal("the audio slot delivered nothing")
	}

	if len(packets) == 0 {
		t.Fatal("packetize produced no RTP packets for a 20ms frame")
	}
}

// When capture gives up, the frame channel closes and the send loop returns.
// The manager has to notice, or it never starts a replacement stream.
func TestSendAudioStreamClearsTheFlagWhenTheStreamEnds(t *testing.T) {
	manager := NewWebRTCManager()

	stream := audio.NewStream()

	manager.mutex.Lock()
	manager.audioStream = stream
	manager.audioSending = true
	manager.mutex.Unlock()

	stream.Stop()
	manager.sendAudioStream(stream)

	manager.mutex.Lock()
	sending := manager.audioSending
	manager.mutex.Unlock()

	if sending {
		t.Error("the send loop returned but the manager still believes audio is sending")
	}
}
