package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// SpeedTestResult holds the outcome of a speed test run.
type SpeedTestResult struct {
	Running      bool      `json:"running"`
	Stage        string    `json:"stage"` // "idle", "latency", "download", "upload", "done", "error"
	PingMs       float64   `json:"ping_ms"`
	DownloadMbps float64   `json:"download_mbps"`
	UploadMbps   float64   `json:"upload_mbps"`
	Error        string    `json:"error,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// SpeedTester manages speed testing routines.
type SpeedTester struct {
	mu     sync.RWMutex
	client *http.Client
	state  SpeedTestResult
	cancel context.CancelFunc
}

func NewSpeedTester() *SpeedTester {
	return &SpeedTester{
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
		state: SpeedTestResult{
			Stage: "idle",
		},
	}
}

// GetStatus returns the current or latest speed test state.
func (st *SpeedTester) GetStatus() SpeedTestResult {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.state
}

// Start initiates a speed test if one is not already running.
func (st *SpeedTester) Start() (bool, error) {
	st.mu.Lock()
	if st.state.Running {
		st.mu.Unlock()
		return false, fmt.Errorf("speed test already running")
	}

	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	st.state = SpeedTestResult{
		Running:   true,
		Stage:     "latency",
		Timestamp: time.Now(),
	}
	st.mu.Unlock()

	go st.run(ctx)
	return true, nil
}

// Stop cancels an ongoing test.
func (st *SpeedTester) Stop() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.cancel != nil {
		st.cancel()
	}
	st.state.Running = false
	st.state.Stage = "idle"
}

func (st *SpeedTester) run(ctx context.Context) {
	// 1. Latency test
	st.mu.Lock()
	st.state.Stage = "latency"
	st.mu.Unlock()

	ping, err := st.measurePing(ctx)
	if err != nil && ctx.Err() == nil {
		st.fail(fmt.Sprintf("latency test failed: %v", err))
		return
	}
	if ctx.Err() != nil {
		return
	}

	st.mu.Lock()
	st.state.PingMs = ping
	st.state.Stage = "download"
	st.mu.Unlock()

	// 2. Download test (fetch ~4MB)
	downMbps, err := st.measureDownload(ctx, 4*1024*1024)
	if err != nil && ctx.Err() == nil {
		st.fail(fmt.Sprintf("download test failed: %v", err))
		return
	}
	if ctx.Err() != nil {
		return
	}

	st.mu.Lock()
	st.state.DownloadMbps = downMbps
	st.state.Stage = "upload"
	st.mu.Unlock()

	// 3. Upload test (send ~1.5MB)
	upMbps, err := st.measureUpload(ctx, 1536*1024)
	if err != nil && ctx.Err() == nil {
		st.fail(fmt.Sprintf("upload test failed: %v", err))
		return
	}
	if ctx.Err() != nil {
		return
	}

	st.mu.Lock()
	st.state.UploadMbps = upMbps
	st.state.Stage = "done"
	st.state.Running = false
	st.mu.Unlock()
}

func (st *SpeedTester) fail(msg string) {
	st.mu.Lock()
	st.state.Running = false
	st.state.Stage = "error"
	st.state.Error = msg
	st.mu.Unlock()
}

func (st *SpeedTester) measurePing(ctx context.Context) (float64, error) {
	url := "https://speed.cloudflare.com/__down?bytes=0"
	var total time.Duration
	rounds := 3

	for i := 0; i < rounds; i++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return 0, err
		}
		start := time.Now()
		resp, err := st.client.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		total += time.Since(start)
	}

	avgMs := float64(total.Milliseconds()) / float64(rounds)
	return avgMs, nil
}

func (st *SpeedTester) measureDownload(ctx context.Context, bytesCount int) (float64, error) {
	url := fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", bytesCount)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	resp, err := st.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, err
	}

	duration := time.Since(start).Seconds()
	if duration <= 0 {
		duration = 0.001
	}

	// Mbps = (bytes * 8) / (seconds * 1,000,000)
	mbps := (float64(n) * 8.0) / (duration * 1000000.0)
	return mbps, nil
}

func (st *SpeedTester) measureUpload(ctx context.Context, bytesCount int) (float64, error) {
	url := "https://speed.cloudflare.com/__up"
	payload := make([]byte, bytesCount)
	_, _ = rand.Read(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	start := time.Now()
	resp, err := st.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	duration := time.Since(start).Seconds()
	if duration <= 0 {
		duration = 0.001
	}

	mbps := (float64(bytesCount) * 8.0) / (duration * 1000000.0)
	return mbps, nil
}

func (st *SpeedTester) MarshalJSON() ([]byte, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return json.Marshal(st.state)
}
