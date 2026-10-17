package charts

import (
	"context"
	"sync"
	"testing"
	"time"

	"scalp-assist/internal/domain"
)

func TestCatalogUsesBinanceFirstAndBybitFallback(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	binance := &fakeClient{
		exchange: domain.ExchangeBinance,
		quotes: []domain.VenueQuote{
			fakeQuote(domain.ExchangeBinance, "BTCUSDT", "BTC", "USDT"),
		},
		klines: []domain.Kline{{
			OpenTime: now,
			Open:     100,
			High:     110,
			Low:      90,
			Close:    105,
		}},
	}
	bybit := &fakeClient{
		exchange: domain.ExchangeBybit,
		quotes: []domain.VenueQuote{
			fakeQuote(domain.ExchangeBybit, "BTCUSDT", "BTC", "USDT"),
			fakeQuote(domain.ExchangeBybit, "SOLUSDT", "SOL", "USDT"),
		},
		klines: []domain.Kline{{
			OpenTime: now,
			Open:     20,
			High:     22,
			Low:      19,
			Close:    21,
		}},
	}
	service, err := New(Options{Clients: []Client{binance, bybit}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	instruments := service.Instruments()
	if len(instruments) != 2 {
		t.Fatalf("len(Instruments) = %d, want 2: %+v", len(instruments), instruments)
	}
	if instruments[0].ID != "BTC:1" || instruments[0].Exchange != domain.ExchangeBinance {
		t.Fatalf("BTC source = %+v, want Binance", instruments[0])
	}
	if instruments[1].ID != "SOL:1" || instruments[1].Exchange != domain.ExchangeBybit {
		t.Fatalf("SOL source = %+v, want Bybit", instruments[1])
	}

	snapshot, err := service.Snapshot(context.Background(), "SOL:1", "1h", 300)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if bybit.lastHistoryInterval != "60" {
		t.Fatalf("Bybit history interval = %q, want 60", bybit.lastHistoryInterval)
	}
	if len(snapshot.Candles) != 1 {
		t.Fatalf("len(Candles) = %d, want 1", len(snapshot.Candles))
	}
	candle := snapshot.Candles[0]
	if candle.Open != 20 || candle.High != 22 || candle.Low != 19 || candle.Close != 21 {
		t.Fatalf("unexpected OHLC candle: %+v", candle)
	}
}

func TestSubscribersShareOneUpstreamStream(t *testing.T) {
	gate := make(chan struct{})
	client := &fakeClient{
		exchange: domain.ExchangeBinance,
		quotes:   []domain.VenueQuote{fakeQuote(domain.ExchangeBinance, "BTCUSDT", "BTC", "USDT")},
		stream: domain.Kline{
			OpenTime: time.Unix(1_700_000_000, 0),
			Open:     100,
			High:     102,
			Low:      99,
			Close:    101,
		},
		streamGate: gate,
	}
	service, err := New(Options{Clients: []Client{client}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	first, unsubscribeFirst, err := service.Subscribe("BTC:1", "5m")
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}
	defer unsubscribeFirst()
	second, unsubscribeSecond, err := service.Subscribe("BTC:1", "5m")
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	defer unsubscribeSecond()
	close(gate)

	waitForBar(t, first)
	waitForBar(t, second)
	client.mu.Lock()
	streamCalls := client.streamCalls
	client.mu.Unlock()
	if streamCalls != 1 {
		t.Fatalf("StreamKlines calls = %d, want 1", streamCalls)
	}
}

func waitForBar(t *testing.T, events <-chan Event) {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == "bar" {
				if event.Candle == nil || event.Candle.Close != 101 {
					t.Fatalf("unexpected bar event: %+v", event)
				}
				return
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for bar event")
		}
	}
}

func fakeQuote(exchange domain.Exchange, symbol, base, quote string) domain.VenueQuote {
	return domain.VenueQuote{
		Exchange:     exchange,
		SourceSymbol: symbol,
		Contract: domain.ContractSpec{
			Exchange:     exchange,
			Symbol:       symbol,
			BaseAsset:    base,
			QuoteAsset:   quote,
			Status:       "TRADING",
			ContractType: "PERPETUAL",
			TickSize:     0.01,
		},
	}
}

type fakeClient struct {
	exchange            domain.Exchange
	quotes              []domain.VenueQuote
	klines              []domain.Kline
	stream              domain.Kline
	streamGate          <-chan struct{}
	mu                  sync.Mutex
	streamCalls         int
	lastHistoryInterval string
}

func (f *fakeClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f *fakeClient) Quotes(context.Context) ([]domain.VenueQuote, error) {
	return append([]domain.VenueQuote(nil), f.quotes...), nil
}

func (f *fakeClient) Klines(_ context.Context, _ string, interval string, _ int) ([]domain.Kline, map[string]string, error) {
	f.mu.Lock()
	f.lastHistoryInterval = interval
	f.mu.Unlock()
	return append([]domain.Kline(nil), f.klines...), nil, nil
}

func (f *fakeClient) StreamKlines(ctx context.Context, _ string, _ string, out chan<- domain.Kline) error {
	f.mu.Lock()
	f.streamCalls++
	f.mu.Unlock()
	if f.streamGate != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.streamGate:
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case out <- f.stream:
	}
	<-ctx.Done()
	return ctx.Err()
}

var _ Client = (*fakeClient)(nil)
