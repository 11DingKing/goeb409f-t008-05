package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"microgrid/internal/domain"
)

// Config holds the operational configuration loaded from disk and environment.
type Config struct {
	HTTPAddr           string        `json:"http_addr"`
	MonitorInterval    time.Duration `json:"-"`
	MonitorIntervalRaw Duration      `json:"monitor_interval"`
	Settings           Settings      `json:"settings"`
}

// Settings are the tunable business parameters in their JSON form.
type Settings struct {
	SyncLimits         SyncLimits `json:"sync_limits"`
	DeviationThreshold float64    `json:"deviation_threshold"`
	BlackStartWindow   Duration   `json:"black_start_window"`
	DieselStartWindow  Duration   `json:"diesel_start_window"`
	MaxBreakerFailures int        `json:"max_breaker_failures"`
}

// SyncLimits mirrors domain.SyncLimits for JSON parsing.
type SyncLimits struct {
	MaxPhaseAngleDeg float64 `json:"max_phase_angle_deg"`
	MaxFrequencyHz   float64 `json:"max_frequency_hz"`
	MaxVoltagePct    float64 `json:"max_voltage_pct"`
}

// Duration is a time.Duration that parses from JSON strings ("5s") or numbers
// (interpreted as seconds).
type Duration time.Duration

// UnmarshalJSON parses a duration from a string or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}
	*d = Duration(time.Duration(n) * time.Second)
	return nil
}

// Load reads configuration from path, falling back to defaults when the file
// is absent or empty. Environment variables override the file.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("parse config %q: %w", path, err)
			}
		}
	}
	cfg = applyDefaults(cfg)
	cfg = applyEnv(cfg)
	cfg.MonitorInterval = time.Duration(cfg.MonitorIntervalRaw)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Default returns the default configuration.
func Default() Config {
	return applyDefaults(Config{})
}

func applyDefaults(cfg Config) Config {
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":51326"
	}
	if cfg.MonitorIntervalRaw <= 0 {
		cfg.MonitorIntervalRaw = Duration(5 * time.Second)
	}
	s := &cfg.Settings
	if s.SyncLimits.MaxPhaseAngleDeg <= 0 {
		s.SyncLimits.MaxPhaseAngleDeg = 15
	}
	if s.SyncLimits.MaxFrequencyHz <= 0 {
		s.SyncLimits.MaxFrequencyHz = 0.1
	}
	if s.SyncLimits.MaxVoltagePct <= 0 {
		s.SyncLimits.MaxVoltagePct = 5
	}
	if s.DeviationThreshold <= 0 {
		s.DeviationThreshold = 0.15
	}
	if s.BlackStartWindow <= 0 {
		s.BlackStartWindow = Duration(5 * time.Minute)
	}
	if s.DieselStartWindow <= 0 {
		s.DieselStartWindow = Duration(10 * time.Minute)
	}
	if s.MaxBreakerFailures <= 0 {
		s.MaxBreakerFailures = 2
	}
	return cfg
}

func applyEnv(cfg Config) Config {
	if v := os.Getenv("MICROGRID_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("MICROGRID_MONITOR_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MonitorIntervalRaw = Duration(d)
		}
	}
	return cfg
}

func (c Config) validate() error {
	if time.Duration(c.MonitorIntervalRaw) <= 0 {
		return errors.New("monitor_interval must be positive")
	}
	if c.Settings.MaxBreakerFailures < 1 {
		return errors.New("max_breaker_failures must be >= 1")
	}
	if c.Settings.DeviationThreshold <= 0 {
		return errors.New("deviation_threshold must be positive")
	}
	return nil
}

// ToDomainSettings converts the config settings into domain.Settings.
func (c Config) ToDomainSettings() domain.Settings {
	return domain.Settings{
		Limits: domain.SyncLimits{
			MaxPhaseAngleDeg: c.Settings.SyncLimits.MaxPhaseAngleDeg,
			MaxFrequencyHz:   c.Settings.SyncLimits.MaxFrequencyHz,
			MaxVoltagePct:    c.Settings.SyncLimits.MaxVoltagePct,
		},
		DeviationThreshold: c.Settings.DeviationThreshold,
		BlackStartWindow:   time.Duration(c.Settings.BlackStartWindow),
		DieselStartWindow:  time.Duration(c.Settings.DieselStartWindow),
		MaxBreakerFailures: c.Settings.MaxBreakerFailures,
	}
}
