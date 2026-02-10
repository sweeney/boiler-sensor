// Package web provides an HTTP status server for the boiler-sensor daemon.
package web

import (
	"context"
	"net"
	"net/http"

	"github.com/sweeney/boiler-sensor/internal/status"
)

// Server serves the status page over HTTP.
type Server struct {
	httpServer *http.Server
	tracker    *status.Tracker
}

// New creates a Server that reads state from the given tracker.
func New(addr string, tracker *status.Tracker) *Server {
	s := &Server{tracker: tracker}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/index.html", s.handleIndex)
	mux.HandleFunc("/index.json", s.handleJSON)

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
	renderHTML(w, snap)
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	snap := s.tracker.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Write(formatJSON(snap))
}
