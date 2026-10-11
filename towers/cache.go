package towers

import (
	"antenna-aligner/engine"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// MakeTowerID creates standard ID format "mcc-mnc-enb"
func MakeTowerID(mcc, mnc, enb int) string {
	return fmt.Sprintf("%d-%d-%d", mcc, mnc, enb)
}

// TowerCache manages an in-memory and disk-persisted cache of towers.
type TowerCache struct {
	mu       sync.RWMutex
	filePath string
	towers   map[string]Tower // keyed by "mcc-mnc-enb"
}

// NewTowerCache creates and loads a TowerCache from the given file path.
func NewTowerCache(filePath string) *TowerCache {
	tc := &TowerCache{
		filePath: filePath,
		towers:   make(map[string]Tower),
	}
	_ = tc.load()
	return tc
}

func (tc *TowerCache) load() error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	data, err := os.ReadFile(tc.filePath)
	if err != nil {
		return err
	}

	var list []Tower
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	for _, t := range list {
		id := t.ID
		if id == "" {
			id = MakeTowerID(t.MCC, t.MNC, t.ENB)
			t.ID = id
		}
		tc.towers[id] = t
	}
	return nil
}

// Save persists the cache to disk.
func (tc *TowerCache) Save() error {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	list := make([]Tower, 0, len(tc.towers))
	for _, t := range tc.towers {
		list = append(list, t)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tc.filePath, data, 0644)
}

// Get retrieves a tower by MCC, MNC, and eNB.
func (tc *TowerCache) Get(mcc, mnc, enb int) (*Tower, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	id := MakeTowerID(mcc, mnc, enb)
	t, ok := tc.towers[id]
	if !ok {
		return nil, false
	}
	tCopy := t
	return &tCopy, true
}

// Put adds or updates a tower in the cache and saves it to disk.
func (tc *TowerCache) Put(t Tower) error {
	if t.ID == "" {
		t.ID = MakeTowerID(t.MCC, t.MNC, t.ENB)
	}

	tc.mu.Lock()
	tc.towers[t.ID] = t
	tc.mu.Unlock()

	return tc.Save()
}

// All returns a slice of all towers in cache.
func (tc *TowerCache) All() []Tower {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	res := make([]Tower, 0, len(tc.towers))
	for _, t := range tc.towers {
		res = append(res, t)
	}
	return res
}

// FindNearby returns cached towers within radiusM meters of the coordinates.
func (tc *TowerCache) FindNearby(lat, lon float64, radiusM int) []Tower {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	var result []Tower
	for _, t := range tc.towers {
		dist := engine.HaversineDistanceM(lat, lon, t.Lat, t.Lon)
		if dist <= float64(radiusM) {
			result = append(result, t)
		}
	}
	return result
}
