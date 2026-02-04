// Package gpio provides GPIO input reading with hardware abstraction.
// The real implementation uses Linux GPIO character device.
// The fake implementation allows testing without hardware.
package gpio

// Reader reads GPIO input states.
type Reader interface {
	// Read returns the logical states of CH and HW.
	// The raw GPIO values are inverted: raw active = logical OFF.
	// Returns (chOn, hwOn, error).
	Read() (bool, bool, error)

	// Close releases GPIO resources.
	Close() error
}

// Pin definitions (BCM numbering)
const (
	PinCH = 26 // Central Heating
	PinHW = 16 // Hot Water
)
