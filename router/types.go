package router

import (
	"regexp"
	"strconv"
	"time"
)

// Sample holds one snapshot of the router's signal metrics.
type Sample struct {
	Time   time.Time `json:"time"`
	RSRP   *float64  `json:"rsrp"`   // dBm
	RSRQ   *float64  `json:"rsrq"`   // dB
	SINR   *float64  `json:"sinr"`   // dB
	RSSI   *float64  `json:"rssi"`   // dBm
	CellID string    `json:"cell_id"`
	PCI    string    `json:"pci"`
	Band   string    `json:"band"`
}

// SignalSource abstracts over real and mock routers.
type SignalSource interface {
	FetchSample() (*Sample, error)
	Close()
}

var dbRegex = regexp.MustCompile(`(-?\d+(?:\.\d+)?)`)

// parseDBValue extracts a numeric value from strings like "-95dBm", "10dB", ">=-5dB".
// Returns nil if the string cannot be parsed.
func ParseDBValue(s string) *float64 {
	if s == "" {
		return nil
	}
	m := dbRegex.FindString(s)
	if m == "" {
		return nil
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return nil
	}
	return &v
}
