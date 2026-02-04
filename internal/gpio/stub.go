//go:build !linux

package gpio

import "errors"

// RealReader is not available on non-Linux platforms.
type RealReader struct{}

// NewRealReader returns an error on non-Linux platforms.
func NewRealReader() (*RealReader, error) {
	return nil, errors.New("gpio: not supported on this platform (requires Linux)")
}

// Read is not implemented on non-Linux platforms.
func (r *RealReader) Read() (bool, bool, error) {
	return false, false, errors.New("gpio: not supported")
}

// Close is not implemented on non-Linux platforms.
func (r *RealReader) Close() error {
	return nil
}
