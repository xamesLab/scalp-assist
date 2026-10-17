package propscreen

import (
	"context"
	"errors"
	"testing"
	"time"

	"scalp-assist/internal/corr"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/store"
)

func TestBuildRowsUsesBinanceThenFallsBackToBybit(t *testing.T) {
	resolver, err := normalize.NewResolver(nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	props := []store.PropInstrument{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}},
		{Instrument: domain.Instrument{Base: "SOL", Multiplier: 1}},
		{Instrument: domain.Instrument{Base: "PEPE", Multiplier: 1}},
	}
	rows := BuildRows(props, []domain.MarketSnapshot{
		snapshot(domain.ExchangeBinance, now,
			market("BTCUSDT", "BTC", "USDT", 1, 100, now),
			market("1000PEPEUSDT", "1000PEPE", "USDT", 3, 300, now),
		),
		snapshot(domain.ExchangeBybit, now,
			market("BTCUSDT", "BTC", "USDT", 2, 200, now),
			market("SOLUSDT", "SOL", "USDT", -4, 400, now),
			market("1000PEPEUSDT", "1000PEPE", "USDT", 5, 500, now),
		),
	}, resolver)

	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	byBase := map[string]Row{}
	for _, row := range rows {
		byBase[row.Instrument.Base] = row
	}
	if got := byBase["BTC"]; got.SourceExchange != domain.ExchangeBinance || got.TurnoverUSD != 100 {
		t.Fatalf("BTC row = %+v", got)
	}
	if got := byBase["SOL"]; got.SourceExchange != domain.ExchangeBybit || got.ChangePct24h != -4 {
		t.Fatalf("SOL row = %+v", got)
	}
	if got := byBase["PEPE"]; got.SourceSymbol != "1000PEPEUSDT" || got.ContractMultiplier != 1000 {
		t.Fatalf("PEPE row = %+v", got)
	}
}

func TestBuildRowsPrefersUSDTAndRejectsAmbiguousBaseFallback(t *testing.T) {
	resolver, err := normalize.NewResolver(nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	now := time.Now().UTC()
	props := []store.PropInstrument{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}},
		{Instrument: domain.Instrument{Base: "PEPE", Multiplier: 1}},
		{Instrument: domain.Instrument{Base: "PEPE", Multiplier: 1000}},
		{Instrument: domain.Instrument{Base: "CAT", Multiplier: 1}},
	}
	rows := BuildRows(props, []domain.MarketSnapshot{
		snapshot(domain.ExchangeBinance, now,
			market("BTCUSDC", "BTC", "USDC", 1, 50, now),
			market("BTCUSDT", "BTC", "USDT", 2, 100, now),
			market("1000PEPEUSDT", "1000PEPE", "USDT", 3, 300, now),
			market("100CATUSDT", "100CAT", "USDT", 4, 400, now),
			market("1000CATUSDT", "1000CAT", "USDT", 5, 500, now),
		),
	}, resolver)

	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Instrument.Base != "BTC" || rows[0].SourceSymbol != "BTCUSDT" {
		t.Fatalf("BTC row = %+v", rows[0])
	}
	if rows[1].Instrument.Base != "PEPE" || rows[1].Instrument.Multiplier != 1000 {
		t.Fatalf("PEPE row = %+v", rows[1])
	}
}

