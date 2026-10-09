package router

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// MockSource simulates a router with a hidden optimal antenna position.
type MockSource struct {
	mu sync.Mutex

	// The hidden "true best" position
	bestHeading float64 // degrees 0-360
	bestTilt    float64 // degrees -15 to +15

	// Current antenna position (what the user has set)
	currentHeading float64
	currentTilt    float64

	// Target position (for lag simulation)
	targetHeading float64
	targetTilt    float64
	lastMoveTime  time.Time

	rng *rand.Rand
}

// NewMockSource creates a simulated router with a random optimal position.
func NewMockSource() *MockSource {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	m := &MockSource{
		bestHeading: rng.Float64() * 360,
		bestTilt:    rng.Float64()*20 - 10, // -10 to +10
		rng:         rng,
	}
	log_mock_info(m.bestHeading, m.bestTilt)
	return m
}

func log_mock_info(heading, tilt float64) {
	// Import log in a way that doesn't conflict - just print to stdout
	println("[mock] Best position: heading =", int(heading), "°, tilt =", int(tilt), "°")
}

// Move simulates turning the antenna. Changes take effect over 3-5 seconds.
func (m *MockSource) Move(deltaHeading, deltaTilt float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targetHeading = normAngle(m.targetHeading + deltaHeading)
	m.targetTilt = clampF(m.targetTilt+deltaTilt, -30, 30)
	m.lastMoveTime = time.Now()
}

// GetPosition returns the current heading and tilt (for UI display in mock mode).
func (m *MockSource) GetPosition() (heading, tilt float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatePosition()
	return m.currentHeading, m.currentTilt
}

// FetchSample returns a simulated signal based on distance from the optimal position.
func (m *MockSource) FetchSample() (*Sample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updatePosition()

	// Calculate angular distance from best
	headingDiff := angleDiff(m.currentHeading, m.bestHeading)
	tiltDiff := m.currentTilt - m.bestTilt
	distance := math.Sqrt(headingDiff*headingDiff + (tiltDiff*3)*(tiltDiff*3))

	// Generate signal values
	noise := func(scale float64) float64 {
		return (m.rng.Float64()*2 - 1) * scale
	}

	rsrp := clampF(-75-distance*0.5+noise(2), -140, -44)
	sinr := clampF(25-distance*0.3+noise(2), -20, 35)
	rsrq := clampF(-5+distance*0.15+noise(1), -25, 0)
	rssi := clampF(rsrp+20+noise(1), -120, -25)

	// Simulate tower switch if very far from best
	cellID := "mock-12345"
	if math.Abs(headingDiff) > 120 {
		cellID = "mock-67890"
	}

	return &Sample{
		Time:   time.Now(),
		RSRP:   fp(rsrp),
		RSRQ:   fp(rsrq),
		SINR:   fp(sinr),
		RSSI:   fp(rssi),
		CellID: cellID,
		PCI:    "100",
		Band:   "B3",
	}, nil
}

func (m *MockSource) Close() {}

// updatePosition gradually moves current toward target (simulates lag).
func (m *MockSource) updatePosition() {
	if m.lastMoveTime.IsZero() {
		return
	}
	elapsed := time.Since(m.lastMoveTime).Seconds()
	// Smooth transition: move ~70% of the way over 3 seconds
	alpha := clampF(elapsed/3.0, 0, 1)

	// Interpolate heading (handle wrap-around)
	diff := angleDiff(m.targetHeading, m.currentHeading)
	// diff is signed: positive means target is clockwise
	m.currentHeading = normAngle(m.currentHeading + diff*alpha)

	// Interpolate tilt linearly
	tiltDiff := m.targetTilt - m.currentTilt
	m.currentTilt = m.currentTilt + tiltDiff*alpha

	// If we're close enough, snap
	if math.Abs(angleDiff(m.currentHeading, m.targetHeading)) < 0.5 && math.Abs(m.currentTilt-m.targetTilt) < 0.5 {
		m.currentHeading = m.targetHeading
		m.currentTilt = m.targetTilt
	}
}

// --- helpers ---

func fp(v float64) *float64 {
	return &v
}

func normAngle(a float64) float64 {
	a = math.Mod(a, 360)
	if a < 0 {
		a += 360
	}
	return a
}

// angleDiff returns the signed shortest difference from a to b, range [-180, 180].
func angleDiff(target, current float64) float64 {
	d := target - current
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	return d
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
