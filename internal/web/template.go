package web

import (
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/sweeney/common/iot/status"
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
<title>{{.View.Title}}</title>
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
<h1>{{.View.Title}}{{if .Config.WSBroker}}<span id="live-dot" class="live-dot pending" title="connecting"></span>{{end}}</h1>

<h2>State</h2>
<table>
{{range .View.Channels}}<tr><th>{{.Label}}</th><td id="state-{{.Key}}" class="{{$s := index $.States .Key}}{{if eq $s "ON"}}on{{else if eq $s "OFF"}}off{{else}}unknown{{end}}">{{index $.States .Key}}</td></tr>
{{end}}<tr><th>Ready</th><td>{{if .Baselined}}yes{{else}}no{{end}}</td></tr>
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
{{range .CountRows}}<tr><th>{{.Label}}</th><td>{{.Count}}</td></tr>
{{end}}</table>

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
  var topic = "{{.View.EventTopic}}";
  var root = "{{.View.PayloadRoot}}";
  var keys = [{{range $i, $c := .View.Channels}}{{if $i}},{{end}}"{{$c.Key}}"{{end}}];
  var dot = document.getElementById("live-dot");

  function setState(key, state) {
    var el = document.getElementById("state-" + key);
    if (!el) return;
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
      var body = msg[root];
      if (body) {
        keys.forEach(function(k) {
          if (body[k] && body[k].state) setState(k, body[k].state);
        });
      }
    } catch (e) {}
  });
})();
</script>
{{end}}
</body>
</html>
`

// countRow is one row in the Event Counts table.
type countRow struct {
	Label string
	Count int
}

func renderHTML(w io.Writer, snap status.Snapshot, view View) {
	// Per-channel display states, keyed by channel key, defaulting to UNKNOWN.
	states := make(map[string]string, len(view.Channels))
	for _, c := range view.Channels {
		s := string(snap.States[c.Key])
		if s == "" {
			s = "UNKNOWN"
		}
		states[c.Key] = s
	}

	// Event-count rows in profile order: "<Label> ON" / "<Label> OFF".
	var countRows []countRow
	for _, c := range snap.Profile.Channels {
		countRows = append(countRows,
			countRow{Label: c.Label + " ON", Count: snap.GenericCounts[c.OnEvent]},
			countRow{Label: c.Label + " OFF", Count: snap.GenericCounts[c.OffEvent]},
		)
	}

	// Snapshot has Uptime() method but template needs a Duration field.
	data := struct {
		status.Snapshot
		Uptime    time.Duration
		View      View
		States    map[string]string
		CountRows []countRow
	}{
		Snapshot:  snap,
		Uptime:    snap.Uptime(),
		View:      view,
		States:    states,
		CountRows: countRows,
	}
	indexTmpl.Execute(w, data)
}
