package engine

import (
	"antenna-aligner/router"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Phase constants
type Phase string

const (
	PhaseIdle     Phase = "IDLE"
	PhaseCoarse   Phase = "COARSE"
	PhaseFineAz10 Phase = "FINE_AZ_10"
	PhaseFineAz5  Phase = "FINE_AZ_5"
	PhaseTilt     Phase = "TILT"
	PhaseRecheck  Phase = "RECHECK"
	PhaseDone     Phase = "DONE"
)

// Action constants
type Action string

const (
	ActionTurnLeft  Action = "TURN_LEFT"
	ActionTurnRight Action = "TURN_RIGHT"
	ActionTiltUp    Action = "TILT_UP"
	ActionTiltDown  Action = "TILT_DOWN"
	ActionHold      Action = "HOLD"
	ActionMeasure   Action = "MEASURE"
	ActionDone      Action = "DONE"
)

// MeasureState constants
type MeasureState string

const (
	MeasureIdle       MeasureState = "IDLE"
	MeasureSettling   MeasureState = "SETTLING"
	MeasureCollecting MeasureState = "COLLECTING"
)

// SessionMode constants
type SessionMode string

const (
	ModeNone   SessionMode = ""
	ModeGuided SessionMode = "guided"
	ModeLive   SessionMode = "live"
	ModeSpeed  SessionMode = "speed"
)

// LiveTrend constants
type LiveTrend string

const (
	TrendWarmer LiveTrend = "WARMER"
	TrendColder LiveTrend = "COLDER"
	TrendSteady LiveTrend = "STEADY"
)

// Position represents one measured antenna position.
type Position struct {
	ID          int       `json:"id"`
	Phase       Phase     `json:"phase"`
	AzimuthStep int       `json:"azimuth_step"`
	TiltStep    int       `json:"tilt_step"`
	AvgScore    float64   `json:"avg_score"`
	AvgRSRP     *float64  `json:"avg_rsrp"`
	AvgSINR     *float64  `json:"avg_sinr"`
	AvgRSRQ     *float64  `json:"avg_rsrq"`
	CellID      string    `json:"cell_id"`
	TowerSwitch bool      `json:"tower_switch"`
	SampleCount int       `json:"sample_count"`
	Time        time.Time `json:"time"`
}

// Instruction tells the UI what to do next.
type Instruction struct {
	Action    Action `json:"action"`
	AmountDeg int    `json:"amount_deg"`
	Text      string `json:"text"`
	Phase     Phase  `json:"phase"`
	Progress  struct {
		Current        int `json:"current"`
		EstimatedTotal int `json:"estimated_total"`
	} `json:"progress"`
}

// SessionState is the snapshot sent to the UI.
type SessionState struct {
	Mode            SessionMode  `json:"mode"`
	Phase           Phase        `json:"phase"`
	Positions       []Position   `json:"positions"`
	Instruction     Instruction  `json:"instruction"`
	MeasureState    MeasureState `json:"measure_state"`
	MeasureStart    time.Time    `json:"measure_start"`
	SettleSeconds   int          `json:"settle_seconds"`
	MeasureSeconds  int          `json:"measure_seconds"`
	BestPosition    *Position    `json:"best_position"`
	CurrentAzStep   int          `json:"current_az_step"`
	CurrentTiltStep int          `json:"current_tilt_step"`
	LiveTrend       LiveTrend    `json:"live_trend"`
	LiveBestScore   float64      `json:"live_best_score"`
	LiveScore       float64      `json:"live_score"`
	TotalPositions  int          `json:"total_positions"`
	StepSize        int          `json:"step_size"`
}

// Session manages the alignment state machine.
type Session struct {
	mu sync.RWMutex

	mode         SessionMode
	phase        Phase
	positions    []Position
	instruction  Instruction
	measureState MeasureState
	measureStart time.Time

	settleSeconds  int
	measureSeconds int

	collectSamples []router.Sample

	// Guided mode
	currentAzStep   int
	currentTiltStep int
	bestPosition    *Position
	stepSize        int
	coarseCount     int
	hillDirection   int // +1 or -1
	hillTried       int // 0=not started, 1=first dir tried, 2=both tried
	hillBest        *Position
	positionCounter int

	// Live mode
	liveScoreHistory []liveEntry
	liveBestScore    float64
	liveTrend        LiveTrend
	liveScore        float64

	onChange func()
}

type liveEntry struct {
	time  time.Time
	score float64
}

// NewSession creates a new session with the given timing parameters.
func NewSession(settleSeconds, measureSeconds int, onChange func()) *Session {
	return &Session{
		settleSeconds:  settleSeconds,
		measureSeconds: measureSeconds,
		phase:          PhaseIdle,
		measureState:   MeasureIdle,
		onChange:        onChange,
	}
}

// Start begins a new session in the given mode.
func (s *Session) Start(mode SessionMode) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mode = mode
	s.positions = nil
	s.measureState = MeasureIdle
	s.collectSamples = nil
	s.currentAzStep = 0
	s.currentTiltStep = 0
	s.bestPosition = nil
	s.coarseCount = 0
	s.hillDirection = 0
	s.hillTried = 0
	s.hillBest = nil
	s.positionCounter = 0
	s.liveScoreHistory = nil
	s.liveBestScore = 0
	s.liveTrend = TrendSteady
	s.liveScore = 0

	if mode == ModeGuided {
		s.phase = PhaseCoarse
		s.stepSize = 30
		s.instruction = Instruction{
			Action: ActionHold,
			Text:   "Measure at your starting position",
			Phase:  PhaseCoarse,
		}
		s.instruction.Progress.Current = 0
		s.instruction.Progress.EstimatedTotal = 12
	} else if mode == ModeSpeed {
		s.phase = PhaseIdle
		s.instruction = Instruction{
			Action: ActionHold,
			Text:   "Benchmark your internet speed",
			Phase:  PhaseIdle,
		}
	} else {
		s.phase = PhaseIdle
		s.instruction = Instruction{
			Action: ActionHold,
			Text:   "Move the antenna and watch the signal",
			Phase:  PhaseIdle,
		}
	}

	s.notify()
}

