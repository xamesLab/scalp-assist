package metrics

import (
	"testing"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
)

func TestBuildRowsCalculatesMetricsAndUsesBinancePriority(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.EnableChangeFilter = false

	items := []normalize.InstrumentData{
		{
			Instrument: domain.Instrument{Base: "AAA", Multiplier: 1, OnBinance: true, OnBybit: true},
			BinanceTicker: &domain.Ticker{
				Exchange:     domain.ExchangeBinance,
				Status:       "TRADING",
				Price:        100,
				High24h:      130,
				Low24h:       90,
				ChangePct24h: 10,
				TurnoverUSD:  100,
				FundingRate:  0.001,
				TickSize:     0.01,
			},
			BybitTicker: &domain.Ticker{
				Exchange:     domain.ExchangeBybit,
				Status:       "Trading",
				Price:        200,
				High24h:      220,
				Low24h:       180,
				ChangePct24h: 99,
				TurnoverUSD:  200,
				FundingRate:  0.005,
				TickSize:     0.5,
			},
		},
	}

	rows := BuildRows(items, settings, Options{})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.TurnoverUSD != 300 {
		t.Fatalf("TurnoverUSD = %v, want 300", row.TurnoverUSD)
	}
	if row.Price != 100 || row.ChangePct24h != 10 || row.FundingRate != 0.001 {
		t.Fatalf("Binance priority failed: %+v", row)
	}
	wantRange := (130.0 - 90.0) / 90.0 * 100
	if row.DayRangePct != wantRange {
		t.Fatalf("DayRangePct = %v, want %v", row.DayRangePct, wantRange)
	}
	if row.RelativeTickPct != 0.01 {
		t.Fatalf("RelativeTickPct = %v, want 0.01", row.RelativeTickPct)
	}
}

func TestBuildRowsFallsBackToBybitOnly(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.EnableChangeFilter = false

	items := []normalize.InstrumentData{
		{
			Instrument: domain.Instrument{Base: "BBB", Multiplier: 1, OnBybit: true},
			BybitTicker: &domain.Ticker{
				Exchange:     domain.ExchangeBybit,
				Status:       "Trading",
				Price:        50,
				High24h:      75,
				Low24h:       50,
				ChangePct24h: 20,
				TurnoverUSD:  150,
				FundingRate:  0.002,
				TickSize:     0.05,
			},
		},
	}

	rows := BuildRows(items, settings, Options{})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if !rows[0].BybitOnly {
		t.Fatalf("BybitOnly = false, want true")
	}
	if rows[0].Price != 50 {
		t.Fatalf("Price = %v, want 50", rows[0].Price)
	}
}

func TestBuildRowsAppliesVolumeAndChangeFilters(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.MinChangePct = 20
	settings.EnableChangeFilter = true

	items := []normalize.InstrumentData{
		item("LOWVOL", 30, 99, "TRADING"),
		item("LOWCHANGE", 19, 200, "TRADING"),
		item("PASS", 21, 200, "TRADING"),
	}

	rows := BuildRows(items, settings, Options{})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].Instrument.Base != "PASS" {
		t.Fatalf("base = %s, want PASS", rows[0].Instrument.Base)
	}

	settings.EnableChangeFilter = false
	rows = BuildRows(items, settings, Options{})
	if len(rows) != 2 {
		t.Fatalf("len(rows) with disabled change filter = %d, want 2: %+v", len(rows), rows)
	}
}

func TestBuildRowsCalculatesInplay(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.EnableChangeFilter = false
	settings.Inplay.MarketThresholdPct = 3
	settings.Inplay.Multiplier = 2

	items := []normalize.InstrumentData{
		item("M1", 30, 200, "TRADING"),
		item("M2", 30, 200, "TRADING"),
		item("M3", 30, 200, "TRADING"),
		item("FAST", 65, 200, "TRADING"),
		item("DOWN", -5, 200, "TRADING"),
		item("NORMAL", 32, 200, "TRADING"),
	}

	rows := BuildRows(items, settings, Options{})
	byBase := rowsByBase(rows)
	if !byBase["FAST"].IsInplay {
		t.Fatal("FAST IsInplay = false, want true")
	}
	if !byBase["DOWN"].IsInplay {
		t.Fatal("DOWN IsInplay = false, want true")
	}
	if byBase["NORMAL"].IsInplay {
		t.Fatal("NORMAL IsInplay = true, want false")
	}
}

func TestBuildRowsDropsInvalidAndNonTrading(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.EnableChangeFilter = false

	items := []normalize.InstrumentData{
		itemWithPrices("ZEROLOW", 20, 200, "TRADING", 100, 110, 0),
		itemWithPrices("ZEROPRICE", 20, 200, "TRADING", 0, 110, 90),
		itemWithPrices("CLOSED", 20, 200, "Closed", 100, 110, 90),
		itemWithPrices("OK", 20, 200, "TRADING", 100, 110, 90),
	}

	rows := BuildRows(items, settings, Options{})
	if len(rows) != 1 || rows[0].Instrument.Base != "OK" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestBuildRowsAppliesBlacklist(t *testing.T) {
	settings := testSettings()
	settings.MinVolumeUSD = 100
	settings.EnableChangeFilter = false

	rows := BuildRows([]normalize.InstrumentData{
		item("USDC", 20, 200, "TRADING"),
		item("OK", 20, 200, "TRADING"),
	}, settings, Options{Blacklist: []string{"USDC"}})
	if len(rows) != 1 || rows[0].Instrument.Base != "OK" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func testSettings() domain.Settings {
	return domain.Settings{
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
}

func item(base string, change, turnover float64, status string) normalize.InstrumentData {
	return itemWithPrices(base, change, turnover, status, 100, 130, 90)
}

func itemWithPrices(base string, change, turnover float64, status string, price, high, low float64) normalize.InstrumentData {
	return normalize.InstrumentData{
		Instrument: domain.Instrument{Base: base, Multiplier: 1, OnBinance: true},
		BinanceTicker: &domain.Ticker{
			Exchange:     domain.ExchangeBinance,
			Status:       status,
			Price:        price,
			High24h:      high,
			Low24h:       low,
			ChangePct24h: change,
			TurnoverUSD:  turnover,
			TickSize:     0.01,
		},
	}
}

func rowsByBase(rows []domain.MetricsRow) map[string]domain.MetricsRow {
	out := make(map[string]domain.MetricsRow, len(rows))
	for _, row := range rows {
		out[row.Instrument.Base] = row
	}
	return out
}
