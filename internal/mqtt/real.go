package mqtt

import (
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/sweeney/boiler-sensor/internal/logic"
)

// RealPublisher publishes to an actual MQTT broker.
type RealPublisher struct {
	client paho.Client
	topic  string
}

// NewRealPublisher creates a publisher connected to the given broker.
func NewRealPublisher(broker string) (*RealPublisher, error) {
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID("boiler-sensor").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("connection timeout")
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connect to broker: %w", err)
	}

	return &RealPublisher{
		client: client,
		topic:  Topic,
	}, nil
}

// Publish sends a heating event to the MQTT broker.
func (p *RealPublisher) Publish(event logic.Event) error {
	payload, err := FormatPayload(event)
	if err != nil {
		return fmt.Errorf("format payload: %w", err)
	}

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

	// QoS 1 (at-least-once) for shutdown events - we want to ensure delivery
	token := p.client.Publish(TopicSystem, 1, false, payload)
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
	p.client.Disconnect(1000) // 1 second timeout
	return nil
}
