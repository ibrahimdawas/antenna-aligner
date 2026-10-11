package towers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// OpenCelliDSource implements TowerSource querying the OpenCelliD API,
// with local cache fallback.
type OpenCelliDSource struct {
	token      string
	cache      *TowerCache
	httpClient *http.Client
	baseURL    string // default: "https://opencellid.org"
}

// NewOpenCelliDSource creates a new OpenCelliDSource.
func NewOpenCelliDSource(token string, cache *TowerCache) *OpenCelliDSource {
	return &OpenCelliDSource{
		token: token,
		cache: cache,
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
		},
		baseURL: "https://opencellid.org",
	}
}

// SetBaseURL allows overriding the base URL for unit tests (e.g. httptest.Server).
func (s *OpenCelliDSource) SetBaseURL(url string) {
	s.baseURL = strings.TrimRight(url, "/")
}

// openCelliDResponse represents single cell response from OpenCelliD
type openCelliDResponse struct {
	Lat                    float64 `json:"lat"`
	Lon                    float64 `json:"lon"`
	MCC                    int     `json:"mcc"`
	MNC                    int     `json:"mnc"`
	LAC                    int     `json:"lac"`
	CellID                 int     `json:"cellid"`
	Range                  int     `json:"range"` // accuracy in meters
	AverageSignalStrength *int    `json:"averageSignalStrength"`
	Status                 string  `json:"status"`
	Message                string  `json:"message"`
	Error                  string  `json:"error"`
}

// openCelliDAreaResponse represents cell elements in getInArea response
type openCelliDAreaCell struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	MCC    int     `json:"mcc"`
	MNC    int     `json:"mnc"`
	LAC    int     `json:"lac"`
	CellID int     `json:"cellid"`
	Range  int     `json:"range"`
}

type openCelliDAreaResponse struct {
	Status  string               `json:"status"`
	Message string               `json:"message"`
	Cells   []openCelliDAreaCell `json:"cells"`
}

// Lookup finds a tower by MCC, MNC, TAC, eNB.
// Checks local cache first; if not found and online, queries OpenCelliD.
func (s *OpenCelliDSource) Lookup(mcc, mnc, tac, enb int) (*Tower, error) {
	// Check cache first
	if t, ok := s.cache.Get(mcc, mnc, enb); ok {
		return t, nil
	}

	if s.token == "" {
		return nil, nil // No token provided, offline/manual only
	}

	// Try querying by ECI or eNB
	// Note: in LTE, OpenCelliD cellid is often the full ECI, or sometimes eNB.
	// We'll try cellid = enb << 8 first if tac is unknown, or eNB directly.
	cellToQuery := enb << 8 // Sector 0 ECI
	if cellToQuery == 0 {
		cellToQuery = enb
	}

	url := fmt.Sprintf("%s/cell/get?key=%s&mcc=%d&mnc=%d&cellid=%d&format=json",
		s.baseURL, s.token, mcc, mnc, cellToQuery)
	if tac > 0 {
		url += fmt.Sprintf("&lac=%d", tac)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Network error (offline) - return nil, nil so caller can handle gracefully
		return nil, fmt.Errorf("opencellid request failed: %v", sanitizeError(err, s.token))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("opencellid rate limit exceeded (HTTP 429)")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("opencellid server error (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data openCelliDResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("opencellid json parse error: %w", err)
	}

	if strings.EqualFold(data.Status, "error") || data.Error != "" {
		errMsg := data.Message
		if errMsg == "" {
			errMsg = data.Error
		}
		if strings.Contains(strings.ToLower(errMsg), "not found") ||
			strings.Contains(strings.ToLower(errMsg), "cell not found") {
			return nil, nil
		}
		if strings.Contains(strings.ToLower(errMsg), "limit") ||
			strings.Contains(strings.ToLower(errMsg), "quota") {
			return nil, fmt.Errorf("opencellid rate limit/quota error: %s", errMsg)
		}
		return nil, fmt.Errorf("opencellid error: %s", errMsg)
	}

	if data.Lat == 0 && data.Lon == 0 {
		return nil, nil
	}

	tower := Tower{
		ID:       MakeTowerID(mcc, mnc, enb),
		Lat:      data.Lat,
		Lon:      data.Lon,
		MCC:      mcc,
		MNC:      mnc,
		ENB:      enb,
		Source:   "opencellid",
		Accuracy: data.Range,
		LastSeen: time.Now(),
	}

	_ = s.cache.Put(tower)
	return &tower, nil
}

// Nearby queries OpenCelliD for towers in the bounding box around lat, lon.
func (s *OpenCelliDSource) Nearby(lat, lon float64, radiusM int) ([]Tower, error) {
	// First gather cached nearby
	cached := s.cache.FindNearby(lat, lon, radiusM)

	if s.token == "" {
		return cached, nil
	}

	// Compute bounding box
	dLat := float64(radiusM) / 111000.0
	cosLat := math.Cos(lat * math.Pi / 180.0)
	if cosLat < 0.01 {
		cosLat = 0.01
	}
	dLon := float64(radiusM) / (111000.0 * cosLat)

	minLat := lat - dLat
	maxLat := lat + dLat
	minLon := lon - dLon
	maxLon := lon + dLon

	url := fmt.Sprintf("%s/cell/getInArea?key=%s&BBOX=%.6f,%.6f,%.6f,%.6f&format=json",
		s.baseURL, s.token, minLat, minLon, maxLat, maxLon)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return cached, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Offline fallback to cached
		return cached, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return cached, fmt.Errorf("opencellid rate limit exceeded (HTTP 429)")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return cached, err
	}

	// OpenCelliD can return array of cells or wrapper object with cells
	var areaResp openCelliDAreaResponse
	var rawCells []openCelliDAreaCell

	if err := json.Unmarshal(body, &rawCells); err != nil {
		if err2 := json.Unmarshal(body, &areaResp); err2 == nil {
			rawCells = areaResp.Cells
		}
	}

	for _, c := range rawCells {
		enb := c.CellID >> 8
		if enb == 0 {
			enb = c.CellID
		}
		t := Tower{
			ID:       MakeTowerID(c.MCC, c.MNC, enb),
			Lat:      c.Lat,
			Lon:      c.Lon,
			MCC:      c.MCC,
			MNC:      c.MNC,
			ENB:      enb,
			Source:   "opencellid",
			Accuracy: c.Range,
			LastSeen: time.Now(),
		}
		_ = s.cache.Put(t)
	}

	return s.cache.FindNearby(lat, lon, radiusM), nil
}

func sanitizeError(err error, token string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if token != "" {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}
