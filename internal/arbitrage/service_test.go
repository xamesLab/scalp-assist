package arbitrage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/store"
)

func TestServiceRefreshPersistsAndDegrades(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	service, err := New(Options{
		Store: db,
		Clients: []QuoteClient{
			fakeQuoteClient{exchange: domain.ExchangeBinance, quotes: []domain.VenueQuote{
				quote(domain.ExchangeBinance, "BTCUSDT", "BTC", 99, 100, 3, 3, now, nil),
			}},
			fakeQuoteClient{exchange: domain.ExchangeBybit, quotes: []domain.VenueQuote{
				quote(domain.ExchangeBybit, "BTCUSDT", "BTC", 102, 103, 3, 3, now, nil),
			}},
			fakeQuoteClient{exchange: domain.ExchangeOKX, err: errors.New("blocked")},
		},
		Settings: serviceSettings(),
		Interval: 10 * time.Second,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	latest := service.Latest()
	if len(latest.Opportunities) != 1 || latest.ExchangeState["okx"].OK {
		t.Fatalf("unexpected latest: %+v", latest)
	}
	persisted, err := db.LatestArbitrageRun(ctx)
	if err != nil || len(persisted.Opportunities) != 1 {
		t.Fatalf("persisted run: %+v err=%v", persisted, err)
	}
}

func TestServiceRequiresTwoExchanges(t *testing.T) {
	now := time.Now().UTC()
	service, err := New(Options{
		Clients: []QuoteClient{
			fakeQuoteClient{exchange: domain.ExchangeBinance, quotes: []domain.VenueQuote{
				quote(domain.ExchangeBinance, "BTCUSDT", "BTC", 99, 100, 1, 1, now, nil),
			}},
			fakeQuoteClient{exchange: domain.ExchangeBybit, err: errors.New("down")},
		},
		Settings: serviceSettings(),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh returned nil error")
	}
}

type fakeQuoteClient struct {
	exchange domain.Exchange
	quotes   []domain.VenueQuote
	err      error
}

func (f fakeQuoteClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f fakeQuoteClient) Quotes(context.Context) ([]domain.VenueQuote, error) {
	return f.quotes, f.err
}

func serviceSettings() model.Settings {
	settings := testSettings()
	settings.HistoryInterval = time.Minute
	settings.RetentionDays = 30
	return settings
}

func TestFeeRatesExposeConfiguredMakerAndTakerFees(t *testing.T) {
	settings := serviceSettings()
	service, err := New(Options{Settings: settings})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rates := service.FeeRates()
	if len(rates) != 3 || rates[0].Exchange != "binance" ||
		rates[0].MakerBps != 2 || rates[0].TakerBps != 5 {
		t.Fatalf("unexpected fee rates: %+v", rates)
	}
}
