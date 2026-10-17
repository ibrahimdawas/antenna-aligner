package main

import (
	"antenna-aligner/engine"
	"antenna-aligner/router"
	"antenna-aligner/towers"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

// --- Ring buffer for recent samples ---

type RingBuffer struct {
	mu      sync.RWMutex
	samples []router.Sample
	maxSize int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{maxSize: size, samples: make([]router.Sample, 0, size)}
}

func (rb *RingBuffer) Add(s *router.Sample) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.samples) >= rb.maxSize {
		rb.samples = rb.samples[1:]
	}
	rb.samples = append(rb.samples, *s)
}

func (rb *RingBuffer) Latest() *router.Sample {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if len(rb.samples) == 0 {
		return nil
	}
	s := rb.samples[len(rb.samples)-1]
	return &s
}

// --- SSE Broadcaster ---

type SSEBroadcaster struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

func NewSSEBroadcaster() *SSEBroadcaster {
	return &SSEBroadcaster{clients: make(map[chan string]struct{})}
}

func (b *SSEBroadcaster) AddClient() chan string {
	ch := make(chan string, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *SSEBroadcaster) RemoveClient(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *SSEBroadcaster) Broadcast(eventType, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n", eventType, data)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			// Drop message for slow client
		}
	}
}

func (b *SSEBroadcaster) BroadcastSample(s *router.Sample, score float64, quality string) {
	type sampleEvent struct {
		Sample  *router.Sample `json:"sample"`
		Score   float64        `json:"score"`
		Quality string         `json:"quality"`
	}
	data, err := json.Marshal(sampleEvent{Sample: s, Score: score, Quality: quality})
	if err != nil {
		return
	}
	b.Broadcast("sample", string(data))
}

func (b *SSEBroadcaster) BroadcastSession(state engine.SessionState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	b.Broadcast("session", string(data))
}

func (b *SSEBroadcaster) BroadcastTowers(state towers.TowersState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	b.Broadcast("towers", string(data))
}

func (b *SSEBroadcaster) BroadcastError(errMsg string) {
	type errEvent struct {
		Error string `json:"error"`
	}
	data, _ := json.Marshal(errEvent{Error: errMsg})
	b.Broadcast("error", string(data))
}

// --- Global state ---

var (
	cfg                Config
	resolvedConfigPath string = "config.json"
	mockMode           bool
	source             router.SignalSource
	mockSource         *router.MockSource
	session            *engine.Session
	broadcaster        *SSEBroadcaster
	ringBuf            *RingBuffer
	speedTester        *engine.SpeedTester
	routerStatus       string = "connecting"
	statusMu           sync.RWMutex

	// Tower map state
	towerCache        *towers.TowerCache
	towerSource       towers.TowerSource
	manualSource      *towers.ManualSource
	lastServingCellID string
	lastServingTower  *towers.TowerWithBearing
	towersMu          sync.RWMutex
	lastRefreshTime   time.Time
)

func setRouterStatus(s string) {
	statusMu.Lock()
	routerStatus = s
	statusMu.Unlock()
}

func getRouterStatus() string {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return routerStatus
}

