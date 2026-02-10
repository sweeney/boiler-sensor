// Command boiler-sensor monitors GPIO inputs and publishes heating state changes to MQTT.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sweeney/boiler-sensor/internal/gpio"
	"github.com/sweeney/boiler-sensor/internal/logic"
	"github.com/sweeney/boiler-sensor/internal/mqtt"
	"github.com/sweeney/boiler-sensor/internal/status"
	"github.com/sweeney/boiler-sensor/internal/web"
)

func main() {
	poll := flag.Duration("poll", 100*time.Millisecond, "GPIO polling interval")
	debounce := flag.Duration("debounce", 250*time.Millisecond, "Debounce duration")
	broker := flag.String("broker", "tcp://192.168.1.200:1883", "MQTT broker address")
	heartbeat := flag.Duration("heartbeat", 15*time.Minute, "Heartbeat interval (0 to disable)")
	pinCH := flag.Int("pin-ch", gpio.DefaultPinCH, "BCM pin number for Central Heating")
	pinHW := flag.Int("pin-hw", gpio.DefaultPinHW, "BCM pin number for Hot Water")
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
	// Initialize GPIO
	gpioReader, err := gpio.NewRealReader(pinCH, pinHW)
	if err != nil {
		return fmt.Errorf("init gpio: %w", err)
	}
	defer gpioReader.Close()

	// Print state mode
	if printState {
		ch, hw, err := gpioReader.Read()
		if err != nil {
			return fmt.Errorf("read gpio: %w", err)
		}
		chState := stateString(ch)
		hwState := stateString(hw)
		fmt.Printf("CH: %s, HW: %s\n", chState, hwState)
		return nil
	}

	// Initialize MQTT
	publisher := mqtt.NewRealPublisher(broker)
	defer publisher.Close()

	// Initialize status tracker (before STARTUP so snapshot is available)
	tracker := status.NewTracker(time.Now(), status.Config{
		PollMs:      poll.Milliseconds(),
		DebounceMs:  debounce.Milliseconds(),
		HeartbeatMs: heartbeat.Milliseconds(),
		Broker:      broker,
		HTTPPort:    httpAddr,
		WSBroker:    wsBroker,
	})
	if net := readNetworkInfo(); net != nil {
		tracker.SetNetwork(net)
	}

	// Publish startup event with full status snapshot
	snap := tracker.Snapshot()
	startupEvent := mqtt.SystemEvent{
		Timestamp:  snap.Now,
		Event:      "STARTUP",
		Retained:   true,
		RawPayload: status.FormatStatusEvent(snap, "STARTUP", ""),
	}
	if err := publisher.PublishSystem(startupEvent); err != nil {
		log.Printf("failed to publish startup event: %v", err)
	} else {
		log.Printf("published startup event")
	}

	// Start HTTP status server
	if httpAddr != "" {
		srv := web.New(httpAddr, tracker)
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("http server error: %v", err)
			}
		}()
		defer srv.Shutdown(context.Background())
		log.Printf("http status server listening on %s", httpAddr)
	}

	log.Printf("started: poll=%v debounce=%v broker=%s heartbeat=%v", poll, debounce, broker, heartbeat)

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	return runLoop(gpioReader, publisher, publisher, tracker, debounce, heartbeat, time.Now, ticker.C, sigCh)
}

func runLoop(gpioReader gpio.Reader, publisher mqtt.Publisher, mqttStatus mqtt.ConnectionStatus, tracker *status.Tracker, debounce, heartbeat time.Duration, now func() time.Time, tick <-chan time.Time, sig <-chan os.Signal) error {
	startTime := now()
	detector := logic.NewDetector(debounce, startTime)

	for {
		select {
		case s := <-sig:
			log.Printf("received %v, shutting down", s)
			signalName := "UNKNOWN"
			if s == syscall.SIGINT {
				signalName = "SIGINT"
			} else if s == syscall.SIGTERM {
				signalName = "SIGTERM"
			}
			event := mqtt.SystemEvent{
				Timestamp: now(),
				Event:     "SHUTDOWN",
				Reason:    signalName,
				Retained:  true,
			}
			if tracker != nil {
				if mqttStatus != nil {
					tracker.SetMQTTConnected(mqttStatus.IsConnected())
				}
				snap := tracker.Snapshot()
				event.RawPayload = status.FormatStatusEvent(snap, "SHUTDOWN", signalName)
			}
			if err := publisher.PublishSystem(event); err != nil {
				log.Printf("failed to publish shutdown event: %v", err)
			} else {
				log.Printf("published shutdown event")
			}
			return nil

		case <-tick:
			t := now()
			ch, hw, err := gpioReader.Read()
			if err != nil {
				log.Printf("gpio read error: %v", err)
				continue
			}

			events := detector.Process(logic.Input{
				CH:   ch,
				HW:   hw,
				Time: t,
			})

			for _, event := range events {
				log.Printf("event: %s (CH=%s HW=%s)", event.Type, event.CHState, event.HWState)
				if err := publisher.Publish(event); err != nil {
					log.Printf("publish error: %v", err)
					// Don't crash on publish failure
				}
			}

			if !detector.IsBaselined() {
				// Still waiting for baseline
				continue
			}

			// Check for heartbeat
			if hbData := detector.CheckHeartbeat(t, heartbeat); hbData != nil {
				log.Printf("heartbeat: uptime=%v ch_on=%d ch_off=%d hw_on=%d hw_off=%d",
					hbData.Uptime, hbData.Counts.CHOn, hbData.Counts.CHOff, hbData.Counts.HWOn, hbData.Counts.HWOff)

				hbEvent := mqtt.SystemEvent{
					Timestamp: hbData.Timestamp,
					Event:     "HEARTBEAT",
				}
				if tracker != nil {
					if mqttStatus != nil {
						tracker.SetMQTTConnected(mqttStatus.IsConnected())
					}
					// Refresh network info for heartbeat
					if net := readNetworkInfo(); net != nil {
						tracker.SetNetwork(net)
					}
					chState, hwState := detector.CurrentState()
					tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())
					snap := tracker.Snapshot()
					hbEvent.RawPayload = status.FormatStatusEvent(snap, "HEARTBEAT", "")
				}
				if err := publisher.PublishSystem(hbEvent); err != nil {
					log.Printf("heartbeat publish error: %v", err)
				}
			}

			// Update status tracker for HTTP/LED consumers
			if tracker != nil {
				chState, hwState := detector.CurrentState()
				tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())
				if mqttStatus != nil {
					tracker.SetMQTTConnected(mqttStatus.IsConnected())
				}
			}
		}
	}
}

// pi-helper env var names (written to /run/pi-helper.env).
const (
	envNetworkType       = "NETWORK_TYPE"
	envNetworkIP         = "NETWORK_IP"
	envNetworkStatus     = "NETWORK_STATUS"
	envNetworkGateway    = "NETWORK_GATEWAY"
	envNetworkWifiStatus = "NETWORK_WIFI_STATUS"
	envNetworkWifiSSID   = "NETWORK_WIFI_SSID"
)

func readNetworkInfo() *status.NetworkInfo {
	s := os.Getenv(envNetworkStatus)
	if s == "" {
		return nil
	}
	return &status.NetworkInfo{
		Type:       os.Getenv(envNetworkType),
		IP:         os.Getenv(envNetworkIP),
		Status:     s,
		Gateway:    os.Getenv(envNetworkGateway),
		WifiStatus: os.Getenv(envNetworkWifiStatus),
		SSID:       os.Getenv(envNetworkWifiSSID),
	}
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
