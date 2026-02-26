package eventbus

import (
	"context"
	"log/slog"
)

// SlogHandler wraps an slog.Handler and publishes each log record to the event
// bus as a LogEntry event.
type SlogHandler struct {
	inner slog.Handler
	bus   *Bus
	attrs []slog.Attr
	group string
}

// NewSlogHandler returns a handler that writes to inner and also publishes to bus.
func NewSlogHandler(inner slog.Handler, bus *Bus) *SlogHandler {
	return &SlogHandler{inner: inner, bus: bus}
}

// Enabled delegates to the inner handler.
func (h *SlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle writes the record to the inner handler and publishes to the bus.
func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Publish to event bus as a log.entry event.
	entry := map[string]any{
		"level": r.Level.String(),
		"msg":   r.Message,
		"time":  r.Time,
	}
	if h.group != "" {
		entry["group"] = h.group
	}
	r.Attrs(func(a slog.Attr) bool {
		entry[a.Key] = a.Value.Any()
		return true
	})
	for _, a := range h.attrs {
		entry[a.Key] = a.Value.Any()
	}
	h.bus.PublishType(LogEntry, entry)

	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new handler with the given attributes.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlogHandler{
		inner: h.inner.WithAttrs(attrs),
		bus:   h.bus,
		attrs: append(h.attrs, attrs...),
		group: h.group,
	}
}

// WithGroup returns a new handler with the given group.
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	newGroup := name
	if h.group != "" {
		newGroup = h.group + "." + name
	}
	return &SlogHandler{
		inner: h.inner.WithGroup(name),
		bus:   h.bus,
		attrs: h.attrs,
		group: newGroup,
	}
}