func initTowerSources() {
	cachePath := "towers_cache.json"
	if mockMode {
		cachePath = "" // in-memory only in mock mode, never pollute real disk cache!
	}
	towerCache = towers.NewTowerCache(cachePath)
	manualSource = towers.NewManualSource(towerCache)

	switch cfg.TowerSource {
	case "opencellid":
		if cfg.OpenCelliDToken != "" {
			towerSource = towers.NewOpenCelliDSource(cfg.OpenCelliDToken, towerCache)
			log.Println("Tower Source: OpenCelliD API")
		} else {
			towerSource = manualSource
			log.Println("Tower Source: OpenCelliD (no token configured, using manual/cache)")
		}
	case "csv":
		if cfg.TowersCSVPath != "" {
			csvSrc, err := towers.NewCSVSource(cfg.TowersCSVPath, towerCache)
			if err != nil {
				log.Printf("Failed to load CSV at %s: %v; falling back to manual", cfg.TowersCSVPath, err)
				towerSource = manualSource
			} else {
				towerSource = csvSrc
				log.Printf("Tower Source: CSV file (%s)", cfg.TowersCSVPath)
			}
		} else {
			towerSource = manualSource
			log.Println("Tower Source: CSV (no path configured, using manual/cache)")
		}
	default:
		towerSource = manualSource
		log.Println("Tower Source: Manual")
	}

	// In mock mode, pre-seed a couple of towers for realistic Libya / sample testing
	if mockMode {
		_ = manualSource.AddOrUpdateTower(towers.Tower{
			ID:       towers.MakeTowerID(606, 1, 48225),
			Lat:      32.8925,
			Lon:      13.1930,
			MCC:      606,
			MNC:      1,
			ENB:      48225,
			Source:   "manual",
			Accuracy: 100,
			Cells:    []towers.CellInfo{{Sector: 78, Band: "B3"}},
		})
		_ = manualSource.AddOrUpdateTower(towers.Tower{
			ID:       towers.MakeTowerID(606, 1, 48226),
			Lat:      32.8750,
			Lon:      13.1700,
			MCC:      606,
			MNC:      1,
			ENB:      48226,
			Source:   "manual",
			Accuracy: 250,
			Cells:    []towers.CellInfo{{Sector: 78, Band: "B3"}},
		})
	}
}

func main() {
	configPath := flag.String("config", "", "Path to config.json")
	mock := flag.Bool("mock", false, "Use simulated router (no hardware needed)")
	flag.Parse()

	mockMode = *mock

	// Determine config path
	if *configPath != "" {
		resolvedConfigPath = *configPath
	} else if _, err := os.Stat("config.json"); err == nil {
		resolvedConfigPath = "config.json"
	}

	// Load config
	var err error
	if resolvedConfigPath != "" {
		cfg, err = LoadConfig(resolvedConfigPath)
		if err != nil {
			log.Printf("Could not load config from %s: %v (using defaults)", resolvedConfigPath, err)
			cfg = DefaultConfig()
		}
	} else {
		cfg = DefaultConfig()
	}

	// Initialize towers
	initTowerSources()

	// Create signal source
	if mockMode {
		mockSource = router.NewMockSource()
		source = mockSource
		log.Println("Mode: MOCK (simulated router)")
	} else {
		source = router.NewHuaweiClient(cfg.RouterURL, cfg.Username, cfg.Password)
		log.Printf("Mode: Real router at %s", cfg.RouterURL)
	}

	// Create broadcaster and ring buffer
	broadcaster = NewSSEBroadcaster()
	ringBuf = NewRingBuffer(300)

	// Create session
	session = engine.NewSession(cfg.SettleSeconds, cfg.MeasureSeconds, func() {
		state := session.GetState()
		broadcaster.BroadcastSession(state)
	})

	// Start poller
	go startPoller()

	// Set up HTTP routes
	mux := http.NewServeMux()

	// Serve embedded static files
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(webSub))

	mux.Handle("/", fileServer)

	mux.HandleFunc("/events", handleSSE)
	mux.HandleFunc("/api/state", handleState)
	mux.HandleFunc("/api/session/start", handleSessionStart)
	mux.HandleFunc("/api/session/measure", handleSessionMeasure)
	mux.HandleFunc("/api/session/remeasure", handleSessionRemeasure)
	mux.HandleFunc("/api/session/skip", handleSessionSkip)
	mux.HandleFunc("/api/session/mark", handleSessionMark)
	mux.HandleFunc("/api/session/reset", handleSessionReset)
	mux.HandleFunc("/api/session/full-scan", handleSessionFullScan)
	mux.HandleFunc("/api/export.csv", handleExportCSV)
	mux.HandleFunc("/api/mock/move", handleMockMove)

	// Tower routes
	mux.HandleFunc("/api/towers", handleTowersGet)
	mux.HandleFunc("/api/location", handleLocationPost)
	mux.HandleFunc("/api/towers/manual", handleTowersManualPost)
	mux.HandleFunc("/api/towers/refresh", handleTowersRefreshPost)

	// Initialize speed tester
	speedTester = engine.NewSpeedTester()

	// Print startup info
	log.Printf("Listen: %s", cfg.Listen)
	printLocalURLs(cfg.Listen)

	// Routes
	mux.HandleFunc("/api/speedtest/start", handleSpeedTestStart)
	mux.HandleFunc("/api/speedtest/status", handleSpeedTestStatus)
	mux.HandleFunc("/api/speedtest/stop", handleSpeedTestStop)

	// Start server
	server := &http.Server{
		Addr:    cfg.Listen,
		Handler: withRecover(mux),
	}
	log.Fatal(server.ListenAndServe())
}

