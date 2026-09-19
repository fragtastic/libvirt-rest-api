package hypervisor

import (
	"context"
	"testing"
)

func TestEventBrokerReplaysEventsAfterRequestedID(t *testing.T) {
	broker := newEventBroker()
	broker.publish(LifecycleEvent{Name: "first"})
	broker.publish(LifecycleEvent{Name: "second"})

	subscription := broker.subscribe(context.Background(), broker.cursor(1))
	defer subscription.Cancel()

	event := <-subscription.Events
	if event.ID != 2 || event.Name != "second" {
		t.Fatalf("unexpected replayed event: %+v", event)
	}
}

func TestEventBrokerCancelClosesSubscription(t *testing.T) {
	broker := newEventBroker()
	subscription := broker.subscribe(context.Background(), "")
	subscription.Cancel()

	if _, open := <-subscription.Events; open {
		t.Fatal("subscription channel remained open after cancellation")
	}
}

func TestEventBrokerCanReplayFullHistory(t *testing.T) {
	broker := newEventBroker()
	for index := 0; index < 256; index++ {
		broker.publish(LifecycleEvent{Name: "domain"})
	}

	subscription := broker.subscribe(context.Background(), "")
	defer subscription.Cancel()
	for expected := uint64(1); expected <= 256; expected++ {
		event := <-subscription.Events
		if event.ID != expected {
			t.Fatalf("event ID = %d, want %d", event.ID, expected)
		}
	}
}

func TestEventBrokerSignalsReplayGap(t *testing.T) {
	broker := newEventBroker()
	for index := 0; index < 257; index++ {
		broker.publish(LifecycleEvent{Name: "domain"})
	}
	subscription := broker.subscribe(context.Background(), broker.cursor(0))
	defer subscription.Cancel()
	event := <-subscription.Events
	if !event.StreamReset || event.ID != 257 {
		t.Fatalf("reset event = %+v", event)
	}
}

func TestEventBrokerSignalsCursorFromPreviousProcess(t *testing.T) {
	previous := newEventBroker()
	for index := 0; index < 5; index++ {
		previous.publish(LifecycleEvent{Name: "old-domain"})
	}
	oldCursor := previous.cursor(5)

	broker := newEventBroker()
	for index := 0; index < 10; index++ {
		broker.publish(LifecycleEvent{Name: "new-domain"})
	}
	subscription := broker.subscribe(context.Background(), oldCursor)
	defer subscription.Cancel()
	event := <-subscription.Events
	if !event.StreamReset || event.ID != 10 || event.Cursor != broker.cursor(10) {
		t.Fatalf("reset event = %+v", event)
	}
}
