package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scalp-assist/internal/domain"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load(config.example.yaml) returned error: %v", err)
	}

	if cfg.Settings.TopN != 20 {
		t.Fatalf("TopN = %d, want 20", cfg.Settings.TopN)
	}
	if cfg.DBPath == "" {
		t.Fatal("DBPath must not be empty")
	}
	if cfg.Arbitrage.MakerFeesBps["binance"] != 2 {
		t.Fatalf("Binance maker fee = %v, want 2", cfg.Arbitrage.MakerFeesBps["binance"])
	}
}

func TestLoadAppliesArbitrageDefaultsToLegacyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.yaml")
	raw := []byte(`
db_path: ./test.db
cycles:
  fast_interval: 15m
  slow_interval: 2h
settings:
  min_volume_usd: 100000000
  min_change_pct: 20
  corr_threshold: 0.8
  inplay:
    market_threshold_pct: 3
    multiplier: 2
  weights:
    volatility: 0.4
    turnover: 0.3
    inplay: 0.2
    corr: 0.1
  top_n: 20
`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cycles.ArbitrageInterval != 10*time.Second || cfg.Arbitrage.TopN != 30 ||
		cfg.Arbitrage.MakerFeesBps["okx"] != 2 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestValidateSettingsRejectsInvalidValues(t *testing.T) {
	valid := domain.Settings{
		MinVolumeUSD:       100_000_000,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay: domain.InplaySettings{
			MarketThresholdPct: 3,
			Multiplier:         2,
		},
		Weights: domain.ScoreWeights{
			Volatility: 0.4,
			Turnover:   0.3,
			Inplay:     0.2,
			Corr:       0.1,
		},
		TopN: 20,
	}

	tests := map[string]func(domain.Settings) domain.Settings{
		"negative volume": func(s domain.Settings) domain.Settings {
			s.MinVolumeUSD = -1
			return s
		},
		"corr too high": func(s domain.Settings) domain.Settings {
			s.CorrThreshold = 1.1
			return s
		},
		"zero top n": func(s domain.Settings) domain.Settings {
			s.TopN = 0
			return s
		},
		"negative weight": func(s domain.Settings) domain.Settings {
			s.Weights.Turnover = -0.1
			return s
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSettings(mutate(valid)); err == nil {
				t.Fatal("ValidateSettings returned nil error")
			}
		})
	}
}

func TestEffectiveAppliesOverrides(t *testing.T) {
	defaults := domain.Settings{
		MinVolumeUSD:       100_000_000,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay: domain.InplaySettings{
			MarketThresholdPct: 3,
			Multiplier:         2,
		},
		Weights: domain.ScoreWeights{
			Volatility: 0.4,
			Turnover:   0.3,
			Inplay:     0.2,
			Corr:       0.1,
		},
		TopN: 20,
	}

	got, err := Effective(defaults, map[string]string{
		"min_volume_usd":   "250000000",
		"weights.turnover": "0.45",
		"top_n":            "30",
	})
	if err != nil {
		t.Fatalf("Effective returned error: %v", err)
	}

	if got.MinVolumeUSD != 250_000_000 {
		t.Fatalf("MinVolumeUSD = %v, want %v", got.MinVolumeUSD, 250_000_000.0)
	}
	if got.Weights.Turnover != 0.45 {
		t.Fatalf("Weights.Turnover = %v, want 0.45", got.Weights.Turnover)
	}
	if got.TopN != 30 {
		t.Fatalf("TopN = %d, want 30", got.TopN)
	}
	if defaults.TopN != 20 {
		t.Fatalf("defaults mutated: TopN = %d, want 20", defaults.TopN)
	}
}

func TestEffectiveRejectsInvalidOverrideWithoutPartialApply(t *testing.T) {
	defaults := domain.Settings{
		MinVolumeUSD:       100_000_000,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay: domain.InplaySettings{
			MarketThresholdPct: 3,
			Multiplier:         2,
		},
		Weights: domain.ScoreWeights{
			Volatility: 0.4,
			Turnover:   0.3,
			Inplay:     0.2,
			Corr:       0.1,
		},
		TopN: 20,
	}

	if _, err := Effective(defaults, map[string]string{
		"top_n":          "30",
		"corr_threshold": "1.5",
	}); err == nil {
		t.Fatal("Effective returned nil error")
	}

	if defaults.TopN != 20 || defaults.CorrThreshold != 0.8 {
		t.Fatalf("defaults mutated: %+v", defaults)
	}
}