// AddSample is called by the poller for every new sample.
func (s *Session) AddSample(sample *router.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sample == nil {
		return
	}

	switch s.measureState {
	case MeasureSettling:
		elapsed := time.Since(s.measureStart).Seconds()
		if elapsed >= float64(s.settleSeconds) {
			s.measureState = MeasureCollecting
			s.measureStart = time.Now()
			s.collectSamples = nil
			s.notify()
		}
		// Discard samples during settling

	case MeasureCollecting:
		s.collectSamples = append(s.collectSamples, *sample)
		elapsed := time.Since(s.measureStart).Seconds()
		if elapsed >= float64(s.measureSeconds) {
			s.finalizeMeasurement()
		}
	}

	// Live mode: update rolling average
	if s.mode == ModeLive {
		score := Score(sample)
		now := time.Now()
		s.liveScoreHistory = append(s.liveScoreHistory, liveEntry{time: now, score: score})

		// Keep last 10 seconds of data
		cutoff := now.Add(-10 * time.Second)
		start := 0
		for start < len(s.liveScoreHistory) && s.liveScoreHistory[start].time.Before(cutoff) {
			start++
		}
		s.liveScoreHistory = s.liveScoreHistory[start:]

		// Compute 5s rolling average
		fiveAgo := now.Add(-5 * time.Second)
		var recentSum, oldSum float64
		var recentCount, oldCount int
		for _, e := range s.liveScoreHistory {
			if e.time.After(fiveAgo) {
				recentSum += e.score
				recentCount++
			} else {
				oldSum += e.score
				oldCount++
			}
		}

		if recentCount > 0 {
			s.liveScore = recentSum / float64(recentCount)
		}
		if s.liveScore > s.liveBestScore {
			s.liveBestScore = s.liveScore
		}

		// Determine trend
		if oldCount > 0 && recentCount > 0 {
			oldAvg := oldSum / float64(oldCount)
			diff := s.liveScore - oldAvg
			if diff >= 2 {
				s.liveTrend = TrendWarmer
			} else if diff <= -2 {
				s.liveTrend = TrendColder
			} else {
				s.liveTrend = TrendSteady
			}
		}
	}
}

// StartMeasure begins a measurement at the current position.
func (s *Session) StartMeasure() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settleSeconds <= 0 {
		s.measureState = MeasureCollecting
	} else {
		s.measureState = MeasureSettling
	}
	s.measureStart = time.Now()
	s.collectSamples = nil
	s.notify()
}

