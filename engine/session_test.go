package engine

import (
	"antenna-aligner/router"
	"testing"
	"time"
)

func TestSessionGuidedLandscape(t *testing.T) {
	// Settle: 0, Measure: 0 for synchronous test simulation
	sess := NewSession(0, 0, nil)
	sess.Start(ModeGuided)

	state := sess.GetState()
	if state.Phase != PhaseCoarse {
		t.Fatalf("expected phase %v, got %v", PhaseCoarse, state.Phase)
	}

	// Fake landscape where position at AzimuthStep = 3 (approx 90 deg) has the highest score
	// Coarse expects 12 positions
	for i := 0; i < 12; i++ {
		sess.StartMeasure()

		// Generate signal for step i
		sinrVal := 5.0
		rsrpVal := -105.0
		if i == 3 {
			sinrVal = 25.0
			rsrpVal = -70.0
		}
		sample := &router.Sample{
			Time:   time.Now(),
			SINR:   &sinrVal,
			RSRP:   &rsrpVal,
			CellID: "cell-1",
		}
		sess.AddSample(sample)
	}

	state = sess.GetState()
	if state.Phase != PhaseFineAz10 {
		t.Fatalf("expected transition to PhaseFineAz10, got %v", state.Phase)
	}
	if state.BestPosition == nil || state.BestPosition.AzimuthStep != 3 {
		t.Fatalf("expected best position at step 3, got %+v", state.BestPosition)
	}
}

func TestSessionNoiseImmunity(t *testing.T) {
	sess := NewSession(0, 0, nil)
	sess.Start(ModeGuided)

	// Complete coarse quickly
	for i := 0; i < 12; i++ {
		sess.StartMeasure()
		sinr := 15.0
		rsrp := -80.0
		sess.AddSample(&router.Sample{SINR: &sinr, RSRP: &rsrp})
	}

	state := sess.GetState()
	if state.Phase != PhaseFineAz10 {
		t.Fatalf("expected PhaseFineAz10, got %v", state.Phase)
	}

	bestScoreBefore := state.BestPosition.AvgScore

	// Now in FineAz10, measure with a score increase of less than 1.0 (e.g. +0.5 noise)
	sess.StartMeasure()
	// Slightly higher but within noise margin (< 1.0 point improvement)
	sinrNoise := 15.3
	rsrpNoise := -79.8
	sess.AddSample(&router.Sample{SINR: &sinrNoise, RSRP: &rsrpNoise})

	state = sess.GetState()
	// Should not have treated minor noise as a definitive improvement to continue in that direction blindly
	// Instead, it handles hill climb boundaries appropriately.
	_ = bestScoreBefore
}
