package mqtt

import (
	"testing"
)

func TestRingBufferEmptyDrain(t *testing.T) {
	rb := newRingBuffer(10)
	got := rb.drainAll()
	if got != nil {
		t.Errorf("expected nil from empty drain, got %d items", len(got))
	}
}

func TestRingBufferPushAndDrain(t *testing.T) {
	rb := newRingBuffer(10)
	for i := 0; i < 5; i++ {
		rb.push(bufferedMsg{topic: "t", payload: []byte{byte(i)}})
	}

	got := rb.drainAll()
	if len(got) != 5 {
		t.Fatalf("expected 5 items, got %d", len(got))
	}
	for i := 0; i < 5; i++ {
		if got[i].payload[0] != byte(i) {
			t.Errorf("item %d: expected payload %d, got %d", i, i, got[i].payload[0])
		}
	}

	// Second drain should be empty
	got2 := rb.drainAll()
	if got2 != nil {
		t.Errorf("expected nil from second drain, got %d items", len(got2))
	}
}

func TestRingBufferFillToCapacity(t *testing.T) {
	cap := 10
	rb := newRingBuffer(cap)
	for i := 0; i < cap; i++ {
		rb.push(bufferedMsg{topic: "t", payload: []byte{byte(i)}})
	}

	got := rb.drainAll()
	if len(got) != cap {
		t.Fatalf("expected %d items, got %d", cap, len(got))
	}
	for i := 0; i < cap; i++ {
		if got[i].payload[0] != byte(i) {
			t.Errorf("item %d: expected payload %d, got %d", i, i, got[i].payload[0])
		}
	}
}

func TestRingBufferOverflow(t *testing.T) {
	cap := 5
	rb := newRingBuffer(cap)

	// Push cap+3 items (0..7), buffer should keep the most recent 5 (3..7)
	for i := 0; i < cap+3; i++ {
		rb.push(bufferedMsg{topic: "t", payload: []byte{byte(i)}})
	}

	got := rb.drainAll()
	if len(got) != cap {
		t.Fatalf("expected %d items, got %d", cap, len(got))
	}
	for i := 0; i < cap; i++ {
		want := byte(i + 3) // oldest 3 were dropped
		if got[i].payload[0] != want {
			t.Errorf("item %d: expected payload %d, got %d", i, want, got[i].payload[0])
		}
	}
}

func TestRingBufferMultipleCycles(t *testing.T) {
	rb := newRingBuffer(5)

	// Cycle 1: push 3, drain
	for i := 0; i < 3; i++ {
		rb.push(bufferedMsg{topic: "t", payload: []byte{byte(i)}})
	}
	got := rb.drainAll()
	if len(got) != 3 {
		t.Fatalf("cycle 1: expected 3 items, got %d", len(got))
	}

	// Cycle 2: push 4, drain
	for i := 10; i < 14; i++ {
		rb.push(bufferedMsg{topic: "t", payload: []byte{byte(i)}})
	}
	got = rb.drainAll()
	if len(got) != 4 {
		t.Fatalf("cycle 2: expected 4 items, got %d", len(got))
	}
	for i, msg := range got {
		want := byte(10 + i)
		if msg.payload[0] != want {
			t.Errorf("cycle 2 item %d: expected %d, got %d", i, want, msg.payload[0])
		}
	}
}

func TestRingBufferLen(t *testing.T) {
	rb := newRingBuffer(10)
	if rb.len() != 0 {
		t.Errorf("expected len 0, got %d", rb.len())
	}

	rb.push(bufferedMsg{topic: "t"})
	rb.push(bufferedMsg{topic: "t"})
	if rb.len() != 2 {
		t.Errorf("expected len 2, got %d", rb.len())
	}

	rb.drainAll()
	if rb.len() != 0 {
		t.Errorf("expected len 0 after drain, got %d", rb.len())
	}
}

func TestRingBufferPreservesFields(t *testing.T) {
	rb := newRingBuffer(10)
	rb.push(bufferedMsg{
		topic:    "energy/test",
		payload:  []byte(`{"test":true}`),
		qos:      1,
		retained: true,
	})

	got := rb.drainAll()
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].topic != "energy/test" {
		t.Errorf("topic: got %s, want energy/test", got[0].topic)
	}
	if string(got[0].payload) != `{"test":true}` {
		t.Errorf("payload: got %s", got[0].payload)
	}
	if got[0].qos != 1 {
		t.Errorf("qos: got %d, want 1", got[0].qos)
	}
	if !got[0].retained {
		t.Error("retained: got false, want true")
	}
}
