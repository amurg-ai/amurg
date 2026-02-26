package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client connects to the runtime IPC server.
type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex
	nextID  atomic.Int64

	// responses and events are demuxed by a background reader.
	pending map[string]chan Response
	eventCh chan Event
	pendMu  sync.Mutex
	done    chan struct{}
}

// Dial connects to the runtime IPC socket.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial IPC socket: %w", err)
	}

	c := &Client{
		conn:    conn,
		scanner: bufio.NewScanner(conn),
		pending: make(map[string]chan Response),
		eventCh: make(chan Event, 64),
		done:    make(chan struct{}),
	}
	c.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	go c.readLoop()
	return c, nil
}

// Call sends a request and waits for the response.
func (c *Client) Call(method string, params any) (*Response, error) {
	id := fmt.Sprintf("%d", c.nextID.Add(1))

	ch := make(chan Response, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()

	req := Request{ID: id, Method: method}
	if params != nil {
		req.Params, _ = json.Marshal(params)
	}

	if err := c.send(req); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		return &resp, nil
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// Subscribe sends a subscribe request and returns the event channel.
// Events are delivered on the channel returned by Events().
func (c *Client) Subscribe(events ...string) error {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	req := Request{ID: id, Method: "subscribe"}
	if len(events) > 0 {
		req.Params, _ = json.Marshal(SubscribeParams{Events: events})
	}
	return c.send(req)
}

// Events returns the channel that receives subscribed events.
func (c *Client) Events() <-chan Event {
	return c.eventCh
}

// Close closes the connection.
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return c.conn.Close()
}

func (c *Client) send(req Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.conn.Write(data)
	return err
}

func (c *Client) readLoop() {
	defer func() {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		close(c.eventCh)
	}()

	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.Type == "event" {
			var evt Event
			if err := json.Unmarshal(resp.Data, &evt); err == nil {
				select {
				case c.eventCh <- evt:
				default:
				}
			}
			continue
		}

		// It's a result/error response — route to pending call.
		if resp.ID != "" {
			c.pendMu.Lock()
			ch, ok := c.pending[resp.ID]
			c.pendMu.Unlock()
			if ok {
				ch <- resp
			}
		}
	}
}
