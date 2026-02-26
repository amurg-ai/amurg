package router

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// wsPingInterval is how often the hub sends WebSocket ping frames.
	wsPingInterval = 30 * time.Second
	// wsPongWait is the maximum time to wait for a pong from the peer.
	wsPongWait = 60 * time.Second
)

// startWSKeepalive sets up WebSocket-level ping/pong on a connection. It sets
// a read deadline, installs a pong handler, and starts a goroutine that sends
// periodic pings. The returned cancel function stops the ping goroutine.
// The provided mutex must be the same one used for all writes to the connection.
func startWSKeepalive(conn *websocket.Conn, mu *sync.Mutex) (cancel func()) {
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				mu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	return func() { close(done) }
}
