package mqtt

import (
	"fmt"
	"log"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/sweeney/boiler-sensor/internal/logic"
)

// RealPublisher publishes to an actual MQTT broker.
type RealPublisher struct {
	client        paho.Client
	topic         string
	mu            sync.Mutex
	connected     bool
	everConnected bool
	buf           *ringBuffer
}

// NewRealPublisher creates a publisher that connects to the given broker
// asynchronously. It returns immediately — Paho retries in the background.
func NewRealPublisher(broker string) *RealPublisher {
	willPayload, err := FormatSystemPayload(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
	})
	if err != nil {
		// FormatSystemPayload only fails on json.Marshal of a known struct —
		// unreachable in practice, but log and continue without a will.
		log.Printf("mqtt: failed to format will payload: %v", err)
	}

	p := &RealPublisher{
		topic: Topic,
		buf:   newRingBuffer(100),
	}

	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID("boiler-sensor").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetConnectionLostHandler(p.onConnectionLost).
		SetReconnectingHandler(p.onReconnecting).
		SetOnConnectHandler(p.onConnect)

	if willPayload != nil {
		opts.SetWill(TopicSystem, string(willPayload), 1, true)
	}

	p.client = paho.NewClient(opts)

	// Non-blocking connect — Paho retries via ConnectRetry.
	p.client.Connect()

	return p
}

func (p *RealPublisher) onConnectionLost(_ paho.Client, err error) {
	log.Printf("mqtt: connection lost: %v", err)
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
}

func (p *RealPublisher) onReconnecting(_ paho.Client, _ *paho.ClientOptions) {
	log.Printf("mqtt: reconnecting to broker")
}

func (p *RealPublisher) onConnect(_ paho.Client) {
	p.mu.Lock()
	wasEverConnected := p.everConnected
	p.connected = true
	p.everConnected = true
	msgs := p.buf.drainAll()
	p.mu.Unlock()

	n := 0
	for _, msg := range msgs {
		token := p.client.Publish(msg.topic, msg.qos, msg.retained, msg.payload)
		if !token.WaitTimeout(5 * time.Second) {
			log.Printf("mqtt: replay timeout for buffered message")
			continue
		}
		if err := token.Error(); err != nil {
			log.Printf("mqtt: replay error: %v", err)
			continue
		}
		n++
	}

	if len(msgs) > 0 {
		log.Printf("mqtt: connected to broker, replayed %d/%d buffered events", n, len(msgs))
	} else {
		log.Printf("mqtt: connected to broker")
	}

	// On reconnect (not first connect), publish a RECONNECTED event
	// to replace the stale LWT on the broker.
	if wasEverConnected {
		reconnectPayload, err := FormatSystemPayload(SystemEvent{
			Timestamp: time.Now(),
			Event:     "RECONNECTED",
		})
		if err != nil {
			log.Printf("mqtt: failed to format RECONNECTED payload: %v", err)
			return
		}
		token := p.client.Publish(TopicSystem, 1, true, reconnectPayload)
		if !token.WaitTimeout(5 * time.Second) {
			log.Printf("mqtt: RECONNECTED publish timeout")
		} else if err := token.Error(); err != nil {
			log.Printf("mqtt: RECONNECTED publish error: %v", err)
		}
	}
}

// IsConnected reports whether the MQTT client is currently connected.
func (p *RealPublisher) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

// Publish sends a boiler event to the MQTT broker.
func (p *RealPublisher) Publish(event logic.Event) error {
	payload, err := FormatPayload(event)
	if err != nil {
		return fmt.Errorf("format payload: %w", err)
	}

	p.mu.Lock()
	if !p.connected {
		p.buf.push(bufferedMsg{topic: p.topic, payload: payload, qos: 0, retained: false})
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// QoS 0 (at-most-once), not retained
	token := p.client.Publish(p.topic, 0, false, payload)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("publish timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	return nil
}

// PublishSystem sends a system lifecycle event to the MQTT broker.
func (p *RealPublisher) PublishSystem(event SystemEvent) error {
	payload, err := FormatSystemPayload(event)
	if err != nil {
		return fmt.Errorf("format system payload: %w", err)
	}

	p.mu.Lock()
	if !p.connected {
		p.buf.push(bufferedMsg{topic: TopicSystem, payload: payload, qos: 1, retained: event.Retained})
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// QoS 1 (at-least-once) for system events - we want to ensure delivery
	token := p.client.Publish(TopicSystem, 1, event.Retained, payload)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("publish system timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish system: %w", err)
	}

	return nil
}

// Close disconnects from the broker.
func (p *RealPublisher) Close() error {
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
	p.client.Disconnect(1000) // 1 second timeout
	return nil
}