func startPoller() {
	interval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		sample, err := source.FetchSample()
		if err != nil {
			setRouterStatus(fmt.Sprintf("error: %s", err.Error()))
			broadcaster.BroadcastError(err.Error())
			continue
		}

		setRouterStatus("connected")
		ringBuf.Add(sample)
		session.AddSample(sample)

		score := engine.Score(sample)
		quality := engine.QualityLabel(sample)
		broadcaster.BroadcastSample(sample, score, quality)

		checkTowerUpdate(sample)
	}
}

func checkTowerUpdate(sample *router.Sample) {
	if sample == nil {
		return
	}

	var enb int
	if sample.ENB != nil {
		enb = *sample.ENB
	} else if sample.CellID != "" {
		parsedENB, _, err := engine.ParseECIString(sample.CellID)
		if err == nil {
			enb = parsedENB
		}
	}

	mcc := 0
	mnc := 0
	if sample.MCC != nil {
		mcc = *sample.MCC
	}
	if sample.MNC != nil {
		mnc = *sample.MNC
	}
	tac := 0
	if sample.TAC != nil {
		tac = *sample.TAC
	}

	towersMu.Lock()
	cellChanged := sample.CellID != "" && sample.CellID != lastServingCellID
	lastServingCellID = sample.CellID

	if enb > 0 && towerSource != nil {
		tw, err := towerSource.Lookup(mcc, mnc, tac, enb)
		if err == nil && tw != nil {
			var dist, bearing float64
			var compass string
			unreliable := false

			if cfg.HasLocation() {
				dist = engine.HaversineDistanceM(cfg.Location.Lat, cfg.Location.Lon, tw.Lat, tw.Lon)
				bearing = engine.InitialBearing(cfg.Location.Lat, cfg.Location.Lon, tw.Lat, tw.Lon)
				compass = engine.CompassText(bearing)

				if dist > 50000 {
					unreliable = true
					session.ClearTowerHint()
				} else {
					session.SetTowerHint(bearing, dist)
				}
			} else {
				session.ClearTowerHint()
			}

			// Add cell band/pci to tower cells if available
			if sample.Band != "" || sample.PCI != "" {
				sec := 0
				if sample.Sector != nil {
					sec = *sample.Sector
				}
				foundSec := false
				for i := range tw.Cells {
					if tw.Cells[i].Sector == sec {
						if sample.Band != "" {
							tw.Cells[i].Band = sample.Band
						}
						if sample.PCI != "" {
							tw.Cells[i].PCI = sample.PCI
						}
						foundSec = true
						break
					}
				}
				if !foundSec {
					tw.Cells = append(tw.Cells, towers.CellInfo{
						Sector: sec,
						PCI:    sample.PCI,
						Band:   sample.Band,
					})
				}
			}

			lastServingTower = &towers.TowerWithBearing{
				Tower:       *tw,
				DistanceM:   dist,
				BearingDeg:  bearing,
				CompassText: compass,
				IsServing:   true,
				Unreliable:  unreliable,
			}
		} else {
			lastServingTower = nil
			session.ClearTowerHint()
		}
	} else {
		lastServingTower = nil
		session.ClearTowerHint()
	}
	towersMu.Unlock()

	if cellChanged {
		tState := getTowersState(false)
		broadcaster.BroadcastTowers(tState)
	}
}