// Remeasure discards the last position and measures again.
func (s *Session) Remeasure() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.positions) > 0 {
		s.positions = s.positions[:len(s.positions)-1]
		if s.phase == PhaseCoarse {
			s.coarseCount--
		}
	}
	s.measureState = MeasureSettling
	s.measureStart = time.Now()
	s.collectSamples = nil
	s.notify()
}

// SkipPhase advances to the next phase.
func (s *Session) SkipPhase() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.advanceToNextPhase()
	s.notify()
}

// Mark saves the current position in live mode.
func (s *Session) Mark() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mode != ModeLive {
		return
	}
	s.positionCounter++
	pos := Position{
		ID:          s.positionCounter,
		Phase:       PhaseIdle,
		AzimuthStep: s.currentAzStep,
		TiltStep:    s.currentTiltStep,
		AvgScore:    math.Round(s.liveScore*10) / 10,
		Time:        time.Now(),
	}
	s.positions = append(s.positions, pos)
	s.notify()
}

// Reset clears everything back to idle.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mode = ModeNone
	s.phase = PhaseIdle
	s.positions = nil
	s.instruction = Instruction{}
	s.measureState = MeasureIdle
	s.collectSamples = nil
	s.currentAzStep = 0
	s.currentTiltStep = 0
	s.bestPosition = nil
	s.coarseCount = 0
	s.hillDirection = 0
	s.hillTried = 0
	s.hillBest = nil
	s.positionCounter = 0
	s.liveScoreHistory = nil
	s.liveBestScore = 0
	s.liveTrend = TrendSteady
	s.liveScore = 0
	s.notify()
}

// GetState returns a thread-safe snapshot of the session.
func (s *Session) GetState() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	positions := make([]Position, len(s.positions))
	copy(positions, s.positions)

	return SessionState{
		Mode:            s.mode,
		Phase:           s.phase,
		Positions:       positions,
		Instruction:     s.instruction,
		MeasureState:    s.measureState,
		MeasureStart:    s.measureStart,
		SettleSeconds:   s.settleSeconds,
		MeasureSeconds:  s.measureSeconds,
		BestPosition:    s.bestPosition,
		CurrentAzStep:   s.currentAzStep,
		CurrentTiltStep: s.currentTiltStep,
		LiveTrend:       s.liveTrend,
		LiveBestScore:   math.Round(s.liveBestScore*10) / 10,
		LiveScore:       math.Round(s.liveScore*10) / 10,
		TotalPositions:  len(s.positions),
		StepSize:        s.stepSize,
	}
}

// --- private methods ---

func (s *Session) notify() {
	if s.onChange != nil {
		go s.onChange()
	}
}

func (s *Session) finalizeMeasurement() {
	samples := s.collectSamples
	s.collectSamples = nil
	s.measureState = MeasureIdle

	if len(samples) == 0 {
		return
	}

	// Trimmed average: drop lowest and highest 10% by score if >= 10 samples
	type scored struct {
		sample router.Sample
		score  float64
	}
	scoredSamples := make([]scored, len(samples))
	for i, sm := range samples {
		scoredSamples[i] = scored{sample: sm, score: Score(&sm)}
	}

	if len(scoredSamples) >= 10 {
		sort.Slice(scoredSamples, func(i, j int) bool {
			return scoredSamples[i].score < scoredSamples[j].score
		})
		trim := len(scoredSamples) / 10
		scoredSamples = scoredSamples[trim : len(scoredSamples)-trim]
	}

	// Average the remaining samples
	var rsrpSum, sinrSum, rsrqSum float64
	var rsrpN, sinrN, rsrqN int
	for _, ss := range scoredSamples {
		if ss.sample.RSRP != nil {
			rsrpSum += *ss.sample.RSRP
			rsrpN++
		}
		if ss.sample.SINR != nil {
			sinrSum += *ss.sample.SINR
			sinrN++
		}
		if ss.sample.RSRQ != nil {
			rsrqSum += *ss.sample.RSRQ
			rsrqN++
		}
	}

	var avgRSRP, avgSINR, avgRSRQ *float64
	if rsrpN > 0 {
		v := math.Round((rsrpSum/float64(rsrpN))*10) / 10
		avgRSRP = &v
	}
	if sinrN > 0 {
		v := math.Round((sinrSum/float64(sinrN))*10) / 10
		avgSINR = &v
	}
	if rsrqN > 0 {
		v := math.Round((rsrqSum/float64(rsrqN))*10) / 10
		avgRSRQ = &v
	}

	avgScore := ScoreFromValues(avgRSRP, avgSINR, avgRSRQ)

	// Check for tower switch
	firstCell := samples[0].CellID
	lastCell := samples[len(samples)-1].CellID
	towerSwitch := firstCell != "" && lastCell != "" && firstCell != lastCell

	s.positionCounter++
	pos := Position{
		ID:          s.positionCounter,
		Phase:       s.phase,
		AzimuthStep: s.currentAzStep,
		TiltStep:    s.currentTiltStep,
		AvgScore:    math.Round(avgScore*10) / 10,
		AvgRSRP:     avgRSRP,
		AvgSINR:     avgSINR,
		AvgRSRQ:     avgRSRQ,
		CellID:      lastCell,
		TowerSwitch: towerSwitch,
		SampleCount: len(scoredSamples),
		Time:        time.Now(),
	}
	s.positions = append(s.positions, pos)

	// Update overall best
	if s.bestPosition == nil || pos.AvgScore > s.bestPosition.AvgScore {
		posCopy := pos
		s.bestPosition = &posCopy
	}

	if s.mode == ModeGuided {
		s.advanceGuidedState()
	}
	s.notify()
}

