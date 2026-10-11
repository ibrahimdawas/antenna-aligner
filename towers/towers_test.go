package towers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCelliDSuccess(t *testing.T) {
	// Mock OpenCelliD server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		resp := openCelliDResponse{
			Lat:    32.8872,
			Lon:    13.1913,
			MCC:    606,
			MNC:    1,
			LAC:    100,
			CellID: 12345678,
			Range:  1500,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cache := NewTowerCache(filepath.Join(tmpDir, "towers_cache.json"))

	src := NewOpenCelliDSource("test-token", cache)
	src.SetBaseURL(server.URL)

	tower, err := src.Lookup(606, 1, 100, 48225)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if tower == nil {
		t.Fatal("expected tower, got nil")
	}

	if tower.Lat != 32.8872 || tower.Lon != 13.1913 {
		t.Errorf("got lat/lon %.4f, %.4f; want 32.8872, 13.1913", tower.Lat, tower.Lon)
	}
	if tower.Accuracy != 1500 {
		t.Errorf("got accuracy %d, want 1500", tower.Accuracy)
	}

	// Verify cached
	cachedTower, ok := cache.Get(606, 1, 48225)
	if !ok || cachedTower == nil {
		t.Fatal("expected tower in cache")
	}
}

func TestOpenCelliDNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{
			"status":  "error",
			"message": "cell not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cache := NewTowerCache(filepath.Join(tmpDir, "towers_cache.json"))

	src := NewOpenCelliDSource("test-token", cache)
	src.SetBaseURL(server.URL)

	tower, err := src.Lookup(606, 1, 0, 99999)
	if err != nil {
		t.Errorf("expected nil error on not found, got %v", err)
	}
	if tower != nil {
		t.Errorf("expected nil tower on not found, got %+v", tower)
	}
}

func TestOpenCelliDRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"status":"error","message":"rate limit exceeded"}`))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cache := NewTowerCache(filepath.Join(tmpDir, "towers_cache.json"))

	src := NewOpenCelliDSource("secret-key-123", cache)
	src.SetBaseURL(server.URL)

	_, err := src.Lookup(606, 1, 0, 48225)
	if err == nil {
		t.Fatal("expected error on rate limit, got nil")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
	// Verify token is never exposed in error
	if strings.Contains(err.Error(), "secret-key-123") {
		t.Errorf("API token leaked in error message: %v", err)
	}
}

func TestManualSourceAndCache(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache.json")
	cache := NewTowerCache(cachePath)
	manual := NewManualSource(cache)

	tw := Tower{
		ID:       MakeTowerID(606, 1, 1001),
		Lat:      32.900,
		Lon:      13.180,
		MCC:      606,
		MNC:      1,
		ENB:      1001,
		Accuracy: 50,
	}

	if err := manual.AddOrUpdateTower(tw); err != nil {
		t.Fatalf("AddOrUpdateTower error: %v", err)
	}

	found, err := manual.Lookup(606, 1, 0, 1001)
	if err != nil || found == nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if found.ENB != 1001 {
		t.Errorf("expected eNB 1001, got %d", found.ENB)
	}

	// Verify persistence: create new cache from same file
	cache2 := NewTowerCache(cachePath)
	found2, ok := cache2.Get(606, 1, 1001)
	if !ok || found2 == nil {
		t.Fatal("expected persisted tower in cache2")
	}
}

func TestCSVSource(t *testing.T) {
	csvContent := `radio,mcc,mnc,area,cell,unit,lon,lat,range,samples,changeable,created,updated,averageSignalStrength
LTE,606,1,100,12345678,0,13.185,32.895,1000,5,1,1600000000,1600000000,-85
GSM,606,1,100,1111,0,13.100,32.800,2000,5,1,1600000000,1600000000,-80
`
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "towers.csv")
	if err := os.WriteFile(csvPath, []byte(csvContent), 0644); err != nil {
		t.Fatalf("write csv error: %v", err)
	}

	cache := NewTowerCache(filepath.Join(tmpDir, "cache.json"))
	src, err := NewCSVSource(csvPath, cache)
	if err != nil {
		t.Fatalf("NewCSVSource error: %v", err)
	}

	// eNB = 12345678 >> 8 = 48225
	tw, err := src.Lookup(606, 1, 0, 48225)
	if err != nil || tw == nil {
		t.Fatalf("expected tower 48225 from CSV: %v", err)
	}
	if tw.Lat != 32.895 || tw.Lon != 13.185 {
		t.Errorf("got lat/lon %.3f, %.3f; want 32.895, 13.185", tw.Lat, tw.Lon)
	}
}
