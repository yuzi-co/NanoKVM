package utils

import (
	"net/http"
	"time"
)

// readHeaderTimeout bounds how long a client may take to send its request
// headers. Without it a handful of sockets trickling one byte at a time ties
// up the whole server (slowloris).
const readHeaderTimeout = 15 * time.Second

// idleTimeout closes keep-alive connections that go quiet between requests.
const idleTimeout = 2 * time.Minute

// NewServer builds an http.Server with the timeouts that gin's Run helpers
// leave at zero.
//
// ReadTimeout and WriteTimeout stay unset on purpose: this server streams MJPEG
// responses that never end and accepts multi-gigabyte image uploads, both of
// which a whole-request deadline would cut off. Websocket connections hijack
// the socket, so these timeouts do not apply to them either way.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}
