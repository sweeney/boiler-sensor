package web

import "github.com/sweeney/common/iot/status"

// formatJSON renders the status snapshot as JSON for the /index.json endpoint,
// reusing the shared engine's ordered encoder.
func formatJSON(snap status.Snapshot) []byte {
	return status.FormatJSON(snap)
}