func (s *Session) advanceGuidedState() {
	switch s.phase {
	case PhaseCoarse:
		s.advanceCoarse()
	case PhaseFineAz10, PhaseFineAz5, PhaseRecheck:
		s.advanceHillClimb()
	case PhaseTilt:
		s.advanceTilt()
	}
}

func (s *Session) advanceCoarse() {
	s.coarseCount++
	total := 12

	if s.coarseCount >= total {
		// Find best coarse position
		best := s.findBestInPhase(PhaseCoarse)
		if best != nil {
			posCopy := *best
			s.bestPosition = &posCopy
		}
		s.transitionToPhase(PhaseFineAz10)
		return
	}

	// Instruction: turn right 30°
	s.instruction = Instruction{
		Action:    ActionTurnRight,
		AmountDeg: 30,
		Text:      "Turn RIGHT about 30°, then press Measure",
		Phase:     PhaseCoarse,
	}
	s.instruction.Progress.Current = s.coarseCount
	s.instruction.Progress.EstimatedTotal = total
	s.currentAzStep += 1 // Each step is 30° in coarse
}

func (s *Session) transitionToPhase(newPhase Phase) {
	s.phase = newPhase

	switch newPhase {
	case PhaseFineAz10:
		s.stepSize = 10
		s.beginHillClimb(newPhase, false)
	case PhaseFineAz5:
		s.stepSize = 5
		s.beginHillClimb(newPhase, false)
	case PhaseTilt:
		s.stepSize = 5
		s.beginTiltClimb()
	case PhaseRecheck:
		s.stepSize = 5
		s.beginHillClimb(newPhase, false)
	case PhaseDone:
		s.finishSession()
	}
}

func (s *Session) beginHillClimb(phase Phase, _ bool) {
	// Navigate to the best position first
	if s.bestPosition != nil {
		deltaSteps := s.bestPosition.AzimuthStep - s.currentAzStep
		if deltaSteps != 0 {
			degrees := abs(deltaSteps) * s.getCoarseStepDeg()
			action := ActionTurnRight
			text := fmt.Sprintf("Turn RIGHT about %d° (return to best position), then press Measure", degrees)
			if deltaSteps < 0 {
				action = ActionTurnLeft
				text = fmt.Sprintf("Turn LEFT about %d° (return to best position), then press Measure", degrees)
			}
			s.currentAzStep = s.bestPosition.AzimuthStep
			s.instruction = Instruction{
				Action:    action,
				AmountDeg: degrees,
				Text:      text,
				Phase:     phase,
			}
			s.instruction.Progress.Current = 0
			s.instruction.Progress.EstimatedTotal = s.estimateTotal(phase)
			return
		}
	}

	// Already at best position, start climbing left
	s.hillDirection = -1
	s.hillTried = 0
	if s.bestPosition != nil {
		posCopy := *s.bestPosition
		s.hillBest = &posCopy
	}

	s.instruction = Instruction{
		Action:    ActionTurnLeft,
		AmountDeg: s.stepSize,
		Text:      fmt.Sprintf("Turn LEFT about %d°, then press Measure", s.stepSize),
		Phase:     phase,
	}
	s.instruction.Progress.Current = 0
	s.instruction.Progress.EstimatedTotal = s.estimateTotal(phase)
	s.currentAzStep--
}

