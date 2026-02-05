//go:build linux

package gpio

import (
	"fmt"

	"github.com/warthog618/go-gpiocdev"
)

// RealReader reads GPIO from actual hardware using Linux GPIO character device.
type RealReader struct {
	chip  *gpiocdev.Chip
	chPin *gpiocdev.Line
	hwPin *gpiocdev.Line
}

// NewRealReader creates a GPIO reader for actual Raspberry Pi hardware.
func NewRealReader(pinCH, pinHW int) (*RealReader, error) {
	chip, err := gpiocdev.NewChip("gpiochip0")
	if err != nil {
		return nil, fmt.Errorf("open gpio chip: %w", err)
	}

	// Request lines as input with pull-down to match Pi boot defaults.
	// This ensures consistent behavior with external optocoupler modules.
	chLine, err := chip.RequestLine(pinCH, gpiocdev.AsInput, gpiocdev.WithPullDown)
	if err != nil {
		chip.Close()
		return nil, fmt.Errorf("request CH pin %d: %w", pinCH, err)
	}

	hwLine, err := chip.RequestLine(pinHW, gpiocdev.AsInput, gpiocdev.WithPullDown)
	if err != nil {
		chLine.Close()
		chip.Close()
		return nil, fmt.Errorf("request HW pin %d: %w", pinHW, err)
	}

	return &RealReader{
		chip:  chip,
		chPin: chLine,
		hwPin: hwLine,
	}, nil
}

// Read returns the logical states of CH and HW.
// Inverts raw GPIO: raw active (1) = logical OFF, raw inactive (0) = logical ON.
func (r *RealReader) Read() (bool, bool, error) {
	chRaw, err := r.chPin.Value()
	if err != nil {
		return false, false, fmt.Errorf("read CH pin: %w", err)
	}

	hwRaw, err := r.hwPin.Value()
	if err != nil {
		return false, false, fmt.Errorf("read HW pin: %w", err)
	}

	// Invert: raw active (1) = OFF, raw inactive (0) = ON
	chOn := chRaw == 0
	hwOn := hwRaw == 0

	return chOn, hwOn, nil
}

// Close releases GPIO resources.
// Reconfigures pins to input with pull-down (matching Pi boot defaults) before
// closing to ensure clean state for system shutdown/reboot.
func (r *RealReader) Close() error {
	var errs []error

	// Reconfigure pins to match Raspberry Pi boot defaults (input with pull-down).
	// This prevents boot issues when external hardware (like optocouplers) is
	// connected and might hold pins in unexpected states during early boot.
	if r.chPin != nil {
		if err := r.chPin.Reconfigure(gpiocdev.AsInput, gpiocdev.WithPullDown); err != nil {
			errs = append(errs, fmt.Errorf("reconfigure CH pin: %w", err))
		}
		if err := r.chPin.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close CH pin: %w", err))
		}
	}
	if r.hwPin != nil {
		if err := r.hwPin.Reconfigure(gpiocdev.AsInput, gpiocdev.WithPullDown); err != nil {
			errs = append(errs, fmt.Errorf("reconfigure HW pin: %w", err))
		}
		if err := r.hwPin.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close HW pin: %w", err))
		}
	}
	if r.chip != nil {
		if err := r.chip.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close chip: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}
