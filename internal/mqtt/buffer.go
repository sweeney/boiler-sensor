package mqtt

import "log"

// bufferedMsg stores a serialized MQTT message for replay after reconnection.
type bufferedMsg struct {
	topic    string
	payload  []byte
	qos      byte
	retained bool
}

// ringBuffer is a fixed-capacity FIFO that stores messages while disconnected.
// Not safe for concurrent use — caller must synchronize.
type ringBuffer struct {
	buf      []bufferedMsg
	capacity int
	head     int // next write position
	count    int
	overflow bool // true if any message was dropped since last drain
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		buf:      make([]bufferedMsg, capacity),
		capacity: capacity,
	}
}

func (r *ringBuffer) push(msg bufferedMsg) {
	if r.count == r.capacity {
		if !r.overflow {
			log.Printf("mqtt: buffer full (%d messages), dropping oldest", r.capacity)
			r.overflow = true
		}
		// Overwrite oldest: head is already pointing at it
		r.buf[r.head] = msg
		r.head = (r.head + 1) % r.capacity
		// count stays at capacity
		return
	}
	r.buf[r.head] = msg
	r.head = (r.head + 1) % r.capacity
	r.count++
}

func (r *ringBuffer) drainAll() []bufferedMsg {
	if r.count == 0 {
		return nil
	}

	result := make([]bufferedMsg, r.count)
	// Oldest item is at (head - count) mod capacity
	start := (r.head - r.count + r.capacity) % r.capacity
	for i := 0; i < r.count; i++ {
		result[i] = r.buf[(start+i)%r.capacity]
	}

	r.count = 0
	r.head = 0
	r.overflow = false
	return result
}

func (r *ringBuffer) len() int {
	return r.count
}