func (s *Session) advanceHillClimb() {
	lastPos := s.positions[len(s.positions)-1]

	if s.hillBest == nil {
		posCopy := lastPos
		s.hillBest = &posCopy
		// This is the first measurement at best, now go left
		s.hillDirection = -1
		s.hillTried = 0
		s.currentAzStep += s.hillDirection
		s.setHillInstruction()
		return
	}

	improved := lastPos.AvgScore > s.hillBest.AvgScore+1.0

	if improved {
		posCopy := lastPos
		s.hillBest = &posCopy
		if lastPos.AvgScore > s.bestPosition.AvgScore {
			s.bestPosition = &posCopy
		}
		// Continue in the same direction
		s.currentAzStep += s.hillDirection
		s.setHillInstruction()
		return
	}

	// Not improved
	if s.hillTried == 0 {
		// Return to hill best, try opposite direction
		s.hillTried = 1
		target := s.hillBest.AzimuthStep
		deltaSteps := target - s.currentAzStep
		if deltaSteps != 0 {
			s.currentAzStep = target
		}
		s.hillDirection *= -1
		s.currentAzStep += s.hillDirection
		s.setHillInstruction()
		return
	}

	// Both directions tried, phase complete
	// Return to hill best
	if s.hillBest != nil {
		s.currentAzStep = s.hillBest.AzimuthStep
		posCopy := *s.hillBest
		if posCopy.AvgScore > s.bestPosition.AvgScore {
			s.bestPosition = &posCopy
		}
	}
	s.advanceToNextPhase()
}

func (s *Session) beginTiltClimb() {
	// Navigate to best azimuth first if needed
	if s.bestPosition != nil {
		deltaSteps := s.bestPosition.AzimuthStep - s.currentAzStep
		if deltaSteps != 0 {
			degrees := abs(deltaSteps) * s.getCoarseStepDeg()
			action := ActionTurnRight
			text := fmt.Sprintf("Turn RIGHT about %d° (return to best), then press Measure", degrees)
			if deltaSteps < 0 {
				action = ActionTurnLeft
				text = fmt.Sprintf("Turn LEFT about %d° (return to best), then press Measure", degrees)
			}
			s.currentAzStep = s.bestPosition.AzimuthStep
			s.instruction = Instruction{
				Action:    action,
				AmountDeg: degrees,
				Text:      text,
				Phase:     PhaseTilt,
			}
			return
		}
	}

	s.hillDirection = 1 // up first
	s.hillTried = 0
	if s.bestPosition != nil {
		posCopy := *s.bestPosition
		s.hillBest = &posCopy
	}

	s.currentTiltStep += s.hillDirection
	s.instruction = Instruction{
		Action:    ActionTiltUp,
		AmountDeg: s.stepSize,
		Text:      fmt.Sprintf("Tilt UP about %d°, then press Measure", s.stepSize),
		Phase:     PhaseTilt,
	}
	s.instruction.Progress.Current = 0
	s.instruction.Progress.EstimatedTotal = 6
}

func (s *Session) advanceTilt() {
	lastPos := s.positions[len(s.positions)-1]

	if s.hillBest == nil {
		posCopy := lastPos
		s.hillBest = &posCopy
		s.currentTiltStep += s.hillDirection
		s.setTiltInstruction()
		return
	}

	improved := lastPos.AvgScore > s.hillBest.AvgScore+1.0

	if improved {
		posCopy := lastPos
		s.hillBest = &posCopy
		if lastPos.AvgScore > s.bestPosition.AvgScore {
			s.bestPosition = &posCopy
		}
		s.currentTiltStep += s.hillDirection
		s.setTiltInstruction()
		return
	}

	if s.hillTried == 0 {
		s.hillTried = 1
		// Return to hill best tilt
		if s.hillBest != nil {
			s.currentTiltStep = s.hillBest.TiltStep
		}
		s.hillDirection *= -1
		s.currentTiltStep += s.hillDirection
		s.setTiltInstruction()
		return
	}

	// Both directions tried
	if s.hillBest != nil {
		s.currentTiltStep = s.hillBest.TiltStep
		posCopy := *s.hillBest
		if posCopy.AvgScore > s.bestPosition.AvgScore {
			s.bestPosition = &posCopy
		}
	}
	s.advanceToNextPhase()
}

