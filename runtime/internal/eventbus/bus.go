package eventbus

import (
	"encoding/json"
	"sync"
	"time"
)

// Event types published on the bus.
const (
	HubConnected    = "hub.connected"
	HubDisconnected = "hub.disconnected"
	HubReconnecting = "hub.reconnecting"
	SessionCreated  = "session.created"
	SessionClosed   = "session.closed"
	SessionState    = "session.state"
	AgentOutput     = "agent.output"
	LogEntry        = "log.entry"
)

// Event is a single message on the bus.
type Event struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"ts"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Bus is a fan-out pub/sub event bus. Subscribers receive events on a buffered
// channel. Slow subscribers are dropped (non-blocking publish).
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]map[string]bool // channel → set of subscribed event types (nil = all)
}

// New creates a new event bus.
func New() *Bus {
	return &Bus{
		subs: make(map[chan Event]map[string]bool),
	}
}

// Subscribe returns a channel that receives events matching the given types.
// If no types are given, all events are received. The channel is buffered (64).
func (b *Bus) Subscribe(types ...string) chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(types) == 0 {
		b.subs[ch] = nil // nil = all events
	} else {
		filter := make(map[string]bool, len(types))
		for _, t := range types {
			filter[t] = true
		}
		b.subs[ch] = filter
	}
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

// Publish sends an event to all matching subscribers. Non-blocking: if a
// subscriber's buffer is full the event is dropped for that subscriber.
func (b *Bus) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch, filter := range b.subs {
		if filter != nil && !filter[e.Type] {
			continue
		}
		select {
		case ch <- e:
		default:
			// slow subscriber, drop
		}
	}
}

// PublishType is a convenience method that creates an Event with the given type
// and data, then publishes it.
func (b *Bus) PublishType(eventType string, data any) {
	var raw json.RawMessage
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	b.Publish(Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      raw,
	})
}

// Close unsubscribes all subscribers and closes their channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
	}
}