func getTowersState(showAll bool) towers.TowersState {
	towersMu.RLock()
	defer towersMu.RUnlock()

	var userLat, userLon, userAcc *float64
	hasLoc := cfg.HasLocation()
	if hasLoc {
		lat := cfg.Location.Lat
		lon := cfg.Location.Lon
		userLat = &lat
		userLon = &lon
		if cfg.Location.Accuracy != nil {
			acc := *cfg.Location.Accuracy
			userAcc = &acc
		}
	}

	latestSample := ringBuf.Latest()
	var servingCellID string
	var servingENB int
	if latestSample != nil {
		servingCellID = latestSample.CellID
		if latestSample.ENB != nil {
			servingENB = *latestSample.ENB
		}
	}

	state := towers.TowersState{
		UserLat:       userLat,
		UserLon:       userLon,
		UserAccuracy:  userAcc,
		SourceName:    cfg.TowerSource,
		Serving:       lastServingTower,
		ServingCellID: servingCellID,
		ServingENB:    servingENB,
	}
	if state.SourceName == "" {
		state.SourceName = "manual"
	}

	if hasLoc && towerSource != nil {
		radius := cfg.TowerRadiusM
		if radius <= 0 {
			radius = 10000
		}
		rawNearby, err := towerSource.Nearby(cfg.Location.Lat, cfg.Location.Lon, radius)
		if err != nil {
			state.Error = err.Error()
			state.Offline = true
		}

		var servingMCC, servingMNC int
		if lastServingTower != nil {
			servingMCC = lastServingTower.MCC
			servingMNC = lastServingTower.MNC
		} else if latestSample != nil && latestSample.MCC != nil && latestSample.MNC != nil {
			servingMCC = *latestSample.MCC
			servingMNC = *latestSample.MNC
		}

		var nearbyWithBearing []towers.TowerWithBearing
		for _, tw := range rawNearby {
			if !showAll && servingMCC > 0 && (tw.MCC != servingMCC || tw.MNC != servingMNC) {
				continue
			}

			dist := engine.HaversineDistanceM(cfg.Location.Lat, cfg.Location.Lon, tw.Lat, tw.Lon)
			bearing := engine.InitialBearing(cfg.Location.Lat, cfg.Location.Lon, tw.Lat, tw.Lon)
			compass := engine.CompassText(bearing)

			isServing := false
			if lastServingTower != nil && tw.ID == lastServingTower.ID {
				isServing = true
			}

			nearbyWithBearing = append(nearbyWithBearing, towers.TowerWithBearing{
				Tower:       tw,
				DistanceM:   dist,
				BearingDeg:  bearing,
				CompassText: compass,
				IsServing:   isServing,
				Unreliable:  dist > 50000,
			})
		}

		sort.Slice(nearbyWithBearing, func(i, j int) bool {
			return nearbyWithBearing[i].DistanceM < nearbyWithBearing[j].DistanceM
		})

		state.Nearby = nearbyWithBearing
	}

	return state
}

// --- HTTP Handlers ---

func handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := broadcaster.AddClient()
	defer broadcaster.RemoveClient(ch)

	// Send initial state
	state := session.GetState()
	stateData, _ := json.Marshal(state)
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", stateData)

	// Send initial towers state
	tState := getTowersState(false)
	tData, _ := json.Marshal(tState)
	fmt.Fprintf(w, "event: towers\ndata: %s\n\n", tData)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "%s\n", msg)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

type FullState struct {
	RouterStatus string              `json:"router_status"`
	MockMode     bool                `json:"mock_mode"`
	Sample       *router.Sample      `json:"sample"`
	Score        float64             `json:"score"`
	Quality      string              `json:"quality"`
	Session      engine.SessionState `json:"session"`
	Towers       towers.TowersState  `json:"towers"`
}

func handleState(w http.ResponseWriter, r *http.Request) {
	latest := ringBuf.Latest()
	var score float64
	var quality string
	if latest != nil {
		score = engine.Score(latest)
		quality = engine.QualityLabel(latest)
	}

	state := FullState{
		RouterStatus: getRouterStatus(),
		MockMode:     mockMode,
		Sample:       latest,
		Score:        score,
		Quality:      quality,
		Session:      session.GetState(),
		Towers:       getTowersState(false),
	}

	writeJSON(w, http.StatusOK, state)
}

func handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	switch engine.SessionMode(req.Mode) {
	case engine.ModeGuided, engine.ModeLive, engine.ModeSpeed:
		session.Start(engine.SessionMode(req.Mode))
		writeOK(w)
	default:
		writeError(w, http.StatusBadRequest, "mode must be 'guided', 'live' or 'speed'")
	}
}

