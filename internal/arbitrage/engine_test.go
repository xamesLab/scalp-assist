package arbitrage

import (
	"testing"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
)

func TestScanCalculatesDirectionalNetSpreadAndCapacity(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	buyFunding := 0.0001
	sellFunding := 0.0002
	quotes := []domain.VenueQuote{
		quote(domain.ExchangeBinance, "BTCUSDT", "BTC", 99, 100, 4, 3, now.Add(-time.Second), &buyFunding),
		quote(domain.ExchangeBybit, "BTCUSDT", "BTC", 102, 103, 2, 2, now.Add(-2*time.Second), &sellFunding),
	}
	settings := testSettings()
	resolver, _ := normalize.NewResolver(nil, nil)

	result := Scan(quotes, settings, resolver, now)
	if result.Instruments != 1 || len(result.Opportunities) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	got := result.Opportunities[0]
	if got.BuyExchange != domain.ExchangeBinance || got.SellExchange != domain.ExchangeBybit {
		t.Fatalf("wrong direction: %+v", got)
	}
	if got.GrossSpreadBps != 200 || got.NetSpreadBps != 190 {
		t.Fatalf("spreads = gross %v net %v", got.GrossSpreadBps, got.NetSpreadBps)
	}
	if got.CapacityUSD != 204 {
		t.Fatalf("capacity = %v, want 204", got.CapacityUSD)
	}
	if got.FundingEdgeBpsHour == nil || *got.FundingEdgeBpsHour <= 0 {
		t.Fatalf("funding edge missing: %+v", got)
	}
}

func TestScanRejectsStaleSkewedAndDifferentMultiplier(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	settings := testSettings()
	resolver, _ := normalize.NewResolver(nil, nil)

	tests := []struct {
		name   string
		quotes []domain.VenueQuote
	}{
		{
			name: "stale",
			quotes: []domain.VenueQuote{
				quote(domain.ExchangeBinance, "BTCUSDT", "BTC", 99, 100, 1, 1, now.Add(-time.Minute), nil),
				quote(domain.ExchangeBybit, "BTCUSDT", "BTC", 102, 103, 1, 1, now, nil),
			},
		},
		{
			name: "skewed",
			quotes: []domain.VenueQuote{
				quote(domain.ExchangeBinance, "BTCUSDT", "BTC", 99, 100, 1, 1, now.Add(-time.Second), nil),
				quote(domain.ExchangeBybit, "BTCUSDT", "BTC", 102, 103, 1, 1, now.Add(-20*time.Second), nil),
			},
		},
		{
			name: "multiplier",
			quotes: []domain.VenueQuote{
				quote(domain.ExchangeBinance, "1000PEPEUSDT", "1000PEPE", 99, 100, 1, 1, now, nil),
				quote(domain.ExchangeBybit, "PEPEUSDT", "PEPE", 102, 103, 1, 1, now, nil),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Scan(test.quotes, settings, resolver, now)
			if len(result.Opportunities) != 0 {
				t.Fatalf("opportunities = %+v, want none", result.Opportunities)
			}
		})
	}
}

func quote(exchange domain.Exchange, symbol, base string, bid, ask, bidQty, askQty float64, at time.Time, funding *float64) domain.VenueQuote {
	return domain.VenueQuote{
		Exchange:           exchange,
		SourceSymbol:       symbol,
		Contract:           domain.ContractSpec{Exchange: exchange, Symbol: symbol, BaseAsset: base, QuoteAsset: "USDT", Status: "TRADING", ContractType: "PERPETUAL"},
		BidPrice:           bid,
		AskPrice:           ask,
		BidQuantity:        bidQty,
		AskQuantity:        askQty,
		QuantityMultiplier: 1,
		FundingRate:        funding,
		FundingInterval:    8 * time.Hour,
		UpdatedAt:          at,
	}
}

func testSettings() model.Settings {
	return model.Settings{
		MinGrossSpreadBps: 1,
		MaxGrossSpreadBps: 1_000,
		MinNetSpreadBps:   0,
		MinCapacityUSD:    0,
		MaxQuoteAge:       30 * time.Second,
		MaxQuoteSkew:      10 * time.Second,
		TopN:              20,
		FeesBps:           map[string]float64{"binance": 5, "bybit": 5, "okx": 5},
	}
}
