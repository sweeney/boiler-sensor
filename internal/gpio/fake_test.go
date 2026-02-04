package gpio

import (
	"errors"
	"testing"
)

func TestFakeReaderRead(t *testing.T) {
	samples := []Sample{
		{CH: true, HW: false},
		{CH: false, HW: true},
		{CH: true, HW: true},
	}

	f := NewFakeReader(samples)

	// Read first sample
	ch, hw, err := f.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != true || hw != false {
		t.Errorf("sample 0: expected (true, false), got (%v, %v)", ch, hw)
	}

	// Read second sample
	ch, hw, err = f.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != false || hw != true {
		t.Errorf("sample 1: expected (false, true), got (%v, %v)", ch, hw)
	}

	// Read third sample
	ch, hw, err = f.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != true || hw != true {
		t.Errorf("sample 2: expected (true, true), got (%v, %v)", ch, hw)
	}

	// Fourth read should repeat last sample
	ch, hw, err = f.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != true || hw != true {
		t.Errorf("sample 3 (repeat): expected (true, true), got (%v, %v)", ch, hw)
	}
}

func TestFakeReaderNoSamples(t *testing.T) {
	f := NewFakeReader(nil)

	_, _, err := f.Read()
	if err == nil {
		t.Error("expected error with no samples")
	}
}

func TestFakeReaderError(t *testing.T) {
	f := NewFakeReader([]Sample{{CH: true, HW: true}})
	f.ReadError = errors.New("simulated error")

	_, _, err := f.Read()
	if err == nil {
		t.Error("expected error to be returned")
	}
	if err.Error() != "simulated error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFakeReaderClose(t *testing.T) {
	f := NewFakeReader([]Sample{{CH: true, HW: true}})

	if f.Closed {
		t.Error("should not be closed initially")
	}

	err := f.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !f.Closed {
		t.Error("should be closed after Close()")
	}
}

func TestFakeReaderReset(t *testing.T) {
	samples := []Sample{
		{CH: true, HW: false},
		{CH: false, HW: true},
	}

	f := NewFakeReader(samples)

	// Consume first sample
	f.Read()

	// Reset
	f.Reset()

	// Should read first sample again
	ch, hw, _ := f.Read()
	if ch != true || hw != false {
		t.Errorf("after reset: expected (true, false), got (%v, %v)", ch, hw)
	}
}
