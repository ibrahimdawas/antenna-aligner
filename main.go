package main

import (
	"antenna-aligner/engine"
	"antenna-aligner/router"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
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

func (b *SSEBroadcaster) BroadcastError(errMsg string) {
	type errEvent struct {
		Error string `json:"error"`
	}
	data, _ := json.Marshal(errEvent{Error: errMsg})
	b.Broadcast("error", string(data))
}

// --- Global state ---

var (
	cfg         Config
	mockMode    bool
	source      router.SignalSource
	mockSource  *router.MockSource
	session      *engine.Session
	broadcaster  *SSEBroadcaster
	ringBuf      *RingBuffer
	speedTester  *engine.SpeedTester
	routerStatus string = "connecting"
	statusMu     sync.RWMutex
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

func main() {
	configPath := flag.String("config", "", "Path to config.json")
	mock := flag.Bool("mock", false, "Use simulated router (no hardware needed)")
	flag.Parse()

	mockMode = *mock

	// Load config
	var err error
	if *configPath != "" {
		cfg, err = LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		cfg = DefaultConfig()
	}

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
	mux.HandleFunc("/api/export.csv", handleExportCSV)
	mux.HandleFunc("/api/mock/move", handleMockMove)

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
	}
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

func handleExportCSV(w http.ResponseWriter, r *http.Request) {
	state := session.GetState()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=antenna-positions.csv")
	if err := engine.ExportCSV(state.Positions, w); err != nil {
		log.Printf("CSV export error: %v", err)
	}
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
