package bus

import (
	"sync"
	"time"
)

// EventType identifies what triggered a bus event.
type EventType string

const (
	EventCalendarChanged EventType = "calendar_changed" // a source synced new/updated events
	EventReplanRequested EventType = "replan_requested" // manual or scheduled replan
	EventBlocksUpdated   EventType = "blocks_updated"   // planner finished a replan cycle
)

// Message is a single bus event.
type Message struct {
	Type      EventType
	SourceID  string    // which calendar source triggered this (empty for replan/blocks)
	RequestID string    // short ID that ties all log lines for one replan cycle together
	At        time.Time
}

// Handler is a function that receives bus messages.
type Handler func(msg Message)

// Bus is an in-process pub/sub bus with per-topic debounce.
// Concurrent-safe; subscribers receive on a goroutine managed by the bus.
type Bus struct {
	mu          sync.RWMutex
	handlers    []Handler
	debounce    time.Duration
	pending     map[EventType]*time.Timer
	pendingMu   sync.Mutex
}

// New creates a Bus with the given debounce window.
// Multiple Publish calls for the same EventType within the window
// collapse into a single delivery.
func New(debounce time.Duration) *Bus {
	return &Bus{
		debounce: debounce,
		pending:  make(map[EventType]*time.Timer),
	}
}

// Subscribe registers a handler. Handlers are called sequentially in
// registration order on a single dedicated goroutine per delivery.
func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// Publish schedules delivery of msg after the debounce window.
// If a message of the same type is already pending, the timer resets.
func (b *Bus) Publish(msg Message) {
	msg.At = time.Now()
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	if t, ok := b.pending[msg.Type]; ok {
		t.Reset(b.debounce)
		return
	}
	b.pending[msg.Type] = time.AfterFunc(b.debounce, func() {
		b.pendingMu.Lock()
		delete(b.pending, msg.Type)
		b.pendingMu.Unlock()
		b.deliver(msg)
	})
}

// PublishImmediate delivers msg without debouncing (used for shutdown/flush).
func (b *Bus) PublishImmediate(msg Message) {
	msg.At = time.Now()
	b.deliver(msg)
}

func (b *Bus) deliver(msg Message) {
	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		h(msg)
	}
}
