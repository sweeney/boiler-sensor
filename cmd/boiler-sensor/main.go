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
	startupEvent := buildStartupEvent(snap)
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
	var prevNet *status.NetworkInfo
	var readyHeartbeatSent bool

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

			// Fire a heartbeat immediately the first time we're baselined so
			// new MQTT subscribers get real state without waiting for the next
			// scheduled interval.
			if !readyHeartbeatSent {
				readyHeartbeatSent = true
				log.Printf("baseline established, publishing ready heartbeat")
				hbEvent := mqtt.SystemEvent{
					Timestamp: t,
					Event:     "HEARTBEAT",
					Retained:  true,
				}
				if tracker != nil {
					if mqttStatus != nil {
						tracker.SetMQTTConnected(mqttStatus.IsConnected())
					}
					if net := readNetworkInfo(); net != nil {
						tracker.SetNetwork(net)
					}
					chState, hwState := detector.CurrentState()
					tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())
					snap := tracker.Snapshot()
					hbEvent.RawPayload = status.FormatStatusEvent(snap, "HEARTBEAT", "")
				}
				if err := publisher.PublishSystem(hbEvent); err != nil {
					log.Printf("ready heartbeat publish error: %v", err)
				}
			}

			// Check for scheduled heartbeat
			if hbData := detector.CheckHeartbeat(t, heartbeat); hbData != nil {
				mqttUp := "unknown"
				if mqttStatus != nil {
					if mqttStatus.IsConnected() {
						mqttUp = "connected"
					} else {
						mqttUp = "disconnected"
					}
				}
				netInfo := readNetworkInfo()
				netDesc := "unavailable"
				if netInfo != nil {
					if netInfo.SSID != "" {
						netDesc = fmt.Sprintf("%s/%s (wifi=%s ssid=%s)", netInfo.Status, netInfo.IP, netInfo.WifiStatus, netInfo.SSID)
					} else {
						netDesc = fmt.Sprintf("%s/%s", netInfo.Status, netInfo.IP)
					}
				}
				log.Printf("heartbeat: uptime=%v mqtt=%s net=%s ch_on=%d ch_off=%d hw_on=%d hw_off=%d",
					hbData.Uptime, mqttUp, netDesc,
					hbData.Counts.CHOn, hbData.Counts.CHOff, hbData.Counts.HWOn, hbData.Counts.HWOff)

				// Detect network state changes since last heartbeat
				if prevNet != nil && netInfo != nil && prevNet.Status != netInfo.Status {
					log.Printf("network: status changed since last heartbeat: %s → %s", prevNet.Status, netInfo.Status)
				}
				if prevNet != nil && netInfo != nil && prevNet.WifiStatus != netInfo.WifiStatus {
					log.Printf("network: wifi status changed since last heartbeat: %s → %s", prevNet.WifiStatus, netInfo.WifiStatus)
				}
				prevNet = netInfo

				hbEvent := mqtt.SystemEvent{
					Timestamp: hbData.Timestamp,
					Event:     "HEARTBEAT",
					Retained:  true,
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

func buildStartupEvent(snap status.Snapshot) mqtt.SystemEvent {
	return mqtt.SystemEvent{
		Timestamp:  snap.Now,
		Event:      "STARTUP",
		RawPayload: status.FormatStatusEvent(snap, "STARTUP", ""),
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
