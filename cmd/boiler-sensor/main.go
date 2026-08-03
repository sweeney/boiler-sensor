// Command boiler-sensor monitors GPIO inputs and publishes heating state changes to MQTT.
//
// The engine (debounce, baseline, heartbeat, MQTT plumbing, and the daemon
// runtime) lives in the shared module github.com/sweeney/common/iot. This binary
// only supplies the boiler's SensorProfile (channels, pins, events, topics,
// labels), serves its own status page, and calls iot.Run.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/sweeney/boiler-sensor/internal/web"
	"github.com/sweeney/common/iot"
	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

// Default boiler wiring and topics. These are the ONLY place boiler vocabulary
// (pins, CH/HW, topics) is defined now that the engine is sensor-agnostic.
const (
	defaultPinCH      = 26
	defaultPinHW      = 16
	boilerEventTopic  = "energy/boiler/sensor/events"
	boilerSystemTopic = "energy/boiler/sensor/system"
)

// boilerProfile builds the boiler's sensor profile. This is the per-binary
// domain config (channels, pins, events, topics, labels); everything that says
// "boiler/CH/HW" lives here, not in the shared engine packages.
//
// ActiveLow: the boiler inputs are optocoupler-wired, so a raw active line means
// logical OFF. The inversion is applied in package iot/gpio per ActiveLow.
func boilerProfile(pinCH, pinHW int) profile.SensorProfile {
	return profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "ch", Label: "Central Heating", Pin: pinCH, ActiveLow: true, OnEvent: "CH_ON", OffEvent: "CH_OFF"},
			{Key: "hw", Label: "Hot Water", Pin: pinHW, ActiveLow: true, OnEvent: "HW_ON", OffEvent: "HW_OFF"},
		},
		EventTopic:  boilerEventTopic,
		SystemTopic: boilerSystemTopic,
		PayloadRoot: "boiler",
		WebTitle:    "Boiler Sensor",
	}
}

func main() {
	poll := flag.Duration("poll", 100*time.Millisecond, "GPIO polling interval")
	debounce := flag.Duration("debounce", 250*time.Millisecond, "Debounce duration")
	broker := flag.String("broker", "tcp://192.168.1.200:1883", "MQTT broker address")
	heartbeat := flag.Duration("heartbeat", 15*time.Minute, "Heartbeat interval (0 to disable)")
	pinCH := flag.Int("pin-ch", defaultPinCH, "BCM pin number for Central Heating")
	pinHW := flag.Int("pin-hw", defaultPinHW, "BCM pin number for Hot Water")
	printState := flag.Bool("print-state", false, "Print current state and exit")
	httpAddr := flag.String("http", ":80", "HTTP status address (empty to disable)")
	wsBroker := flag.String("ws-broker", "=broker", `MQTT websocket URL for live UI ("=broker" derives from --broker, "off" disables)`)

	flag.Parse()

	ws := resolveWSBroker(*wsBroker, *broker)
	if err := run(*poll, *debounce, *broker, *heartbeat, *pinCH, *pinHW, *printState, *httpAddr, ws); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run(poll, debounce time.Duration, broker string, heartbeat time.Duration, pinCH, pinHW int, printState bool, httpAddr, wsBroker string) error {
	prof := boilerProfile(pinCH, pinHW)

	// Initialize GPIO from the profile's channels (pins + per-channel inversion).
	gpioReader, err := gpio.NewRealChannelReader(prof.Channels)
	if err != nil {
		return fmt.Errorf("init gpio: %w", err)
	}
	defer gpioReader.Close()

	// Print state mode.
	if printState {
		vals, err := gpioReader.ReadChannels()
		if err != nil {
			return fmt.Errorf("read gpio: %w", err)
		}
		fmt.Printf("CH: %s, HW: %s\n", stateString(vals[0]), stateString(vals[1]))
		return nil
	}

	// Initialize MQTT.
	publisher := mqtt.NewRealPublisherForProfile(broker, prof, "boiler-sensor")
	defer publisher.Close()

	// Status tracker shared by the runtime and the HTTP status page.
	tracker := status.NewTrackerForProfile(prof, time.Now(), status.Config{
		PollMs:      poll.Milliseconds(),
		DebounceMs:  debounce.Milliseconds(),
		HeartbeatMs: heartbeat.Milliseconds(),
		Broker:      broker,
		HTTPPort:    httpAddr,
		WSBroker:    wsBroker,
	})

	// Serve this service's own status page (presentation is not engine).
	if httpAddr != "" {
		srv := web.NewWithView(httpAddr, tracker, web.ViewForProfile(prof))
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("http server error: %v", err)
			}
		}()
		defer srv.Shutdown(context.Background())
		log.Printf("http status server listening on %s", httpAddr)
	}

	// Hand off to the shared daemon runtime (poll/debounce/publish/lifecycle).
	return iot.Run(iot.RunConfig{
		Profile:   prof,
		Reader:    gpioReader,
		Publisher: publisher,
		Tracker:   tracker,
		Debounce:  debounce,
		Heartbeat: heartbeat,
		Poll:      poll,
	})
}

func stateString(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// resolveWSBroker converts the --ws-broker flag value into a concrete URL.
// "=broker" derives ws://host:9001 from the TCP broker address; empty disables.
func resolveWSBroker(ws, broker string) string {
	if ws == "off" {
		return ""
	}
	if ws != "=broker" {
		return ws
	}
	u, err := url.Parse(broker)
	if err != nil {
		log.Printf("ws-broker: cannot parse --broker %q: %v", broker, err)
		return ""
	}
	u.Scheme = "ws"
	u.Host = u.Hostname() + ":9001"
	return u.String()
}
