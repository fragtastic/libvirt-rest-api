package hypervisor

import (
	"context"
	"sync"
	"time"
)

type LifecycleEvent struct {
	ID         uint64    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Name       string    `json:"name"`
	UUID       string    `json:"uuid"`
	Event      string    `json:"event"`
	Detail     int       `json:"detail"`
}

type Subscription struct {
	Events <-chan LifecycleEvent
	Cancel func()
}

type eventBroker struct {
	mu          sync.Mutex
	nextID      uint64
	history     []LifecycleEvent
	subscribers map[chan LifecycleEvent]struct{}
	closed      bool
}

func newEventBroker() *eventBroker {
	return &eventBroker{subscribers: make(map[chan LifecycleEvent]struct{})}
}

func (b *eventBroker) publish(event LifecycleEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.nextID++
	event.ID = b.nextID
	event.OccurredAt = time.Now().UTC()
	b.history = append(b.history, event)
	if len(b.history) > 256 {
		b.history = append([]LifecycleEvent(nil), b.history[len(b.history)-256:]...)
	}
	for subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
			close(subscriber)
			delete(b.subscribers, subscriber)
		}
	}
}

func (b *eventBroker) subscribe(ctx context.Context, after uint64) Subscription {
	b.mu.Lock()
	channel := make(chan LifecycleEvent, len(b.history)+32)
	if b.closed {
		close(channel)
		b.mu.Unlock()
		return Subscription{Events: channel, Cancel: func() {}}
	}
	for _, event := range b.history {
		if event.ID > after {
			channel <- event
		}
	}
	b.subscribers[channel] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	done := make(chan struct{})
	cancel := func() {
		once.Do(func() {
			close(done)
			b.mu.Lock()
			if _, ok := b.subscribers[channel]; ok {
				delete(b.subscribers, channel)
				close(channel)
			}
			b.mu.Unlock()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	}()
	return Subscription{Events: channel, Cancel: cancel}
}

func (b *eventBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for subscriber := range b.subscribers {
		close(subscriber)
	}
	clear(b.subscribers)
}
