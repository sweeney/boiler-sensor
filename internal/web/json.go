package web

import (
	"github.com/sweeney/boiler-sensor/internal/status"
)

func formatJSON(snap status.Snapshot) []byte {
	return status.FormatJSON(snap)
}
