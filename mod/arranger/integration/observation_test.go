package integration

import (
	"context"
	"errors"
	"testing"
	"time"
)

type observationSinkFunc func(context.Context, Observation) error

func (function observationSinkFunc) RecordObservation(ctx context.Context, observation Observation) error {
	return function(ctx, observation)
}

func TestObservationQueueDoesNotWaitForSink(t *testing.T) {
	release := make(chan struct{})
	delivered := make(chan Observation, 1)
	queue, err := NewObservationQueue(context.Background(), observationSinkFunc(func(_ context.Context, observation Observation) error {
		<-release
		delivered <- observation
		return nil
	}), ObservationQueueConfig{Capacity: 1, DeliveryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()

	observation := validObservation()
	started := time.Now()
	if !queue.Enqueue(observation) {
		t.Fatal("first observation was not accepted")
	}
	if time.Since(started) > 50*time.Millisecond {
		t.Fatal("enqueue waited for the observation sink")
	}

	close(release)
	select {
	case result := <-delivered:
		if result.ID != observation.ID {
			t.Fatalf("unexpected observation: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted observation was not delivered")
	}
}

func TestObservationQueueDropsSaturationAndCountsSinkFailure(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	queue, err := NewObservationQueue(context.Background(), observationSinkFunc(func(context.Context, Observation) error {
		started <- struct{}{}
		<-release
		return errors.New("unavailable")
	}), ObservationQueueConfig{Capacity: 1, DeliveryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	first := validObservation()
	if !queue.Enqueue(first) {
		t.Fatal("first observation was not accepted")
	}
	<-started
	second := validObservation()
	second.ID = "observation-2"
	if !queue.Enqueue(second) {
		t.Fatal("buffered observation was not accepted")
	}
	third := validObservation()
	third.ID = "observation-3"
	if queue.Enqueue(third) {
		t.Fatal("saturated observation queue accepted extra work")
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for queue.Stats().Failed == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stats := queue.Stats()
	if stats.Accepted != 2 || stats.Dropped != 1 || stats.Failed == 0 {
		t.Fatalf("unexpected observation queue stats: %+v", stats)
	}
	queue.Close()
}

func TestObservationQueueRejectsInvalidDocuments(t *testing.T) {
	queue, err := NewObservationQueue(context.Background(), observationSinkFunc(func(context.Context, Observation) error {
		t.Fatal("invalid observation reached sink")
		return nil
	}), ObservationQueueConfig{Capacity: 1, DeliveryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()

	observation := validObservation()
	observation.Version = "v2"
	if queue.Enqueue(observation) || queue.Stats().Dropped != 1 {
		t.Fatalf("invalid observation was not rejected: %+v", queue.Stats())
	}
}

func validObservation() Observation {
	return Observation{
		Version: ObservationVersion, ID: "observation-1", Type: "dns.query",
		OccurredAt: time.Now().UTC(), Attributes: map[string]string{"mode": "managed"},
	}
}
