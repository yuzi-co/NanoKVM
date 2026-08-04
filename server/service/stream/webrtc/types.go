package webrtc

import (
	"sync"
	"sync/atomic"

	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/stream/audio"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type WebRTCManager struct {
	clients        map[*websocket.Conn]*Client
	clientSnapshot atomic.Pointer[[]*Client]
	videoSending   bool
	mutex          sync.Mutex
	viewerVersion  uint64

	// videoPacketizer is shared: a frame is cut into RTP packets once and the
	// packets handed to every client, rather than each client paying to
	// packetize and copy the same frame again.
	videoPacketizer rtp.Packetizer

	audioSending    bool
	audioStream     *audio.Stream
	audioPacketizer rtp.Packetizer
}

type Client struct {
	ws    *websocket.Conn
	video *webrtc.PeerConnection
	track *Track
	mutex sync.Mutex

	// slot holds at most one frame for this client. The capture loop hands a
	// frame over and moves on; the writer goroutine takes frames at whatever
	// rate this connection manages.
	slot *stream.FrameSlot[[]*rtp.Packet]
	done chan struct{}

	// audioSlot holds at most one pending audio frame, with its own writer
	// goroutine. Sharing the video slot would drop audio whenever video fell
	// behind, and the two have nothing to do with each other.
	audioSlot *stream.FrameSlot[[]*rtp.Packet]
	audioDone chan struct{}

	// waitingForKeyFrame is read and written only by the capture goroutine.
	waitingForKeyFrame bool
}

func (c *Client) WsConn() *websocket.Conn {
	return c.ws
}

type SignalingHandler struct {
	client         *Client
	mutex          sync.Mutex
	unregisterMode func()
	closed         bool
}

type Track struct {
	video rtpWriter

	// audio is nil when the gadget had no capture card at negotiation time.
	audio rtpWriter

	// extensionID is negotiated on the websocket goroutine and read on the
	// capture goroutine.
	extensionID atomic.Uint32
}

type Message struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}
