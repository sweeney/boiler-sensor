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

// Default pin definitions (BCM numbering)
const (
	DefaultPinCH = 26 // Central Heating
	DefaultPinHW = 16 // Hot Water
)
