package registry

import (
	"fmt"
	"testing"
	"time"
)

func TestRecordRequestRingBufferBounds(t *testing.T) {
	r := New(3)
	for i := range 5 {
		r.RecordRequest(RequestRecord{Box: "dev", URL: fmt.Sprintf("http://x/%d", i)})
	}
	got := r.Snapshot().Requests
	if len(got) != 3 {
		t.Fatalf("want 3 requests retained, got %d", len(got))
	}
	// Oldest two dropped; most recent three retained in chronological order.
	for i, rec := range got {
		want := fmt.Sprintf("http://x/%d", i+2)
		if rec.URL != want {
			t.Fatalf("request[%d].URL = %q, want %q", i, rec.URL, want)
		}
	}
}

func TestSetBoxStatusReplacesByName(t *testing.T) {
	r := New(10)
	r.SetBoxStatus(BoxStatus{Name: "dev", State: "connecting"})
	r.SetBoxStatus(BoxStatus{Name: "prod", State: "connected"})
	r.SetBoxStatus(BoxStatus{Name: "dev", State: "connected"})

	boxes := r.Snapshot().Boxes
	if len(boxes) != 2 {
		t.Fatalf("want 2 boxes, got %d: %+v", len(boxes), boxes)
	}
	byName := map[string]BoxStatus{}
	for _, b := range boxes {
		byName[b.Name] = b
	}
	if byName["dev"].State != "connected" {
		t.Fatalf("dev state = %q, want connected", byName["dev"].State)
	}
}

func TestResetBoxesClearsStatusKeepsRequests(t *testing.T) {
	r := New(10)
	r.SetBoxStatus(BoxStatus{Name: "dev", State: "connected"})
	r.RecordRequest(RequestRecord{Box: "dev", URL: "http://x/1"})
	r.ResetBoxes()

	snap := r.Snapshot()
	if len(snap.Boxes) != 0 {
		t.Fatalf("expected boxes cleared, got %+v", snap.Boxes)
	}
	if len(snap.Requests) != 1 {
		t.Fatalf("requests should be retained, got %d", len(snap.Requests))
	}
}

func TestSubscribeReceivesRequestAndStatusEvents(t *testing.T) {
	r := New(10)
	ch, cancel := r.Subscribe()
	defer cancel()

	r.RecordRequest(RequestRecord{Box: "dev", URL: "http://x/1", Outcome: "launched"})
	r.SetBoxStatus(BoxStatus{Name: "dev", State: "connected"})

	got := map[string]bool{}
	timeout := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got[ev.Kind] = true
		case <-timeout:
			t.Fatalf("did not receive both events; got %v", got)
		}
	}
	if !got["request"] || !got["status"] {
		t.Fatalf("missing event kinds: %v", got)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	r := New(10)
	ch, cancel := r.Subscribe()
	cancel()

	// Channel should be closed by cancel.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after cancel")
	}

	// Recording after cancel must not panic (no send on closed channel).
	r.RecordRequest(RequestRecord{Box: "dev", URL: "http://x/2"})
}

func TestSlowSubscriberDoesNotBlockProducer(t *testing.T) {
	r := New(10)
	_, cancel := r.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for range 1000 {
			r.RecordRequest(RequestRecord{Box: "dev", URL: "http://x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer blocked on a slow subscriber")
	}
}
