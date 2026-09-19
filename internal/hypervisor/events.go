package hypervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LifecycleEvent struct {
	ID          uint64    `json:"id"`
	Cursor      string    `json:"cursor"`
	OccurredAt  time.Time `json:"occurred_at"`
	Name        string    `json:"name"`
	UUID        string    `json:"uuid"`
	Event       string    `json:"event"`
	Detail      int       `json:"detail"`
	StreamReset bool      `json:"-"`
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
	generation  string
}

func newEventBroker() *eventBroker {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return &eventBroker{subscribers: make(map[chan LifecycleEvent]struct{}), generation: strconv.FormatInt(time.Now().UnixNano(), 36)}
	}
	return &eventBroker{subscribers: make(map[chan LifecycleEvent]struct{}), generation: hex.EncodeToString(bytes)}
}

func (b *eventBroker) publish(event LifecycleEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.nextID++
	event.ID = b.nextID
	event.Cursor = b.cursor(b.nextID)
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

func (b *eventBroker) subscribe(ctx context.Context, cursor string) Subscription {
	b.mu.Lock()
	channel := make(chan LifecycleEvent, len(b.history)+33)
	if b.closed {
		close(channel)
		b.mu.Unlock()
		return Subscription{Events: channel, Cancel: func() {}}
	}
	after, unreplayable := b.afterCursor(cursor)
	if !unreplayable && len(b.history) > 0 && after < b.history[0].ID-1 {
		unreplayable = true
	}
	if unreplayable {
		channel <- LifecycleEvent{ID: b.nextID, Cursor: b.cursor(b.nextID), OccurredAt: time.Now().UTC(), StreamReset: true}
	} else {
		for _, event := range b.history {
			if event.ID > after {
				channel <- event
			}
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

func (b *eventBroker) cursor(id uint64) string {
	return b.generation + ":" + strconv.FormatUint(id, 10)
}

func (b *eventBroker) afterCursor(cursor string) (uint64, bool) {
	if cursor == "" {
		return 0, false
	}
	generation, sequence, found := strings.Cut(cursor, ":")
	if !found || generation != b.generation {
		return 0, true
	}
	after, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil || after > b.nextID {
		return 0, true
	}
	return after, false
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
