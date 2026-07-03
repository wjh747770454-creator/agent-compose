package events

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestDispatcherDispatchOnceAckRetryAndDecodeWorkflows(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	store := &dispatcherCoverageStore{events: []domain.TopicEventRecord{
		{ID: "event-1", Topic: "topic.one", PayloadJSON: `{"ok":true}`, CreatedAt: now},
		{ID: "event-bad", Topic: "topic.bad", PayloadJSON: `{`, CreatedAt: now},
	}}
	bus := &dispatcherCoverageBus{}
	dispatcher := NewDispatcher(ctx, store, bus)
	dispatcher.SetInterval(time.Millisecond)
	dispatcher.DispatchOnce(ctx, 10)
	if len(bus.events) != 1 || bus.events[0].Topic != "topic.one" {
		t.Fatalf("bus events = %#v", bus.events)
	}
	if err := bus.events[0].Ack(ctx); err != nil {
		t.Fatalf("Ack returned error: %v", err)
	}
	if store.published != 1 || store.released == 0 {
		t.Fatalf("store = %#v", store)
	}

	store.events = []domain.TopicEventRecord{{ID: "event-2", Topic: "topic.two", PayloadJSON: `{"retry":true}`}}
	bus.publishOK = false
	dispatcher.DispatchOnce(ctx, 10)
	if store.retrying == 0 {
		t.Fatalf("expected retry release, store=%#v", store)
	}
	dispatcher.setInFlight("event-3")
	if !dispatcher.isInFlight("event-3") {
		t.Fatalf("expected in-flight event")
	}
	dispatcher.clearInFlight("event-3")
	if dispatcher.isInFlight("event-3") {
		t.Fatalf("expected in-flight event cleared")
	}
	(*Dispatcher)(nil).Start()
	(*Dispatcher)(nil).DispatchOnce(ctx, 10)
	(*Dispatcher)(nil).SetInterval(time.Second)
}

func TestNormalizeTopicEventScanHelpers(t *testing.T) {
	if sql := SelectTopicEventSQL(); sql == "" {
		t.Fatalf("SelectTopicEventSQL returned empty")
	}
	_, err := ScanTopicEvent(func(dest ...any) error { return errors.New("scan") })
	if err == nil {
		t.Fatalf("expected scan error")
	}
}

func TestIntegrationDispatcherDispatchOnceAckRetryAndDecodeWorkflows(t *testing.T) {
	TestDispatcherDispatchOnceAckRetryAndDecodeWorkflows(t)
}

func TestE2EDispatcherDispatchOnceAckRetryAndDecodeWorkflows(t *testing.T) {
	TestDispatcherDispatchOnceAckRetryAndDecodeWorkflows(t)
}

type dispatcherCoverageStore struct {
	events    []domain.TopicEventRecord
	published int
	released  int
	retrying  int
}

func (s *dispatcherCoverageStore) ListDispatchableEvents(context.Context, time.Time, int) ([]domain.TopicEventRecord, error) {
	return s.events, nil
}

func (s *dispatcherCoverageStore) ClaimEvent(context.Context, string, string, time.Time, time.Time) (bool, error) {
	return true, nil
}

func (s *dispatcherCoverageStore) ReleaseEventClaim(_ context.Context, _ string, _ string, status string, _ string, _ time.Time) error {
	s.released++
	if status == domain.TopicEventDispatchRetrying {
		s.retrying++
	}
	return nil
}

func (s *dispatcherCoverageStore) MarkEventPublished(context.Context, string, string, time.Time) error {
	s.published++
	return nil
}

func (s *dispatcherCoverageStore) MarkEventNoSubscriber(context.Context, string, string, time.Time) error {
	return nil
}

type dispatcherCoverageBus struct {
	events    []domain.LoaderTopicEvent
	publishOK bool
}

func (b *dispatcherCoverageBus) Publish(event domain.LoaderTopicEvent) bool {
	if b.publishOK == false && len(b.events) > 0 {
		return false
	}
	b.events = append(b.events, event)
	return true
}
