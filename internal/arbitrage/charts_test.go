package arbitrage

import (
	"context"
	"sync"
	"testing"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
)

func TestChartCatalogSnapshotAndStream(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	clients := []*fakeChartClient{
		newFakeChartClient(domain.ExchangeBinance, "BTCUSDT", now, 100),
		newFakeChartClient(domain.ExchangeBybit, "BTCUSDT", now, 101),
		newFakeChartClient(domain.ExchangeOKX, "BTC-USDT-SWAP", now, 102),
	}
	service, err := New(Options{
		Clients:  []QuoteClient{clients[0], clients[1], clients[2]},
		Settings: serviceSettings(),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	instruments := service.ChartInstruments()
	if len(instruments) != 1 {
		t.Fatalf("instruments = %+v, want one", instruments)
	}
	instrument := instruments[0]
	if instrument.ID != "BTC:USDT:1:linear_perpetual" ||
		instrument.Symbols[domain.ExchangeOKX] != "BTC-USDT-SWAP" {
		t.Fatalf("unexpected instrument: %+v", instrument)
	}

	snapshot, err := service.ChartSnapshot(context.Background(), instrument.ID, "1m", 100)
	if err != nil {
		t.Fatalf("ChartSnapshot: %v", err)
	}
	if len(snapshot.Series["binance"]) != 2 || snapshot.Series["okx"][1].Value != 102 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if clients[1].historyInterval() != "1" {
		t.Fatalf("Bybit history interval = %q, want 1", clients[1].historyInterval())
	}

	events, unsubscribe, err := service.SubscribeChart(instrument.ID, "1m")
	if err != nil {
		t.Fatalf("SubscribeChart: %v", err)
	}
	defer unsubscribe()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == "bar" && event.Exchange == "okx" {
				if event.Point == nil || event.Point.Value != 102.5 {
					t.Fatalf("unexpected event: %+v", event)
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for chart event")
		}
	}
}

func TestChartCatalogRequiresAllThreeExchanges(t *testing.T) {
	now := time.Now().UTC()
	service, err := New(Options{
		Clients: []QuoteClient{
			newFakeChartClient(domain.ExchangeBinance, "BTCUSDT", now, 100),
			newFakeChartClient(domain.ExchangeBybit, "BTCUSDT", now, 101),
			fakeQuoteClient{exchange: domain.ExchangeOKX, quotes: []domain.VenueQuote{
				quote(domain.ExchangeOKX, "ETH-USDT-SWAP", "ETH", 99, 100, 1, 1, now, nil),
			}},
		},
		Settings: serviceSettings(),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := service.ChartInstruments(); len(got) != 0 {
		t.Fatalf("instruments = %+v, want none", got)
	}
}

func TestBroadcastStoredGrossUsesBestSavedRoute(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 3, 0, 0, time.UTC)
	service, err := New(Options{
		Clients: []QuoteClient{
			newFakeChartClient(domain.ExchangeBinance, "BTCUSDT", now, 100),
			newFakeChartClient(domain.ExchangeBybit, "BTCUSDT", now, 101),
			newFakeChartClient(domain.ExchangeOKX, "BTC-USDT-SWAP", now, 102),
		},
		Settings: serviceSettings(),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	events, unsubscribe, err := service.SubscribeChart("BTC:USDT:1:linear_perpetual", "5m")
	if err != nil {
		t.Fatalf("SubscribeChart: %v", err)
	}
	defer unsubscribe()

	instrument := domain.InstrumentKey{Base: "BTC", Quote: "USDT", Multiplier: 1, Kind: "linear_perpetual"}
	service.broadcastStoredGross(model.Run{
		ObservedAt: now,
		Opportunities: []model.Opportunity{
			{Instrument: instrument, BuyAsk: 100, SellBid: 100.5},
			{Instrument: instrument, BuyAsk: 100, SellBid: 101},
		},
	})

	timeout := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "gross" {
				continue
			}
			if event.Point == nil || event.Point.Time != time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC).Unix() ||
				event.Point.Value < 99.999 || event.Point.Value > 100.001 {
				t.Fatalf("unexpected gross event: %+v", event)
			}
			return
		case <-timeout:
			t.Fatal("timed out waiting for gross event")
		}
	}
}

func TestChartPointsUseSharedBucketAndSnapshotRange(t *testing.T) {
	point := chartPoint(domain.Kline{
		OpenTime: time.Date(2026, 7, 27, 12, 3, 47, 0, time.UTC),
		Close:    100,
	}, "5m")
	wantTime := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC).Unix()
	if point.Time != wantTime {
		t.Fatalf("point time = %d, want %d", point.Time, wantTime)
	}

	series := map[string][]model.ChartPoint{
		"binance": {{Time: 60}, {Time: 120}, {Time: 180}},
		"bybit":   {{Time: 60}, {Time: 120}},
		"okx":     {{Time: 120}, {Time: 180}},
	}
	alignChartSnapshot(series)
	for exchange, points := range series {
		if len(points) != 1 || points[0].Time != 120 {
			t.Fatalf("%s points = %+v, want shared timestamp 120", exchange, points)
		}
	}
}

type fakeChartClient struct {
	exchange domain.Exchange
	symbol   string
	quotes   []domain.VenueQuote
	klines   []domain.Kline
	stream   domain.Kline
	mu       sync.Mutex
	history  string
}

func newFakeChartClient(exchange domain.Exchange, symbol string, now time.Time, price float64) *fakeChartClient {
	return &fakeChartClient{
		exchange: exchange,
		symbol:   symbol,
		quotes: []domain.VenueQuote{
			quote(exchange, symbol, "BTC", price-1, price, 10, 10, now, nil),
		},
		klines: []domain.Kline{
			{OpenTime: now.Add(-time.Minute), Close: price - 0.5, Closed: true},
			{OpenTime: now, Close: price, Closed: false},
		},
		stream: domain.Kline{OpenTime: now, Close: price + 0.5, Closed: false},
	}
}

func (f *fakeChartClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f *fakeChartClient) Quotes(context.Context) ([]domain.VenueQuote, error) {
	return f.quotes, nil
}

func (f *fakeChartClient) Klines(_ context.Context, _ string, interval string, _ int) ([]domain.Kline, map[string]string, error) {
	f.mu.Lock()
	f.history = interval
	f.mu.Unlock()
	return append([]domain.Kline(nil), f.klines...), nil, nil
}

func (f *fakeChartClient) StreamKlines(ctx context.Context, _ string, _ string, out chan<- domain.Kline) error {
	select {
	case out <- f.stream:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeChartClient) historyInterval() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.history
}

var _ chartClient = (*fakeChartClient)(nil)
