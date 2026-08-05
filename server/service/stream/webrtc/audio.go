package webrtc

import (
	"NanoKVM-Server/service/stream/audio"

	log "github.com/sirupsen/logrus"
)

const (
	// audioPayloadType 0 is PCMU's static assignment. pion rewrites it per
	// binding from what the peer negotiated, the same as video.
	audioPayloadType = 0
	audioSSRC        = 0x1234ABCE
)

// hasAudioListener reports whether any connected client negotiated an audio
// track. A viewer that connected before the settings switch was thrown has
// none, and capturing for it would burn a process nobody can hear.
//
// It reads the client snapshot rather than the map, so it does not need the
// manager lock and cannot invert the lock order against Client.mutex.
func (m *WebRTCManager) hasAudioListener() bool {
	for _, client := range m.getClients() {
		if client.hasAudioTrack() {
			return true
		}
	}

	return false
}

// clearAudioStream forgets a stream that has ended on its own, so that a later
// viewer starts a fresh one. It ignores a stream that has already been
// replaced.
func (m *WebRTCManager) clearAudioStream(stream *audio.Stream) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.audioStream != stream {
		return
	}

	m.audioStream = nil
	m.audioSending = false
}

// StartAudioStream begins capture if a client can hear it and the gadget has a
// capture card. Availability is checked here, not cached at start, because the
// settings switch rebuilds the gadget while the server runs.
func (m *WebRTCManager) StartAudioStream() {
	if !m.hasAudioListener() || !audio.Available() {
		return
	}

	m.mutex.Lock()
	if m.audioSending || len(m.clients) == 0 {
		m.mutex.Unlock()
		return
	}

	stream := audio.NewStream()
	m.audioStream = stream
	m.audioSending = true
	m.mutex.Unlock()

	stream.Start()
	go m.sendAudioStream(stream)

	log.Debugf("start sending g711 stream")
}

// StopAudioCapture stops capture whatever the client count, and kills the
// arecord child with it. main.go calls it from dispose() so the child does not
// outlive the server.
//
// An orphaned arecord does not die on its own. It notices the closed pipe only
// when it writes, and while the host plays nothing it blocks in the ALSA read
// for as long as the board is up. The orphan holds hw:UAC1Gadget,0
// exclusively, so the replacement server's arecord fails with "Device or
// resource busy", spends its whole restart budget in about three seconds, and
// gives up. Audio then stays dead until somebody logs in and kills the orphan.
//
// It is a package function rather than a method because dispose() has no
// manager to hand it. getManager() returns the same singleton that signalling
// uses.
func StopAudioCapture() {
	getManager().stopAudioStream()
}

// stopAudioStream ends capture and forgets the stream. The caller decides
// whether stopping is the right thing to do.
func (m *WebRTCManager) stopAudioStream() {
	m.mutex.Lock()

	if !m.audioSending {
		m.mutex.Unlock()
		return
	}

	stream := m.audioStream
	m.audioStream = nil
	m.audioSending = false
	m.mutex.Unlock()

	// Stop runs outside the lock. It waits on the capture goroutine, and no
	// other caller may be held up behind that.
	if stream != nil {
		stream.Stop()
	}

	log.Debugf("stop sending g711 stream")
}

// stopAudioStreamIfIdle stops capture once the last listener has gone.
//
// The condition mirrors StartAudioStream, which starts only when a client can
// hear the result. Stopping on an empty client map instead would keep arecord,
// the FIR and the packetizer running for a viewer that negotiated no audio
// track, which is what a viewer that connected before the switch was thrown
// has for its whole life.
//
// This has to kill the child process rather than wait for the loop to notice.
// While the host plays nothing, arecord blocks in a read, so the loop does not
// tick and would never see that nobody is listening.
//
// hasAudioListener reads the atomic client snapshot and takes no lock, so
// calling it before m.mutex cannot invert the lock order.
func (m *WebRTCManager) stopAudioStreamIfIdle() {
	if m.hasAudioListener() {
		return
	}

	m.stopAudioStream()
}

// sendAudioStream packetizes each frame once and hands the packets to every
// client, the same way the video loop does.
func (m *WebRTCManager) sendAudioStream(stream *audio.Stream) {
	for frame := range stream.Frames() {
		m.deliverAudioFrame(frame)
	}

	// The channel closed. Either the last listener left and
	// stopAudioStreamIfIdle stopped this stream, or capture failed too often
	// and the source gave up. The second case leaves the flag set unless it is
	// cleared here, and no later StartAudioStream could ever run.
	//
	// Clearing the flag does not bring audio back for the viewer that lost it.
	// StartAudioStream has one caller, an ICE state change, so a viewer whose
	// capture gave up stays silent until it reconnects. What this buys is that
	// the next connection starts a fresh stream instead of finding the manager
	// still convinced audio is being sent.
	m.clearAudioStream(stream)
}

// deliverAudioFrame packetizes one frame and hands the packets to every
// client that negotiated audio. Split out from sendAudioStream so the
// packetize-and-fan-out step can be tested directly, without a live capture
// stream behind it.
//
// A client whose track has no audio leg is skipped rather than enqueued and
// dropped: enqueueAudio would still take the slot lock and wake that client's
// writer for a frame it can only discard, fifty times a second, for as long
// as anyone else is listening.
//
// The listener check comes before Packetize for the same reason. Packetize
// allocates a header and a payload slice per packet, and a frame nobody can
// hear is fifty of those a second thrown away on a board with one core.
func (m *WebRTCManager) deliverAudioFrame(frame []byte) {
	clients := m.getClients()

	var listening bool
	for _, client := range clients {
		if client.hasAudioTrack() {
			listening = true
			break
		}
	}

	if !listening {
		return
	}

	packets := m.audioPacketizer.Packetize(frame, audio.FrameSamples)

	for _, client := range clients {
		if !client.hasAudioTrack() {
			continue
		}

		client.enqueueAudio(packets)
	}
}
