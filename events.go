package scout

import "sync"

// Event names published by the search pipeline. Mirrors scout's
// Events\ModelsImported and Events\ModelsFlushed.
const (
	// EventModelsImported fires after a chunk of records is indexed.
	EventModelsImported = "scout.models_imported"
	// EventModelsFlushed fires after records are removed from the index.
	EventModelsFlushed = "scout.models_flushed"
)

// ModelsImported carries the chunk of records that was just indexed.
type ModelsImported struct{ Models []ScoutModel }

// ModelsFlushed carries the records that were just removed from the index.
type ModelsFlushed struct{ Models []ScoutModel }

// EventBus is a synchronous, thread-safe event bus keyed by event name.
// Publish calls handlers inline, in subscription order.
type EventBus struct {
	mu   sync.RWMutex
	subs map[string][]func(any)
}

// NewEventBus returns an empty EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subs: map[string][]func(any){}}
}

// Subscribe registers fn for event. It is safe to call concurrently.
func (b *EventBus) Subscribe(event string, fn func(any)) {
	if fn == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[event] = append(b.subs[event], fn)
}

// Forget removes every handler subscribed to event.
func (b *EventBus) Forget(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, event)
}

// Publish delivers payload to every handler subscribed to event. Handlers run
// outside the lock so a handler may publish without deadlocking.
func (b *EventBus) Publish(event string, payload any) {
	b.mu.RLock()
	handlers := append([]func(any){}, b.subs[event]...)
	b.mu.RUnlock()
	for _, fn := range handlers {
		fn(payload)
	}
}
