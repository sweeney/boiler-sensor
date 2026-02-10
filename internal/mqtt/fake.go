package mqtt

import (
	"github.com/sweeney/boiler-sensor/internal/logic"
)

// FakePublisher records published events for test assertions.
type FakePublisher struct {
	// Events contains all boiler events that were published.
	Events []logic.Event

	// Payloads contains the JSON payloads that were published.
	Payloads [][]byte

	// SystemEvents contains all system events that were published.
	SystemEvents []SystemEvent

	// SystemPayloads contains the JSON payloads for system events.
	SystemPayloads [][]byte

	// PublishError, if set, will be returned by Publish.
	PublishError error

	// PublishSystemError, if set, will be returned by PublishSystem.
	PublishSystemError error

	// Closed tracks if Close was called.
	Closed bool

	// Connected controls the return value of IsConnected.
	Connected bool
}

// NewFakePublisher creates a FakePublisher for testing.
func NewFakePublisher() *FakePublisher {
	return &FakePublisher{}
}

// Publish records the boiler event.
func (f *FakePublisher) Publish(event logic.Event) error {
	if f.PublishError != nil {
		return f.PublishError
	}

	f.Events = append(f.Events, event)

	payload, err := FormatPayload(event)
	if err != nil {
		return err
	}
	f.Payloads = append(f.Payloads, payload)

	return nil
}

// PublishSystem records the system event.
func (f *FakePublisher) PublishSystem(event SystemEvent) error {
	if f.PublishSystemError != nil {
		return f.PublishSystemError
	}

	f.SystemEvents = append(f.SystemEvents, event)

	payload, err := FormatSystemPayload(event)
	if err != nil {
		return err
	}
	f.SystemPayloads = append(f.SystemPayloads, payload)

	return nil
}

// Close marks the publisher as closed.
func (f *FakePublisher) Close() error {
	f.Closed = true
	return nil
}

// IsConnected reports whether the fake publisher is "connected".
func (f *FakePublisher) IsConnected() bool {
	return f.Connected
}

// Reset clears recorded events.
func (f *FakePublisher) Reset() {
	f.Events = nil
	f.Payloads = nil
	f.SystemEvents = nil
	f.SystemPayloads = nil
	f.Closed = false
	f.PublishError = nil
	f.PublishSystemError = nil
	f.Connected = false
}
