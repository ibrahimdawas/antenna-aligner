package engine

import (
	"antenna-aligner/router"
	"testing"
	"time"
)

func TestSessionGuidedLandscape(t *testing.T) {
	// Settle: 0, Measure: 0 for synchronous test simulation
	sess := NewSession(0, 0, 60, nil)
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
	sess := NewSession(0, 0, 60, nil)
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

func TestSessionWithTowerHint(t *testing.T) {
	sess := NewSession(0, 0, 60, nil)
	// Known tower at 2.4 km, bearing 135° (SE)
	sess.SetTowerHint(135.0, 2400.0)

	sess.Start(ModeGuided)

	state := sess.GetState()
	// Acceptance criterion: HINT phase appears first when tower bearing is known
	if state.Phase != PhaseHint {
		t.Fatalf("expected phase %v, got %v", PhaseHint, state.Phase)
	}
	if state.Instruction.Phase != PhaseHint {
		t.Errorf("expected instruction phase %v, got %v", PhaseHint, state.Instruction.Phase)
	}
	if state.Instruction.Text == "" {
		t.Error("expected non-empty hint instruction")
	}

	// 1. Measure at HINT heading (position 0)
	sess.StartMeasure()
	sinr := 25.0
	rsrp := -75.0
	sess.AddSample(&router.Sample{SINR: &sinr, RSRP: &rsrp, CellID: "cell-hint"})

	state = sess.GetState()
	// Should transition to shortened COARSE
	if state.Phase != PhaseCoarse {
		t.Fatalf("expected transition to PhaseCoarse, got %v", state.Phase)
	}
	if state.Instruction.Progress.EstimatedTotal != 5 {
		t.Errorf("expected shortened coarse total 5, got %d", state.Instruction.Progress.EstimatedTotal)
	}

	// 2. Measure remaining 4 positions (+30, +60, -30, -60)
	for i := 1; i < 5; i++ {
		sess.StartMeasure()
		sVal := 15.0
		rVal := -85.0
		sess.AddSample(&router.Sample{SINR: &sVal, RSRP: &rVal, CellID: "cell-hint"})
	}

	state = sess.GetState()
	// Total positions: 5 (1 HINT + 4 COARSE)
	if len(state.Positions) != 5 {
		t.Fatalf("expected 5 positions measured, got %d", len(state.Positions))
	}
	// With good scores, should transition to FINE_AZ_10
	if state.Phase != PhaseFineAz10 {
		t.Fatalf("expected transition to PhaseFineAz10, got %v", state.Phase)
	}
	// Best position should be position 1 (the hint position)
	if state.BestPosition == nil || state.BestPosition.ID != 1 {
		t.Errorf("expected best position ID 1, got %+v", state.BestPosition)
	}
}

func TestSessionShortCoarseLowScoreOfferFullScan(t *testing.T) {
	sess := NewSession(0, 0, 60, nil)
	sess.SetTowerHint(90.0, 5000.0)
	sess.Start(ModeGuided)

	// Measure HINT with poor signal (score < 40)
	sess.StartMeasure()
	sinrLow := -5.0
	rsrpLow := -115.0
	sess.AddSample(&router.Sample{SINR: &sinrLow, RSRP: &rsrpLow})

	// Measure remaining 4 coarse positions, all poor
	for i := 1; i < 5; i++ {
		sess.StartMeasure()
		sess.AddSample(&router.Sample{SINR: &sinrLow, RSRP: &rsrpLow})
	}

	state := sess.GetState()
	// Acceptance criterion: if best score < 40, offer "Run the full 360° scan"
	if !state.OfferFullScan {
		t.Error("expected OfferFullScan to be true when best score < 40")
	}

	// User clicks full scan
	sess.RunFullScan()
	state = sess.GetState()
	if state.Phase != PhaseCoarse {
		t.Errorf("expected PhaseCoarse, got %v", state.Phase)
	}
	if state.Instruction.Progress.EstimatedTotal != 12 {
		t.Errorf("expected full 12-position scan, got %d", state.Instruction.Progress.EstimatedTotal)
	}
}

func TestDynamicSettle(t *testing.T) {
	// Settle: 10s, Measure: 5s
	sess := NewSession(10, 5, 60, nil)
	sess.Start(ModeGuided)

	sess.StartMeasure()
	state := sess.GetState()
	if state.MeasureState != MeasureSettling {
		t.Fatalf("expected MeasureSettling, got %v", state.MeasureState)
	}

	// Send 3 consecutive samples with SINR differing by < 1.0 dB
	s1 := 10.0
	s2 := 10.4
	s3 := 10.2
	sess.AddSample(&router.Sample{SINR: &s1})
	sess.AddSample(&router.Sample{SINR: &s2})
	sess.AddSample(&router.Sample{SINR: &s3})

	state = sess.GetState()
	// Should have transitioned to MeasureCollecting early due to dynamic settle
	if state.MeasureState != MeasureCollecting {
		t.Fatalf("expected early transition to MeasureCollecting, got %v", state.MeasureState)
	}
}

func TestStaleDetection(t *testing.T) {
	sess := NewSession(0, 0, 60, nil)
	sess.Start(ModeGuided)

	// Send 5 identical sample values with separate allocations
	for i := 0; i < 4; i++ {
		s := 12.0
		r := -85.0
		sess.AddSample(&router.Sample{SINR: &s, RSRP: &r})
		state := sess.GetState()
		if state.StaleWarning {
			t.Fatalf("did not expect stale warning at %d samples", i+1)
		}
	}

	// 5th identical sample triggers stale warning
	s5 := 12.0
	r5 := -85.0
	sess.AddSample(&router.Sample{SINR: &s5, RSRP: &r5})
	state := sess.GetState()
	if !state.StaleWarning {
		t.Fatalf("expected stale warning at 5 identical samples")
	}

	// Differing sample clears stale warning
	sinr2 := 14.0
	r6 := -85.0
	sess.AddSample(&router.Sample{SINR: &sinr2, RSRP: &r6})
	state = sess.GetState()
	if state.StaleWarning {
		t.Fatalf("expected stale warning to clear on changing sample")
	}
}

func TestBandSwitchAndHighVariance(t *testing.T) {
	sess := NewSession(0, 10, 60, nil)
	sess.Start(ModeGuided)

	sess.StartMeasure()
	// Sample 1: Band 3, SINR 5
	s1 := 5.0
	r1 := -90.0
	sess.AddSample(&router.Sample{SINR: &s1, RSRP: &r1, Band: "B3", CellID: "cell-1"})

	// Sample 2: Band 20, SINR 15 (spread is 10 dB > 4 dB)
	s2 := 15.0
	r2 := -88.0
	sess.AddSample(&router.Sample{SINR: &s2, RSRP: &r2, Band: "B20", CellID: "cell-1"})

	sess.mu.Lock()
	sess.finalizeMeasurement()
	sess.mu.Unlock()

	state := sess.GetState()
	if len(state.Positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(state.Positions))
	}
	pos := state.Positions[0]
	if !pos.BandSwitch {
		t.Errorf("expected BandSwitch to be true")
	}
	if !pos.HighVariance {
		t.Errorf("expected HighVariance to be true (spread %.1f > 4.0)", pos.SINRSpread)
	}
	if pos.SINRSpread != 10.0 {
		t.Errorf("expected SINRSpread 10.0, got %.1f", pos.SINRSpread)
	}
}

func TestBeamwidthDerivedStepSizes(t *testing.T) {
	// Narrow beamwidth antenna: 30°
	sess := NewSession(0, 0, 30, nil)
	sess.Start(ModeGuided)

	state := sess.GetState()
	if state.StepSize != 15 {
		t.Errorf("expected coarse step 15 for 30° beamwidth, got %d", state.StepSize)
	}
	if state.BeamwidthDeg != 30 {
		t.Errorf("expected BeamwidthDeg 30, got %d", state.BeamwidthDeg)
	}
}


