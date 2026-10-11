package engine

import (
	"math"
	"testing"
)

func TestHaversineDistanceM(t *testing.T) {
	tests := []struct {
		name     string
		lat1, lon1, lat2, lon2 float64
		wantM    float64
		tolM     float64 // tolerance in meters
	}{
		{
			name: "NYC to LA",
			lat1: 40.7128, lon1: -74.0060,
			lat2: 34.0522, lon2: -118.2437,
			wantM: 3944000, tolM: 10000,
		},
		{
			name: "London to Paris",
			lat1: 51.5074, lon1: -0.1278,
			lat2: 48.8566, lon2: 2.3522,
			wantM: 343000, tolM: 5000,
		},
		{
			name: "same point",
			lat1: 32.9, lon1: 13.1,
			lat2: 32.9, lon2: 13.1,
			wantM: 0, tolM: 0.01,
		},
		{
			name: "short distance (1km approx)",
			lat1: 32.9000, lon1: 13.1000,
			lat2: 32.9090, lon2: 13.1000,
			wantM: 1000, tolM: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HaversineDistanceM(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			if math.Abs(got-tt.wantM) > tt.tolM {
				t.Errorf("HaversineDistanceM() = %.1f m, want %.1f m (±%.0f m)", got, tt.wantM, tt.tolM)
			}
		})
	}
}

func TestInitialBearing(t *testing.T) {
	tests := []struct {
		name     string
		lat1, lon1, lat2, lon2 float64
		wantDeg  float64
		tolDeg   float64
	}{
		{
			name: "due east",
			lat1: 0, lon1: 0, lat2: 0, lon2: 1,
			wantDeg: 90, tolDeg: 0.1,
		},
		{
			name: "due north",
			lat1: 0, lon1: 0, lat2: 1, lon2: 0,
			wantDeg: 0, tolDeg: 0.1,
		},
		{
			name: "due south",
			lat1: 1, lon1: 0, lat2: 0, lon2: 0,
			wantDeg: 180, tolDeg: 0.1,
		},
		{
			name: "due west",
			lat1: 0, lon1: 1, lat2: 0, lon2: 0,
			wantDeg: 270, tolDeg: 0.1,
		},
		{
			name: "northeast",
			lat1: 0, lon1: 0, lat2: 1, lon2: 1,
			wantDeg: 45, tolDeg: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InitialBearing(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			diff := math.Abs(got - tt.wantDeg)
			if diff > 180 {
				diff = 360 - diff
			}
			if diff > tt.tolDeg {
				t.Errorf("InitialBearing() = %.1f°, want %.1f° (±%.1f°)", got, tt.wantDeg, tt.tolDeg)
			}
		})
	}
}

func TestCompassText(t *testing.T) {
	tests := []struct {
		bearing float64
		want    string
	}{
		{0, "N (0°)"},
		{45, "NE (45°)"},
		{90, "E (90°)"},
		{135, "SE (135°)"},
		{180, "S (180°)"},
		{225, "SW (225°)"},
		{270, "W (270°)"},
		{315, "NW (315°)"},
		{350, "N (350°)"},
		{22.5, "NE (22°)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := CompassText(tt.bearing)
			if got != tt.want {
				t.Errorf("CompassText(%.1f) = %q, want %q", tt.bearing, got, tt.want)
			}
		})
	}
}

func TestParseECI(t *testing.T) {
	tests := []struct {
		name    string
		eci     int
		wantENB int
		wantSec int
	}{
		{"standard", 12345678, 48225, 78},
		{"min sector", 256, 1, 0},
		{"zero", 0, 0, 0},
		{"max sector", 255, 0, 255},
		{"large", 268435455, 1048575, 255}, // max 28-bit
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enb, sector := ParseECI(tt.eci)
			if enb != tt.wantENB || sector != tt.wantSec {
				t.Errorf("ParseECI(%d) = (%d, %d), want (%d, %d)",
					tt.eci, enb, sector, tt.wantENB, tt.wantSec)
			}
		})
	}
}

func TestParseECIString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantENB int
		wantSec int
		wantErr bool
	}{
		{"decimal", "12345678", 48225, 78, false},
		{"hex", "0xBC614E", 48225, 78, false},
		{"zero", "0", 0, 0, false},
		{"empty", "", 0, 0, true},
		{"invalid", "abc", 0, 0, true},
		{"with spaces", " 256 ", 1, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enb, sector, err := ParseECIString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseECIString(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseECIString(%q) unexpected error: %v", tt.input, err)
				return
			}
			if enb != tt.wantENB || sector != tt.wantSec {
				t.Errorf("ParseECIString(%q) = (%d, %d), want (%d, %d)",
					tt.input, enb, sector, tt.wantENB, tt.wantSec)
			}
		})
	}
}

func TestFreeSpacePathLossDB(t *testing.T) {
	// At 1km, 1800 MHz: FSPL ≈ 20*log10(1) + 20*log10(1800) + 32.44 = 0 + 65.1 + 32.44 ≈ 97.5
	got := FreeSpacePathLossDB(1000, 1800)
	if math.Abs(got-97.5) > 1 {
		t.Errorf("FreeSpacePathLossDB(1000, 1800) = %.1f, want ~97.5", got)
	}

	// Zero distance
	if FreeSpacePathLossDB(0, 1800) != 0 {
		t.Error("FreeSpacePathLossDB(0, ...) should return 0")
	}
}
