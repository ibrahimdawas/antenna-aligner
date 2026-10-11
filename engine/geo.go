package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const earthRadiusM = 6371000.0 // Earth mean radius in meters

// HaversineDistanceM computes the great-circle distance in meters
// between two points specified in decimal degrees.
func HaversineDistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := degToRad(lat2 - lat1)
	dLon := degToRad(lon2 - lon1)
	rLat1 := degToRad(lat1)
	rLat2 := degToRad(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rLat1)*math.Cos(rLat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusM * c
}

// InitialBearing computes the initial bearing from point 1 to point 2,
// returned as degrees 0–360 (north=0, east=90, south=180, west=270).
func InitialBearing(lat1, lon1, lat2, lon2 float64) float64 {
	rLat1 := degToRad(lat1)
	rLat2 := degToRad(lat2)
	dLon := degToRad(lon2 - lon1)

	y := math.Sin(dLon) * math.Cos(rLat2)
	x := math.Cos(rLat1)*math.Sin(rLat2) - math.Sin(rLat1)*math.Cos(rLat2)*math.Cos(dLon)
	theta := math.Atan2(y, x)

	bearing := radToDeg(theta)
	// Normalize to 0–360
	bearing = math.Mod(bearing+360, 360)
	return bearing
}

// CompassText returns a human-readable compass direction with degrees,
// e.g. "SE (135°)".
func CompassText(bearing float64) string {
	bearing = math.Mod(bearing+360, 360)
	dirs := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := int(math.Round(bearing/45)) % 8
	return fmt.Sprintf("%s (%.0f°)", dirs[idx], bearing)
}

// CompassDir returns just the compass direction label, e.g. "SE".
func CompassDir(bearing float64) string {
	bearing = math.Mod(bearing+360, 360)
	dirs := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := int(math.Round(bearing/45)) % 8
	return dirs[idx]
}

// ParseECI splits a 28-bit E-UTRAN Cell Identity into eNB ID and sector.
// eNB = eci >> 8, sector = eci & 0xFF
func ParseECI(eci int) (enb int, sector int) {
	return eci >> 8, eci & 0xFF
}

// ParseECIString parses a CellID string (decimal or "0x"-prefixed hex)
// and returns eNB and sector.
func ParseECIString(eciStr string) (enb int, sector int, err error) {
	eciStr = strings.TrimSpace(eciStr)
	if eciStr == "" {
		return 0, 0, fmt.Errorf("empty cell ID")
	}

	var eci int64
	if strings.HasPrefix(eciStr, "0x") || strings.HasPrefix(eciStr, "0X") {
		eci, err = strconv.ParseInt(eciStr[2:], 16, 64)
	} else {
		eci, err = strconv.ParseInt(eciStr, 10, 64)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cell ID %q: %w", eciStr, err)
	}

	enb, sector = ParseECI(int(eci))
	return enb, sector, nil
}

// FreeSpacePathLossDB estimates the free-space path loss in dB.
// distM is the distance in meters, freqMHz is the frequency in MHz.
// This is a rough theoretical value, not a coverage prediction.
func FreeSpacePathLossDB(distM, freqMHz float64) float64 {
	if distM <= 0 || freqMHz <= 0 {
		return 0
	}
	// FSPL(dB) = 20*log10(d) + 20*log10(f) + 32.44
	// where d is in km and f is in MHz
	distKM := distM / 1000.0
	return 20*math.Log10(distKM) + 20*math.Log10(freqMHz) + 32.44
}

// FormatDistanceKM formats a distance in meters as a human-readable string.
func FormatDistanceKM(distM float64) string {
	if distM < 1000 {
		return fmt.Sprintf("%.0f m", distM)
	}
	return fmt.Sprintf("%.1f km", distM/1000)
}

func degToRad(d float64) float64 { return d * math.Pi / 180 }
func radToDeg(r float64) float64 { return r * 180 / math.Pi }
