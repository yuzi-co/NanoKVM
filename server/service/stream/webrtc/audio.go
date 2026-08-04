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

// stopAudioStreamIfIdle stops capture once the last viewer has gone.
//
// This has to kill the child process rather than wait for the loop to notice.
// While the host plays nothing, arecord blocks in a read, so the loop does not
// tick and would never see the empty client list.
func (m *WebRTCManager) stopAudioStreamIfIdle() {
	m.mutex.Lock()

	if len(m.clients) > 0 || !m.audioSending {
		m.mutex.Unlock()
		return
	}

	stream := m.audioStream
	m.audioStream = nil
	m.audioSending = false
	m.mutex.Unlock()

	if stream != nil {
		stream.Stop()
	}

	log.Debugf("stop sending g711 stream")
}

// sendAudioStream packetizes each frame once and hands the packets to every
// client, the same way the video loop does.
func (m *WebRTCManager) sendAudioStream(stream *audio.Stream) {
	for frame := range stream.Frames() {
		packets := m.audioPacketizer.Packetize(frame, audio.FrameSamples)

		for _, client := range m.getClients() {
			client.enqueueAudio(packets)
		}
	}

	// The channel closed. Either the last viewer left and stopAudioStreamIfIdle
	// stopped this stream, or capture failed too often and the source gave up.
	// The second case leaves the flag set unless it is cleared here, and then
	// audio never recovers for the life of the process.
	m.clearAudioStream(stream)
}
