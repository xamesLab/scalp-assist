package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
)

func TestArbitrageRunRoundTripAndStats(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "arbitrage.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	funding := 0.0001
	run := model.Run{
		ObservedAt: now,
		ExchangeState: map[string]model.ExchangeState{
			"binance": {OK: true, Quotes: 1, UpdatedAt: now},
			"bybit":   {OK: true, Quotes: 1, UpdatedAt: now},
		},
		Instruments: 1,
		Opportunities: []model.Opportunity{{
			Instrument:       domain.InstrumentKey{Base: "BTC", Quote: "USDT", Multiplier: 1, Kind: "linear_perpetual"},
			BuyExchange:      domain.ExchangeBinance,
			SellExchange:     domain.ExchangeBybit,
			BuySymbol:        "BTCUSDT",
			SellSymbol:       "BTCUSDT",
			BuyAsk:           100,
			SellBid:          101,
			GrossSpreadBps:   100,
			NetSpreadBps:     90,
			NetMakerTakerBps: 93,
			CapacityUSD:      1000,
			BuyFundingRate:   &funding,
			BuyQuoteAt:       now,
			SellQuoteAt:      now,
			FirstSeenAt:      now,
			ObservedAt:       now,
		}},
	}
	id, err := db.SaveArbitrageRun(ctx, run)
	if err != nil {
		t.Fatalf("SaveArbitrageRun: %v", err)
	}
	if id == 0 {
		t.Fatal("run id must be set")
	}
	got, err := db.LatestArbitrageRun(ctx)
	if err != nil {
		t.Fatalf("LatestArbitrageRun: %v", err)
	}
	if len(got.Opportunities) != 1 || got.Opportunities[0].NetSpreadBps != 90 ||
		got.Opportunities[0].NetMakerTakerBps != 93 {
		t.Fatalf("unexpected run: %+v", got)
	}
	stats, err := db.ArbitrageStats(ctx, 7)
	if err != nil {
		t.Fatalf("ArbitrageStats: %v", err)
	}
	if stats.Runs != 1 || stats.Observations != 1 || stats.ProfitableCount != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	gross, err := db.ArbitrageGrossHistory(ctx, run.Opportunities[0].Instrument, now.Add(-time.Minute), time.Minute, 100)
	if err != nil {
		t.Fatalf("ArbitrageGrossHistory: %v", err)
	}
	if len(gross) != 1 || math.Abs(gross[0].Value-100) > 0.000001 {
		t.Fatalf("unexpected gross history: %+v", gross)
	}
}
