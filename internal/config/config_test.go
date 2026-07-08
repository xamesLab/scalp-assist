package config

import (
	"path/filepath"
	"testing"

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
