package towers

import (
	"time"
)

// CellInfo holds per-sector details.
type CellInfo struct {
	Sector int    `json:"sector"`
	PCI    string `json:"pci,omitempty"`
	Band   string `json:"band,omitempty"`
}

// Tower represents a cell tower site.
type Tower struct {
	ID       string     `json:"id"` // "mcc-mnc-eNB"
	Lat      float64    `json:"lat"`
	Lon      float64    `json:"lon"`
	MCC      int        `json:"mcc"`
	MNC      int        `json:"mnc"`
	ENB      int        `json:"enb"`
	Cells    []CellInfo `json:"cells,omitempty"`
	Source   string     `json:"source"` // "opencellid" | "csv" | "manual"
	Accuracy int        `json:"accuracy"` // meters, if known
	LastSeen time.Time  `json:"last_seen,omitempty"`
}

// TowerWithBearing augments a Tower with distance and bearing from the user location.
type TowerWithBearing struct {
	Tower
	DistanceM   float64 `json:"distance_m"`
	BearingDeg  float64 `json:"bearing_deg"`
	CompassText string  `json:"compass_text"`
	IsServing   bool    `json:"is_serving"`
	Unreliable  bool    `json:"unreliable"` // true if >50 km away
}

// TowerSource abstracts tower lookups and nearby queries.
type TowerSource interface {
	Lookup(mcc, mnc, tac, enb int) (*Tower, error)
	Nearby(lat, lon float64, radiusM int) ([]Tower, error)
}

// TowersState is the payload returned by GET /api/towers and SSE 'towers' events.
type TowersState struct {
	UserLat       *float64           `json:"user_lat"`
	UserLon       *float64           `json:"user_lon"`
	Serving       *TowerWithBearing  `json:"serving"`
	ServingCellID string             `json:"serving_cell_id,omitempty"`
	ServingENB    int                `json:"serving_enb,omitempty"`
	Nearby        []TowerWithBearing `json:"nearby"`
	Offline       bool               `json:"offline"`
	SourceName    string             `json:"source_name"`
	Error         string             `json:"error,omitempty"`
}
