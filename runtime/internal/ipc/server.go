package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/amurg-ai/amurg/runtime/internal/eventbus"
)

// Server listens on a Unix socket and serves IPC requests.
type Server struct {
	path     string
	listener net.Listener
	provider StateProvider
	bus      *eventbus.Bus
	logger   *slog.Logger

	mu      sync.Mutex
	clients map[net.Conn]struct{}
	done    chan struct{}
}

// NewServer creates an IPC server.
func NewServer(socketPath string, provider StateProvider, bus *eventbus.Bus, logger *slog.Logger) *Server {
	return &Server{
		path:     socketPath,
		provider: provider,
		bus:      bus,
		logger:   logger.With("component", "ipc-server"),
		clients:  make(map[net.Conn]struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins listening on the Unix socket. Non-blocking.
func (s *Server) Start() error {
	// Remove stale socket.
	_ = os.Remove(s.path)

	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	s.listener = ln

	// Set socket permissions so only the user can connect.
	_ = os.Chmod(s.path, 0600)

	go s.acceptLoop()
	s.logger.Info("IPC server listening", "path", s.path)
	return nil
}

// Close shuts down the server and all client connections.
func (s *Server) Close() error {
	close(s.done)

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	s.mu.Lock()
	for c := range s.clients {
		_ = c.Close()
	}
	s.clients = make(map[net.Conn]struct{})
	s.mu.Unlock()

	_ = os.Remove(s.path)
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.logger.Warn("accept error", "error", err)
				continue
			}
		}

		s.mu.Lock()
		s.clients[conn] = struct{}{}
		s.mu.Unlock()

		go s.handleConn(conn)
	}
}

func (s *Server) removeClient(conn net.Conn) {
	s.mu.Lock()
	delete(s.clients, conn)
	s.mu.Unlock()
	_ = conn.Close()
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.removeClient(conn)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = s.writeResponse(conn, Response{Type: "error", Data: marshalRaw(map[string]string{"error": "invalid request"})})
			continue
		}

		s.handleRequest(conn, req)
	}
}

func (s *Server) handleRequest(conn net.Conn, req Request) {
	switch req.Method {
	case "status":
		status := s.provider.Status()
		_ = s.writeResponse(conn, Response{ID: req.ID, Type: "result", Data: marshalRaw(status)})

	case "sessions":
		sessions := s.provider.Sessions()
		_ = s.writeResponse(conn, Response{ID: req.ID, Type: "result", Data: marshalRaw(SessionsResult{Sessions: sessions})})

	case "subscribe":
		var params SubscribeParams
		if req.Params != nil {
			_ = json.Unmarshal(req.Params, &params)
		}
		s.handleSubscribe(conn, req.ID, params)

	default:
		_ = s.writeResponse(conn, Response{ID: req.ID, Type: "error", Data: marshalRaw(map[string]string{"error": "unknown method: " + req.Method})})
	}
}

func (s *Server) handleSubscribe(conn net.Conn, reqID string, params SubscribeParams) {
	ch := s.bus.Subscribe(params.Events...)
	defer s.bus.Unsubscribe(ch)

	// Confirm subscription.
	_ = s.writeResponse(conn, Response{ID: reqID, Type: "result", Data: marshalRaw(map[string]string{"status": "subscribed"})})

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			resp := Response{
				Type: "event",
				Data: marshalRaw(Event{
					Type:      evt.Type,
					Timestamp: evt.Timestamp,
					Data:      evt.Data,
				}),
			}
			if err := s.writeResponse(conn, resp); err != nil {
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *Server) writeResponse(conn net.Conn, resp Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	if err != nil {
		if !errors.Is(err, net.ErrClosed) {
			s.logger.Debug("write error", "error", err)
		}
	}
	return err
}

func marshalRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
