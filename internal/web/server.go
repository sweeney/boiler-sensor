// Package web provides this service's HTTP status page. The UI is
// service-specific presentation (not engine), so each sensor binary owns its
// own copy; it reads state from the shared iot/status tracker and renders N
// channels generically from a View derived from the sensor profile.
package web

import (
	"context"
	_ "embed"
	"net"
	"net/http"

	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

//go:embed static/mqtt.min.js
var mqttJS []byte

// ChannelView is one row in the status page's State table.
type ChannelView struct {
	// Key is the channel key, used to wire live MQTT updates (msg.<root>.<key>).
	Key string
	// Label is the human-readable row label, e.g. "Central Heating".
	Label string
}

// View holds the per-binary presentation vocabulary the status page renders
// with. It is derived from the sensor profile by the cmd/*-sensor binary.
type View struct {
	// Title is the page heading, e.g. "Boiler Sensor".
	Title string
	// Channels are the state rows, in profile order.
	Channels []ChannelView
	// PayloadRoot is the JSON root key of event payloads, e.g. "boiler".
	PayloadRoot string
	// EventTopic is the MQTT topic the live-update script subscribes to.
	EventTopic string
}

// ViewForProfile derives a presentation View from a sensor profile.
func ViewForProfile(p profile.SensorProfile) View {
	channels := make([]ChannelView, len(p.Channels))
	for i, c := range p.Channels {
		channels[i] = ChannelView{Key: c.Key, Label: c.Label}
	}
	return View{
		Title:       p.WebTitle,
		Channels:    channels,
		PayloadRoot: p.PayloadRoot,
		EventTopic:  p.EventTopic,
	}
}

// Server serves the status page over HTTP.
type Server struct {
	// Version names the running build (the commit) in /healthz.
	Version string

	httpServer *http.Server
	tracker    *status.Tracker
	view       View
}

// NewWithView creates a Server that reads state from the given tracker using
// the given presentation view.
func NewWithView(addr string, tracker *status.Tracker, view View) *Server {
	s := &Server{tracker: tracker, view: view}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/index.html", s.handleIndex)
	mux.HandleFunc("/index.json", s.handleJSON)
	mux.HandleFunc("/mqtt.min.js", handleMQTTJS)
	mux.HandleFunc("/healthz", s.handleHealthz)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

// ListenAndServe starts listening. It blocks until the server is shut down.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Serve accepts connections on the given listener. Useful for tests.
func (s *Server) Serve(ln net.Listener) error {
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	snap := s.tracker.Snapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderHTML(w, snap, s.view)
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	snap := s.tracker.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Write(formatJSON(snap))
}

func handleMQTTJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write(mqttJS)
}