func TestServiceDegradesAndKeepsExchangeStates(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service, err := New(Options{
		Store: fakePropStore{props: []store.PropInstrument{{
			Instrument: domain.Instrument{Base: "SOL", Multiplier: 1},
		}}},
		Clients: []MarketClient{
			fakeMarketClient{exchange: domain.ExchangeBinance, err: errors.New("blocked")},
			fakeMarketClient{exchange: domain.ExchangeBybit, snapshot: snapshot(
				domain.ExchangeBybit, now, market("SOLUSDT", "SOL", "USDT", 7, 700, now),
			)},
		},
		Interval: time.Minute,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	run := service.Latest()
	if len(run.Rows) != 1 || run.Rows[0].SourceExchange != domain.ExchangeBybit {
		t.Fatalf("run = %+v", run)
	}
	if run.ExchangeState["binance"].OK || !run.ExchangeState["bybit"].OK {
		t.Fatalf("states = %+v", run.ExchangeState)
	}
}

func TestServiceUsesCacheAndRecomputesCorrelation(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	cached := 0.42
	propStore := &recordingPropStore{
		props: []store.PropInstrument{{
			Instrument: domain.Instrument{Base: "ETH", Multiplier: 1},
		}},
		cache: map[string]*float64{"ETH:1": &cached},
		saved: map[string]*float64{},
	}
	service, err := New(Options{
		Store: propStore,
		Clients: []MarketClient{fakeMarketClient{
			exchange: domain.ExchangeBinance,
			snapshot: snapshot(
				domain.ExchangeBinance, now, market("ETHUSDT", "ETH", "USDT", 3, 300, now),
			),
		}},
		Interval:     time.Minute,
		CorrInterval: 2 * time.Hour,
		CorrFetchers: corr.Fetchers{Binance: fakeCorrFetcher{}},
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := service.Latest().Rows[0].BTCCorr7d; got == nil || *got != cached {
		t.Fatalf("cached correlation = %v", got)
	}
	if err := service.RecomputeCorr(context.Background()); err != nil {
		t.Fatalf("RecomputeCorr: %v", err)
	}
	got := service.Latest().Rows[0].BTCCorr7d
	if got == nil || *got < 0.999 {
		t.Fatalf("recomputed correlation = %v", got)
	}
	saved := propStore.saved["ETH:1"]
	if saved == nil || *saved < 0.999 {
		t.Fatalf("saved correlation = %v", saved)
	}
}

func TestServiceRefreshMutex(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	service, err := New(Options{
		Store: fakePropStore{},
		Clients: []MarketClient{
			blockingMarketClient{started: started, release: release},
		},
		Interval: time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Refresh(context.Background()) }()
	<-started
	if err := service.Refresh(context.Background()); !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("second Refresh error = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
}

type fakePropStore struct {
	props []store.PropInstrument
	err   error
	cache map[string]*float64
}

func (f fakePropStore) PropList(context.Context) ([]store.PropInstrument, error) {
	return append([]store.PropInstrument(nil), f.props...), f.err
}

func (f fakePropStore) CorrCache(context.Context) (map[string]*float64, error) {
	return f.cache, nil
}

func (f fakePropStore) UpsertCorr(context.Context, domain.Instrument, *float64, time.Time) error {
	return nil
}

type recordingPropStore struct {
	props []store.PropInstrument
	cache map[string]*float64
	saved map[string]*float64
}

func (f *recordingPropStore) PropList(context.Context) ([]store.PropInstrument, error) {
	return append([]store.PropInstrument(nil), f.props...), nil
}

func (f *recordingPropStore) CorrCache(context.Context) (map[string]*float64, error) {
	return f.cache, nil
}

func (f *recordingPropStore) UpsertCorr(_ context.Context, instrument domain.Instrument, value *float64, _ time.Time) error {
	f.saved[instrumentKey(instrument)] = cloneFloat(value)
	return nil
}

type fakeMarketClient struct {
	exchange domain.Exchange
	snapshot domain.MarketSnapshot
	err      error
}

func (f fakeMarketClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f fakeMarketClient) MarketSnapshot(context.Context) (domain.MarketSnapshot, error) {
	return f.snapshot, f.err
}

type blockingMarketClient struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (f blockingMarketClient) Exchange() domain.Exchange {
	return domain.ExchangeBinance
}

func (f blockingMarketClient) MarketSnapshot(context.Context) (domain.MarketSnapshot, error) {
	close(f.started)
	<-f.release
	return domain.MarketSnapshot{Exchange: domain.ExchangeBinance}, nil
}

type fakeCorrFetcher struct{}

func (fakeCorrFetcher) Klines(_ context.Context, _ string, _ string, limit int) ([]domain.Kline, map[string]string, error) {
	if limit <= 0 {
		limit = 168
	}
	out := make([]domain.Kline, 0, limit)
	closePrice := 100.0
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := 0; i < limit; i++ {
		if i > 0 {
			closePrice *= 1 + float64((i%5)+1)/1000
		}
		out = append(out, domain.Kline{OpenTime: start.Add(time.Duration(i) * time.Hour), Close: closePrice})
	}
	return out, nil, nil
}

type marketItem struct {
	contract domain.ContractSpec
	ticker   domain.Ticker
}

func market(symbol, base, quote string, change, turnover float64, updatedAt time.Time) marketItem {
	return marketItem{
		contract: domain.ContractSpec{
			Symbol: symbol, BaseAsset: base, QuoteAsset: quote,
			Status: "TRADING", ContractType: "PERPETUAL",
		},
		ticker: domain.Ticker{
			Symbol: symbol, ChangePct24h: change, TurnoverUSD: turnover, UpdatedAt: updatedAt,
		},
	}
}

func snapshot(exchange domain.Exchange, fetchedAt time.Time, items ...marketItem) domain.MarketSnapshot {
	out := domain.MarketSnapshot{Exchange: exchange, FetchedAt: fetchedAt}
	for _, item := range items {
		item.contract.Exchange = exchange
		item.ticker.Exchange = exchange
		out.Contracts = append(out.Contracts, item.contract)
		out.Tickers = append(out.Tickers, item.ticker)
	}
	return out
}
