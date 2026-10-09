package main

import (
	"encoding/json"
	"os"
)

// Config holds all application settings.
type Config struct {
	RouterURL      string `json:"router_url"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Listen         string `json:"listen"`
	PollIntervalMs int    `json:"poll_interval_ms"`
	SettleSeconds  int    `json:"settle_seconds"`
	MeasureSeconds int    `json:"measure_seconds"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		RouterURL:      "http://192.168.1.1",
		Username:       "admin",
		Password:       "admin",
		Listen:         ":8080",
		PollIntervalMs: 1000,
		SettleSeconds:  5,
		MeasureSeconds: 12,
	}
}

// LoadConfig reads a JSON config file, overlaying values on top of defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}
