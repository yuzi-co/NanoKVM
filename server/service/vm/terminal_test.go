package vm

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// terminalWebsocketPair returns a connected server and client websocket.
func terminalWebsocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	conns := make(chan *websocket.Conn, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("failed to upgrade: %s", err)
			return
		}
		conns <- conn
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatalf("failed to dial: %s", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	select {
	case conn := <-conns:
		t.Cleanup(func() { _ = conn.Close() })
		return conn, client
	case <-time.After(5 * time.Second):
		t.Fatal("the server never accepted the connection")
		return nil, nil
	}
}

// A terminal session holds a pty and a /bin/sh, and only the handler's own
// teardown releases them. The handler returns when wsRead returns, and wsRead
// has no deadline of its own, so it returns only when the socket fails.
//
// That leaves the writer's failure unaccounted for. A client that stops reading
// while its connection stays up trips the write deadline, and the reader then
// waits for a message that a client in that state is not going to send. The
// shell survives the session that owned it.
//
// The pipe stands in for the pty here: closing its write end ends the read the
// writer is blocked on, which is what a failed write does to the writer in
// practice. The client sends nothing, exactly like a client that has stopped
// participating.
func TestTerminalStopsWhenTheWriterGivesUp(t *testing.T) {
	server, _ := terminalWebsocketPair(t)

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create the pipe: %s", err)
	}
	t.Cleanup(func() { _ = pr.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		runTerminal(server, pr)
	}()

	// End the writer. Nothing else about the session changes: the client is
	// still connected and still silent.
	if err := pw.Close(); err != nil {
		t.Fatalf("failed to close the pipe: %s", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session outlived its writer, so the pty and the shell behind it are never released")
	}
}
