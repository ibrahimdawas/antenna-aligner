package towers

import (
	"time"
)

// ManualSource implements TowerSource backed by manual entries and the cache.
type ManualSource struct {
	cache *TowerCache
}

// NewManualSource creates a new ManualSource.
func NewManualSource(cache *TowerCache) *ManualSource {
	return &ManualSource{cache: cache}
}

// Lookup finds a tower in the cache.
func (m *ManualSource) Lookup(mcc, mnc, tac, enb int) (*Tower, error) {
	if t, ok := m.cache.Get(mcc, mnc, enb); ok {
		return t, nil
	}
	return nil, nil
}

// Nearby returns all towers in the cache within radiusM.
func (m *ManualSource) Nearby(lat, lon float64, radiusM int) ([]Tower, error) {
	return m.cache.FindNearby(lat, lon, radiusM), nil
}

// AddOrUpdateTower allows manual entry or edit of a tower.
func (m *ManualSource) AddOrUpdateTower(t Tower) error {
	t.Source = "manual"
	if t.LastSeen.IsZero() {
		t.LastSeen = time.Now()
	}
	return m.cache.Put(t)
}
