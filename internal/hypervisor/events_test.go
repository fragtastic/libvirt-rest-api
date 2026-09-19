package hypervisor

import (
	"context"
	"testing"
)

func TestEventBrokerReplaysEventsAfterRequestedID(t *testing.T) {
	broker := newEventBroker()
	broker.publish(LifecycleEvent{Name: "first"})
	broker.publish(LifecycleEvent{Name: "second"})

	subscription := broker.subscribe(context.Background(), 1)
	defer subscription.Cancel()

	event := <-subscription.Events
	if event.ID != 2 || event.Name != "second" {
		t.Fatalf("unexpected replayed event: %+v", event)
	}
}

func TestEventBrokerCancelClosesSubscription(t *testing.T) {
	broker := newEventBroker()
	subscription := broker.subscribe(context.Background(), 0)
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

	subscription := broker.subscribe(context.Background(), 0)
	defer subscription.Cancel()
	for expected := uint64(1); expected <= 256; expected++ {
		event := <-subscription.Events
		if event.ID != expected {
			t.Fatalf("event ID = %d, want %d", event.ID, expected)
		}
	}
}
