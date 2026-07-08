package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"scalp-assist/internal/domain"
)

type Config struct {
	Domain                 string            `yaml:"domain"`
	ListenHTTPS            string            `yaml:"listen_https"`
	ListenHTTP             string            `yaml:"listen_http"`
	DevHTTP                string            `yaml:"dev_http"`
	DBPath                 string            `yaml:"db_path"`
	Cycles                 CycleConfig       `yaml:"cycles"`
	Settings               domain.Settings   `yaml:"settings"`
	Blacklist              []string          `yaml:"blacklist"`
	NormalizationOverrides map[string]string `yaml:"normalization_overrides"`
	Auth                   AuthConfig        `yaml:"auth"`
}

type CycleConfig struct {
	FastInterval time.Duration `yaml:"fast_interval"`
	SlowInterval time.Duration `yaml:"slow_interval"`
}

type AuthConfig struct {
	BootstrapEmail    string `yaml:"bootstrap_email"`
	BootstrapPassword string `yaml:"bootstrap_password"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if cfg.DBPath == "" {
		return errors.New("db_path is required")
	}
	if cfg.Cycles.FastInterval <= 0 {
		return errors.New("cycles.fast_interval must be positive")
	}
	if cfg.Cycles.SlowInterval <= 0 {
		return errors.New("cycles.slow_interval must be positive")
	}
	return ValidateSettings(cfg.Settings)
}

func Effective(defaults domain.Settings, overrides map[string]string) (domain.Settings, error) {
	next := defaults

	for key, raw := range overrides {
		switch key {
		case "min_volume_usd":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.MinVolumeUSD = v
		case "min_change_pct":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.MinChangePct = v
		case "enable_change_filter":
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return domain.Settings{}, fmt.Errorf("%s must be boolean: %w", key, err)
			}
			next.EnableChangeFilter = v
		case "corr_threshold":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.CorrThreshold = v
		case "inplay.market_threshold_pct":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Inplay.MarketThresholdPct = v
		case "inplay.multiplier":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Inplay.Multiplier = v
		case "weights.volatility":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Weights.Volatility = v
		case "weights.turnover":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Weights.Turnover = v
		case "weights.inplay":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Weights.Inplay = v
		case "weights.corr":
			v, err := parseFloat(key, raw)
			if err != nil {
				return domain.Settings{}, err
			}
			next.Weights.Corr = v
		case "top_n":
			v, err := strconv.Atoi(raw)
			if err != nil {
				return domain.Settings{}, fmt.Errorf("%s must be integer: %w", key, err)
			}
			next.TopN = v
		default:
			return domain.Settings{}, fmt.Errorf("unknown settings override %q", key)
		}
	}

	if err := ValidateSettings(next); err != nil {
		return domain.Settings{}, err
	}
	return next, nil
}

func ValidateSettings(settings domain.Settings) error {
	if settings.MinVolumeUSD <= 0 {
		return errors.New("settings.min_volume_usd must be positive")
	}
	if settings.MinChangePct < 0 {
		return errors.New("settings.min_change_pct must be non-negative")
	}
	if settings.CorrThreshold < 0 || settings.CorrThreshold > 1 {
		return errors.New("settings.corr_threshold must be between 0 and 1")
	}
	if settings.Inplay.MarketThresholdPct < 0 {
		return errors.New("settings.inplay.market_threshold_pct must be non-negative")
	}
	if settings.Inplay.Multiplier <= 0 {
		return errors.New("settings.inplay.multiplier must be positive")
	}
	if settings.TopN < 1 || settings.TopN > 100 {
		return errors.New("settings.top_n must be between 1 and 100")
	}
	if settings.Weights.Volatility < 0 ||
		settings.Weights.Turnover < 0 ||
		settings.Weights.Inplay < 0 ||
		settings.Weights.Corr < 0 {
		return errors.New("settings.weights must be non-negative")
	}
	return nil
}

func parseFloat(key, raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be number: %w", key, err)
	}
	return v, nil
}