func handleSessionMeasure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.StartMeasure()
	writeOK(w)
}

func handleSessionRemeasure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.Remeasure()
	writeOK(w)
}

func handleSessionSkip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.SkipPhase()
	writeOK(w)
}

func handleSessionMark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.Mark()
	writeOK(w)
}

func handleSessionReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.Reset()
	writeOK(w)
}

func handleSessionFullScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	session.RunFullScan()
	writeOK(w)
}

func handleExportCSV(w http.ResponseWriter, r *http.Request) {
	state := session.GetState()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=antenna-positions.csv")
	if err := engine.ExportCSV(state.Positions, w); err != nil {
		log.Printf("CSV export error: %v", err)
	}
}

func handleTowersGet(w http.ResponseWriter, r *http.Request) {
	showAll := r.URL.Query().Get("all") == "true"
	state := getTowersState(showAll)
	writeJSON(w, http.StatusOK, state)
}

func handleLocationPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Lat      float64  `json:"lat"`
		Lon      float64  `json:"lon"`
		Accuracy *float64 `json:"accuracy,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Lat < -90 || req.Lat > 90 || req.Lon < -180 || req.Lon > 180 {
		writeError(w, http.StatusBadRequest, "invalid coordinates")
		return
	}

	cfg.Location = &LocationConfig{Lat: req.Lat, Lon: req.Lon, Accuracy: req.Accuracy}
	if err := SaveConfig(resolvedConfigPath, cfg); err != nil {
		log.Printf("Failed to save config: %v", err)
	}

	sample := ringBuf.Latest()
	if sample != nil {
		checkTowerUpdate(sample)
	}

	tState := getTowersState(false)
	broadcaster.BroadcastTowers(tState)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "location": cfg.Location})
}

func handleTowersManualPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req towers.Tower
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Lat == 0 && req.Lon == 0 {
		writeError(w, http.StatusBadRequest, "valid lat/lon required")
		return
	}

	if manualSource == nil {
		writeError(w, http.StatusInternalServerError, "manual source not initialized")
		return
	}

	if err := manualSource.AddOrUpdateTower(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sample := ringBuf.Latest()
	if sample != nil {
		checkTowerUpdate(sample)
	}

	tState := getTowersState(false)
	broadcaster.BroadcastTowers(tState)
	writeOK(w)
}

func handleTowersRefreshPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	towersMu.Lock()
	if time.Since(lastRefreshTime) < 5*time.Second {
		towersMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "Please wait 5 seconds before refreshing again")
		return
	}
	lastRefreshTime = time.Now()
	towersMu.Unlock()

	sample := ringBuf.Latest()
	if sample != nil {
		checkTowerUpdate(sample)
	}

	tState := getTowersState(false)
	broadcaster.BroadcastTowers(tState)
	writeOK(w)
}

func handleMockMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if !mockMode || mockSource == nil {
		writeError(w, http.StatusBadRequest, "not in mock mode")
		return
	}

	var req struct {
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	switch req.Direction {
	case "left":
		mockSource.Move(-10, 0)
	case "right":
		mockSource.Move(10, 0)
	case "up":
		mockSource.Move(0, 5)
	case "down":
		mockSource.Move(0, -5)
	default:
		writeError(w, http.StatusBadRequest, "direction must be left/right/up/down")
		return
	}
	writeOK(w)
}

func handleSpeedTestStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	started, err := speedTester.Start()
	if !started {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w)
}

func handleSpeedTestStatus(w http.ResponseWriter, r *http.Request) {
	status := speedTester.GetStatus()
	writeJSON(w, http.StatusOK, status)
}

func handleSpeedTestStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	speedTester.Stop()
	writeOK(w)
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"ok": false, "error": msg})
}

// withRecover wraps a handler with panic recovery.
func withRecover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	})
}

// printLocalURLs prints helpful URLs for the user.
func printLocalURLs(listen string) {
	_, port, _ := net.SplitHostPort(listen)
	if port == "" {
		port = "8080"
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("Open http://localhost:%s on your phone", port)
		return
	}

	log.Println("Open one of these URLs on your phone:")
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 != nil {
			log.Printf("  http://%s:%s", ip4.String(), port)
		}
	}
}
