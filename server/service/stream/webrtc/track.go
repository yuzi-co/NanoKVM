package webrtc

import (
	"github.com/pion/rtp"
	log "github.com/sirupsen/logrus"
)

// defaultPlayoutDelayExtensionID is used until signalling tells us what the
// peer negotiated.
const defaultPlayoutDelayExtensionID uint8 = 5

// audioClockRate is the RTP clock for G.711.
const audioClockRate = 8000

// rtpExtensionProfile is the one-byte header extension profile (RFC 5285).
const rtpExtensionProfile = 0xBEDE

// playoutDelayExtensionURI has to match on both sides of the negotiation.
const playoutDelayExtensionURI = "http://www.webrtc.org/experiments/rtp-hdrext/playout-delay"

// playoutDelayExtensionData asks the receiver not to buffer ahead of playback.
// It is the same for every client and never changes, so it is marshalled once
// instead of once per track.
var playoutDelayExtensionData = marshalPlayoutDelay()

func marshalPlayoutDelay() []byte {
	data, err := (&rtp.PlayoutDelayExtension{MinDelay: 0, MaxDelay: 0}).Marshal()
	if err != nil {
		log.Errorf("failed to marshal the playout delay extension: %v", err)
		return nil
	}

	return data
}

// rtpWriter is the part of a peer connection's track that a Track writes to.
// Naming it keeps the packet handling testable without a peer connection.
type rtpWriter interface {
	WriteRTP(*rtp.Packet) error
}

// setPlayoutDelayExtensionID records what signalling negotiated. It runs on the
// websocket goroutine while the capture goroutine may already be writing frames
// for this client, so the value is held atomically.
func (t *Track) setPlayoutDelayExtensionID(id uint8) {
	t.extensionID.Store(uint32(id))
}

func (t *Track) playoutDelayExtensionID() uint8 {
	if id := t.extensionID.Load(); id != 0 {
		return uint8(id)
	}

	return defaultPlayoutDelayExtensionID
}

// writePackets sends one frame to this client's peer connection.
//
// The frame is packetized once for everyone, so these packets are shared. pion
// rewrites the SSRC and payload type of whatever header it is handed, and every
// client writes on its own goroutine, so each packet goes out through a copy.
// Only the header is copied - the payload is the encoded frame itself, which
// nobody modifies.
func (t *Track) writePackets(packets []*rtp.Packet) error {
	id := t.playoutDelayExtensionID()

	for _, source := range packets {
		packet := rtp.Packet{Header: source.Header, Payload: source.Payload}

		packet.Header.Extension = true
		packet.Header.ExtensionProfile = rtpExtensionProfile
		// The struct copy still points at the source's extension slice, and
		// appending to that would reach back into the shared packet.
		packet.Header.Extensions = nil

		if err := packet.Header.SetExtension(id, playoutDelayExtensionData); err != nil {
			log.Errorf("failed to set extension: %v", err)
			return err
		}

		if err := t.video.WriteRTP(&packet); err != nil {
			log.Errorf("failed to write RTP: %v", err)
			return err
		}
	}

	return nil
}

// writeAudioPackets sends one audio frame to this client's peer connection.
//
// The packets are shared between clients, so each one goes out through a copy.
// Audio carries no playout delay extension: that hint is about video rendering
// and the browser's own jitter buffer handles the rest.
func (t *Track) writeAudioPackets(packets []*rtp.Packet) error {
	for _, source := range packets {
		packet := rtp.Packet{Header: source.Header, Payload: source.Payload}

		packet.Header.Extension = false
		packet.Header.Extensions = nil

		if err := t.audio.WriteRTP(&packet); err != nil {
			log.Errorf("failed to write audio RTP: %v", err)
			return err
		}
	}

	return nil
}