func (s *Session) setHillInstruction() {
	action := ActionTurnLeft
	dirText := "LEFT"
	if s.hillDirection > 0 {
		action = ActionTurnRight
		dirText = "RIGHT"
	}
	s.instruction = Instruction{
		Action:    action,
		AmountDeg: s.stepSize,
		Text:      fmt.Sprintf("Turn %s about %d°, then press Measure", dirText, s.stepSize),
		Phase:     s.phase,
	}
	posCount := s.countPositionsInPhase(s.phase)
	s.instruction.Progress.Current = posCount
	s.instruction.Progress.EstimatedTotal = s.estimateTotal(s.phase)
}

func (s *Session) setTiltInstruction() {
	action := ActionTiltUp
	dirText := "UP"
	if s.hillDirection < 0 {
		action = ActionTiltDown
		dirText = "DOWN"
	}
	s.instruction = Instruction{
		Action:    action,
		AmountDeg: s.stepSize,
		Text:      fmt.Sprintf("Tilt %s about %d°, then press Measure", dirText, s.stepSize),
		Phase:     PhaseTilt,
	}
	posCount := s.countPositionsInPhase(PhaseTilt)
	s.instruction.Progress.Current = posCount
	s.instruction.Progress.EstimatedTotal = 6
}

func (s *Session) advanceToNextPhase() {
	switch s.phase {
	case PhaseCoarse:
		s.transitionToPhase(PhaseFineAz10)
	case PhaseFineAz10:
		s.transitionToPhase(PhaseFineAz5)
	case PhaseFineAz5:
		s.transitionToPhase(PhaseTilt)
	case PhaseTilt:
		s.transitionToPhase(PhaseRecheck)
	case PhaseRecheck:
		s.transitionToPhase(PhaseDone)
	case PhaseDone:
		// Already done
	}
}

func (s *Session) finishSession() {
	s.phase = PhaseDone
	text := "Alignment complete!"
	if s.bestPosition != nil {
		azDeg := s.bestPosition.AzimuthStep * s.getCoarseStepDeg()
		tiltDeg := s.bestPosition.TiltStep * 5
		text = fmt.Sprintf("Alignment complete! Best position: azimuth %d°, tilt %d°, score %.1f",
			azDeg, tiltDeg, s.bestPosition.AvgScore)

		// If not at best, give return instructions
		if s.currentAzStep != s.bestPosition.AzimuthStep || s.currentTiltStep != s.bestPosition.TiltStep {
			text += ". Return to the best position."
		}
	}
	s.instruction = Instruction{
		Action: ActionDone,
		Text:   text,
		Phase:  PhaseDone,
	}
}

// --- helpers ---

func (s *Session) findBestInPhase(phase Phase) *Position {
	var best *Position
	for i := range s.positions {
		if s.positions[i].Phase == phase {
			if best == nil || s.positions[i].AvgScore > best.AvgScore {
				best = &s.positions[i]
			}
		}
	}
	return best
}

func (s *Session) countPositionsInPhase(phase Phase) int {
	count := 0
	for _, p := range s.positions {
		if p.Phase == phase {
			count++
		}
	}
	return count
}

func (s *Session) estimateTotal(phase Phase) int {
	switch phase {
	case PhaseCoarse:
		return 12
	case PhaseFineAz10:
		return 8
	case PhaseFineAz5:
		return 6
	case PhaseTilt:
		return 6
	case PhaseRecheck:
		return 6
	default:
		return 20
	}
}

func (s *Session) getCoarseStepDeg() int {
	// During coarse phase, each azimuth step = 30°
	// During fine phases, each step depends on stepSize
	// For navigation purposes, we use the current step size
	if s.stepSize > 0 {
		return s.stepSize
	}
	return 30
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Unused import guard
var _ = math.Round
