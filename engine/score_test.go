package engine

import (
	"antenna-aligner/router"
	"math"
	"testing"
)

func floatPtr(f float64) *float64 {
	return &f
}

func TestNorm(t *testing.T) {
	tests := []struct {
		name     string
		x, lo, hi float64
		expected float64
	}{
		{"at low", -120, -120, -60, 0},
		{"at high", -60, -120, -60, 100},
		{"midway", -90, -120, -60, 50},
		{"clamped below", -130, -120, -60, 0},
		{"clamped above", -50, -120, -60, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := norm(tt.x, tt.lo, tt.hi)
			if math.Abs(got-tt.expected) > 0.001 {
				t.Errorf("norm(%v, %v, %v) = %v; want %v", tt.x, tt.lo, tt.hi, got, tt.expected)
			}
		})
	}
}

func TestScore(t *testing.T) {
	// Full sample
	s := &router.Sample{
		SINR: floatPtr(30),  // 100
		RSRP: floatPtr(-60), // 100
		RSRQ: floatPtr(-3),  // 100
	}
	score := Score(s)
	if math.Abs(score-100) > 0.1 {
		t.Errorf("Score(perfect) = %v; want 100", score)
	}

	// Bottom sample
	sBad := &router.Sample{
		SINR: floatPtr(-5),   // 0
		RSRP: floatPtr(-120), // 0
		RSRQ: floatPtr(-20),  // 0
	}
	scoreBad := Score(sBad)
	if math.Abs(scoreBad-0) > 0.1 {
		t.Errorf("Score(bad) = %v; want 0", scoreBad)
	}

	// Missing metrics (e.g. SINR is nil)
	// Weights: RSRP 0.35, RSRQ 0.15 => sum 0.50
	// If RSRP is 100 (-60dBm) and RSRQ is 0 (-20dBm)
	// score = (0.35*100 + 0.15*0) / 0.50 = 35 / 0.5 = 70
	sMissing := &router.Sample{
		RSRP: floatPtr(-60),
		RSRQ: floatPtr(-20),
	}
	scoreMissing := Score(sMissing)
	if math.Abs(scoreMissing-70) > 0.1 {
		t.Errorf("Score(missing SINR) = %v; want 70", scoreMissing)
	}
}

func TestQualityLabel(t *testing.T) {
	cases := []struct {
		rsrp     *float64
		expected string
	}{
		{floatPtr(-85), "Excellent"},
		{floatPtr(-90), "Good"},
		{floatPtr(-95), "Good"},
		{floatPtr(-100), "Fair"},
		{floatPtr(-105), "Fair"},
		{floatPtr(-111), "Poor"},
		{nil, "Unknown"},
	}

	for _, c := range cases {
		s := &router.Sample{RSRP: c.rsrp}
		lbl := QualityLabel(s)
		if lbl != c.expected {
			t.Errorf("QualityLabel(RSRP=%v) = %s; want %s", c.rsrp, lbl, c.expected)
		}
	}
}
