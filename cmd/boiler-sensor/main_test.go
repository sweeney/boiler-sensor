package main

import (
	"testing"

	"github.com/sweeney/boiler-sensor/internal/mqtt"
)

// TestEnvVarNames verifies the env var constants match what pi-helper writes
// to /run/pi-helper.env. If pi-helper changes its var names, this test fails
// and we update the constants — not the other way around.
func TestEnvVarNames(t *testing.T) {
	// These are the canonical names from pi-helper.
	want := map[string]string{
		"NETWORK_TYPE":        envNetworkType,
		"NETWORK_IP":          envNetworkIP,
		"NETWORK_STATUS":      envNetworkStatus,
		"NETWORK_GATEWAY":     envNetworkGateway,
		"NETWORK_WIFI_STATUS": envNetworkWifiStatus,
		"NETWORK_WIFI_SSID":   envNetworkWifiSSID,
	}
	for canonical, got := range want {
		if got != canonical {
			t.Errorf("env var constant: got %q, want %q", got, canonical)
		}
	}
}

func TestReadNetworkInfoAllSet(t *testing.T) {
	t.Setenv(envNetworkType, "wifi")
	t.Setenv(envNetworkIP, "192.168.1.100")
	t.Setenv(envNetworkStatus, "connected")
	t.Setenv(envNetworkGateway, "192.168.1.1")
	t.Setenv(envNetworkWifiStatus, "connected")
	t.Setenv(envNetworkWifiSSID, "MyNetwork")

	info := readNetworkInfo()
	if info == nil {
		t.Fatal("expected non-nil NetworkInfo")
	}

	want := &mqtt.NetworkInfo{
		Type:       "wifi",
		IP:         "192.168.1.100",
		Status:     "connected",
		Gateway:    "192.168.1.1",
		WifiStatus: "connected",
		SSID:       "MyNetwork",
	}

	if info.Type != want.Type {
		t.Errorf("Type: got %q, want %q", info.Type, want.Type)
	}
	if info.IP != want.IP {
		t.Errorf("IP: got %q, want %q", info.IP, want.IP)
	}
	if info.Status != want.Status {
		t.Errorf("Status: got %q, want %q", info.Status, want.Status)
	}
	if info.Gateway != want.Gateway {
		t.Errorf("Gateway: got %q, want %q", info.Gateway, want.Gateway)
	}
	if info.WifiStatus != want.WifiStatus {
		t.Errorf("WifiStatus: got %q, want %q", info.WifiStatus, want.WifiStatus)
	}
	if info.SSID != want.SSID {
		t.Errorf("SSID: got %q, want %q", info.SSID, want.SSID)
	}
}

func TestReadNetworkInfoNoneSet(t *testing.T) {
	info := readNetworkInfo()
	if info != nil {
		t.Errorf("expected nil when NETWORK_STATUS is unset, got %+v", info)
	}
}

func TestReadNetworkInfoPartial(t *testing.T) {
	t.Setenv(envNetworkStatus, "connected")

	info := readNetworkInfo()
	if info == nil {
		t.Fatal("expected non-nil NetworkInfo when NETWORK_STATUS is set")
	}

	if info.Status != "connected" {
		t.Errorf("Status: got %q, want %q", info.Status, "connected")
	}
	if info.Type != "" {
		t.Errorf("Type: got %q, want empty", info.Type)
	}
	if info.IP != "" {
		t.Errorf("IP: got %q, want empty", info.IP)
	}
	if info.Gateway != "" {
		t.Errorf("Gateway: got %q, want empty", info.Gateway)
	}
	if info.WifiStatus != "" {
		t.Errorf("WifiStatus: got %q, want empty", info.WifiStatus)
	}
	if info.SSID != "" {
		t.Errorf("SSID: got %q, want empty", info.SSID)
	}
}
