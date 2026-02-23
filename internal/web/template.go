package web

import (
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/sweeney/boiler-sensor/internal/status"
)

var indexTmpl = template.Must(template.New("index").Funcs(template.FuncMap{
	"uptime": func(d time.Duration) string {
		d = d.Truncate(time.Second)
		days := int(d.Hours()) / 24
		h := int(d.Hours()) % 24
		m := int(d.Minutes()) % 60
		s := int(d.Seconds()) % 60
		if days > 0 {
			return fmt.Sprintf("%dd %dh %dm %ds", days, h, m, s)
		}
		if h > 0 {
			return fmt.Sprintf("%dh %dm %ds", h, m, s)
		}
		if m > 0 {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%ds", s)
	},
	"stateOrUnknown": func(s string) string {
		if s == "" {
			return "UNKNOWN"
		}
		return s
	},
}).Parse(indexHTML))

const indexHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Boiler Sensor</title>
<style>
body { font-family: monospace; max-width: 600px; margin: 2em auto; padding: 0 1em; }
h1 { font-size: 1.4em; }
table { border-collapse: collapse; width: 100%; margin: 1em 0; }
td, th { text-align: left; padding: 4px 8px; border-bottom: 1px solid #ddd; }
th { width: 40%; }
.on { color: green; font-weight: bold; }
.off { color: #888; }
.unknown { color: orange; }
.connected { color: green; }
.disconnected { color: red; }
.live-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-left: 6px; vertical-align: middle; }
.live-dot.ok { background: green; }
.live-dot.err { background: red; }
.live-dot.pending { background: orange; }
</style>
</head>
<body>
<h1>Boiler Sensor{{if .Config.WSBroker}}<span id="live-dot" class="live-dot pending" title="connecting"></span>{{end}}</h1>

<h2>State</h2>
<table>
<tr><th>Central Heating</th><td id="ch-state" class="{{if eq (stateOrUnknown (printf "%s" .CH)) "ON"}}on{{else if eq (stateOrUnknown (printf "%s" .CH)) "OFF"}}off{{else}}unknown{{end}}">{{stateOrUnknown (printf "%s" .CH)}}</td></tr>
<tr><th>Hot Water</th><td id="hw-state" class="{{if eq (stateOrUnknown (printf "%s" .HW)) "ON"}}on{{else if eq (stateOrUnknown (printf "%s" .HW)) "OFF"}}off{{else}}unknown{{end}}">{{stateOrUnknown (printf "%s" .HW)}}</td></tr>
<tr><th>Ready</th><td>{{if .Baselined}}yes{{else}}no{{end}}</td></tr>
</table>

<h2>Connectivity</h2>
<table>
<tr><th>MQTT</th><td class="{{if .MQTTConnected}}connected{{else}}disconnected{{end}}">{{if .MQTTConnected}}connected{{else}}disconnected{{end}}</td></tr>
<tr><th>Broker</th><td>{{.Config.Broker}}</td></tr>
{{if .Network}}<tr><th>Network</th><td>{{.Network.Status}} ({{.Network.Type}}{{if .Network.SSID}} — {{.Network.SSID}}{{end}})</td></tr>
<tr><th>IP</th><td>{{.Network.IP}}</td></tr>{{end}}
</table>

<h2>Event Counts</h2>
<table>
<tr><th>CH ON</th><td>{{.Counts.CHOn}}</td></tr>
<tr><th>CH OFF</th><td>{{.Counts.CHOff}}</td></tr>
<tr><th>HW ON</th><td>{{.Counts.HWOn}}</td></tr>
<tr><th>HW OFF</th><td>{{.Counts.HWOff}}</td></tr>
</table>

<h2>System</h2>
<table>
<tr><th>Uptime</th><td>{{uptime .Uptime}}</td></tr>
<tr><th>Started</th><td>{{.StartTime.UTC.Format "2006-01-02T15:04:05Z"}}</td></tr>
<tr><th>Poll</th><td>{{.Config.PollMs}}ms</td></tr>
<tr><th>Debounce</th><td>{{.Config.DebounceMs}}ms</td></tr>
<tr><th>Heartbeat</th><td>{{if eq .Config.HeartbeatMs 0}}disabled{{else}}{{.Config.HeartbeatMs}}ms{{end}}</td></tr>
<tr><th>HTTP</th><td>{{.Config.HTTPPort}}</td></tr>
</table>

<p><a href="/index.json">JSON</a></p>
{{if .Config.WSBroker}}
<script src="/mqtt.min.js"></script>
<script>
(function() {
  var broker = "{{.Config.WSBroker}}";
  var topic = "energy/boiler/sensor/events";
  var dot = document.getElementById("live-dot");
  var chEl = document.getElementById("ch-state");
  var hwEl = document.getElementById("hw-state");

  function setState(el, state) {
    el.textContent = state;
    el.className = state === "ON" ? "on" : state === "OFF" ? "off" : "unknown";
  }

  function setDot(cls, title) {
    dot.className = "live-dot " + cls;
    dot.title = title;
  }

  var client = mqtt.connect(broker, { reconnectPeriod: 5000 });

  client.on("connect", function() {
    setDot("ok", "live");
    client.subscribe(topic);
  });

  client.on("reconnect", function() {
    setDot("pending", "reconnecting");
  });

  client.on("offline", function() {
    setDot("err", "offline");
  });

  client.on("error", function() {
    setDot("err", "error");
  });

  client.on("message", function(t, payload) {
    try {
      var msg = JSON.parse(payload.toString());
      if (msg.boiler) {
        setState(chEl, msg.boiler.ch.state);
        setState(hwEl, msg.boiler.hw.state);
      }
    } catch (e) {}
  });
})();
</script>
{{end}}
</body>
</html>
`

func renderHTML(w io.Writer, snap status.Snapshot) {
	// Snapshot has Uptime() method but template needs a Duration field.
	data := struct {
		status.Snapshot
		Uptime time.Duration
	}{
		Snapshot: snap,
		Uptime:   snap.Uptime(),
	}
	indexTmpl.Execute(w, data)
}
