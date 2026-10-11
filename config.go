package main

import (
	"encoding/json"
	"os"
)

// LocationConfig holds the user's antenna position.
type LocationConfig struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Config holds all application settings.
type Config struct {
	RouterURL      string `json:"router_url"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Listen         string `json:"listen"`
	PollIntervalMs int    `json:"poll_interval_ms"`
	SettleSeconds  int    `json:"settle_seconds"`
	MeasureSeconds int    `json:"measure_seconds"`

	// Tower map settings
	Location       *LocationConfig `json:"location,omitempty"`
	TowerSource    string          `json:"tower_source,omitempty"`    // "opencellid", "csv", "manual"
	OpenCelliDToken string         `json:"opencellid_token,omitempty"`
	TowersCSVPath  string          `json:"towers_csv_path,omitempty"`
	TowerRadiusM   int             `json:"tower_radius_m,omitempty"`
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
		TowerSource:    "manual",
		TowerRadiusM:   10000,
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

// SaveConfig writes the config to a JSON file.
func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// HasLocation returns true if a valid location is configured.
func (c *Config) HasLocation() bool {
	return c.Location != nil && (c.Location.Lat != 0 || c.Location.Lon != 0)
}
