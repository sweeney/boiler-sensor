package gpio

import "errors"

// FakeReader is a test double that returns scripted GPIO values.
type FakeReader struct {
	// Samples contains scripted (chOn, hwOn) values to return.
	// Each call to Read() consumes the next sample.
	Samples []Sample

	// index tracks current position in Samples
	index int

	// Closed tracks if Close was called
	Closed bool

	// ReadError, if set, will be returned by Read()
	ReadError error
}

// Sample represents a single GPIO reading (already in logical form).
type Sample struct {
	CH bool // true = ON
	HW bool // true = ON
}

// NewFakeReader creates a FakeReader with the given samples.
func NewFakeReader(samples []Sample) *FakeReader {
	return &FakeReader{Samples: samples}
}

// Read returns the next scripted sample.
// If samples are exhausted, returns the last sample repeatedly.
func (f *FakeReader) Read() (bool, bool, error) {
	if f.ReadError != nil {
		return false, false, f.ReadError
	}

	if len(f.Samples) == 0 {
		return false, false, errors.New("no samples configured")
	}

	sample := f.Samples[f.index]
	if f.index < len(f.Samples)-1 {
		f.index++
	}

	return sample.CH, sample.HW, nil
}

// Close marks the reader as closed.
func (f *FakeReader) Close() error {
	f.Closed = true
	return nil
}

// Reset resets the reader to the beginning of samples.
func (f *FakeReader) Reset() {
	f.index = 0
	f.Closed = false
}
