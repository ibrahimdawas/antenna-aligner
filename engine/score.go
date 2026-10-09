package engine

import (
	"antenna-aligner/router"
	"math"
)

// Scoring ranges
const (
	sinrLo = -5.0
	sinrHi = 30.0
	rsrpLo = -120.0
	rsrpHi = -60.0
	rsrqLo = -20.0
	rsrqHi = -3.0
)

// Weights
const (
	weightSINR = 0.50
	weightRSRP = 0.35
	weightRSRQ = 0.15
)

// norm normalizes x from [lo, hi] to [0, 100], clamped.
func norm(x, lo, hi float64) float64 {
	if hi == lo {
		return 0
	}
	v := (x - lo) / (hi - lo)
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return v * 100
}

// Score computes a composite signal quality score (0-100).
func Score(s *router.Sample) float64 {
	if s == nil {
		return 0
	}

	totalWeight := 0.0
	score := 0.0

	if s.SINR != nil {
		score += weightSINR * norm(*s.SINR, sinrLo, sinrHi)
		totalWeight += weightSINR
	}
	if s.RSRP != nil {
		score += weightRSRP * norm(*s.RSRP, rsrpLo, rsrpHi)
		totalWeight += weightRSRP
	}
	if s.RSRQ != nil {
		score += weightRSRQ * norm(*s.RSRQ, rsrqLo, rsrqHi)
		totalWeight += weightRSRQ
	}

	if totalWeight == 0 {
		return 0
	}

	return math.Round((score/totalWeight)*10) / 10
}

// ScoreFromValues computes score from raw values (for averaging).
func ScoreFromValues(rsrp, sinr, rsrq *float64) float64 {
	s := &router.Sample{RSRP: rsrp, SINR: sinr, RSRQ: rsrq}
	return Score(s)
}

// QualityLabel returns a quality label based on RSRP.
func QualityLabel(s *router.Sample) string {
	if s == nil || s.RSRP == nil {
		return "Unknown"
	}
	return QualityFromRSRP(*s.RSRP)
}

// QualityFromRSRP returns a quality label from an RSRP value.
func QualityFromRSRP(rsrp float64) string {
	switch {
	case rsrp > -90:
		return "Excellent"
	case rsrp > -100:
		return "Good"
	case rsrp > -110:
		return "Fair"
	default:
		return "Poor"
	}
}

// QualityClass returns a CSS class name for score coloring.
func QualityClass(s *router.Sample) string {
	if s == nil || s.RSRP == nil {
		return "score-unknown"
	}
	switch {
	case *s.RSRP > -90:
		return "score-excellent"
	case *s.RSRP > -100:
		return "score-good"
	case *s.RSRP > -110:
		return "score-fair"
	default:
		return "score-poor"
	}
}
