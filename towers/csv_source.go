package towers

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// CSVSource implements TowerSource loaded from an OpenCelliD formatted CSV file.
type CSVSource struct {
	cache *TowerCache
}

// NewCSVSource creates a CSVSource and loads the CSV into the provided cache.
func NewCSVSource(csvPath string, cache *TowerCache) (*CSVSource, error) {
	src := &CSVSource{cache: cache}
	if csvPath != "" {
		if err := src.loadCSV(csvPath); err != nil {
			return nil, err
		}
	}
	return src, nil
}

func (c *CSVSource) loadCSV(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.ReuseRecord = true
	reader.FieldsPerRecord = -1 // Allow variable fields

	// Read header
	header, err := reader.Read()
	if err != nil {
		return err
	}

	// Map column names
	colIdx := make(map[string]int)
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	radioIdx := colIdx["radio"]
	mccIdx := colIdx["mcc"]
	mncIdx := colIdx["mnc"]
	cellIdx := colIdx["cell"]
	lonIdx := colIdx["lon"]
	latIdx := colIdx["lat"]
	rangeIdx, hasRange := colIdx["range"]

	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed lines
		}

		if len(rec) <= latIdx || len(rec) <= lonIdx {
			continue
		}

		// Filter for LTE
		radio := strings.ToUpper(strings.TrimSpace(rec[radioIdx]))
		if radio != "LTE" {
			continue
		}

		mcc, _ := strconv.Atoi(rec[mccIdx])
		mnc, _ := strconv.Atoi(rec[mncIdx])
		cellID, _ := strconv.Atoi(rec[cellIdx])
		lat, errLat := strconv.ParseFloat(rec[latIdx], 64)
		lon, errLon := strconv.ParseFloat(rec[lonIdx], 64)

		if errLat != nil || errLon != nil || (lat == 0 && lon == 0) {
			continue
		}

		accuracy := 0
		if hasRange && len(rec) > rangeIdx {
			accuracy, _ = strconv.Atoi(rec[rangeIdx])
		}

		enb := cellID >> 8
		sector := cellID & 0xFF
		if enb == 0 {
			enb = cellID
		}

		tower := Tower{
			ID:       MakeTowerID(mcc, mnc, enb),
			Lat:      lat,
			Lon:      lon,
			MCC:      mcc,
			MNC:      mnc,
			ENB:      enb,
			Source:   "csv",
			Accuracy: accuracy,
			LastSeen: time.Now(),
		}
		if sector > 0 {
			tower.Cells = []CellInfo{{Sector: sector}}
		}

		_ = c.cache.Put(tower)
	}

	return nil
}

// Lookup finds a tower in the cache.
func (c *CSVSource) Lookup(mcc, mnc, tac, enb int) (*Tower, error) {
	if t, ok := c.cache.Get(mcc, mnc, enb); ok {
		return t, nil
	}
	return nil, nil
}

// Nearby returns all towers in the cache within radiusM.
func (c *CSVSource) Nearby(lat, lon float64, radiusM int) ([]Tower, error) {
	return c.cache.FindNearby(lat, lon, radiusM), nil
}

// String provides a human-readable summary.
func (c *CSVSource) String() string {
	return fmt.Sprintf("CSVSource(towers=%d)", len(c.cache.All()))
}
