package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/store"
)

func TestRefreshSavesSnapshotWithPropAndCorrCache(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	defer db.Close()
	corrValue := 0.9
	if err := db.UpsertCorr(ctx, domain.Instrument{Base: "BTC", Multiplier: 1}, &corrValue, time.Now()); err != nil {
		t.Fatalf("UpsertCorr: %v", err)
	}
	if err := db.ReplacePropList(ctx, []store.PropInstrument{{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Note: "main"}}); err != nil {
		t.Fatalf("ReplacePropList: %v", err)
	}
	client := fakeClient{snapshot: snapshot(domain.ExchangeBinance, "BTCUSDT", "BTC", 1_000_000_000, 25)}
	sched := New(Options{
		Store:    db,
		Defaults: settings(),
		Binance:  client,
		Now:      fixedNow,
	})

	if err := sched.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rows, err := db.LatestSnapshotRows(ctx)
	if err != nil {
		t.Fatalf("LatestSnapshotRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if !rows[0].InPropList {
		t.Fatal("InPropList = false, want true")
	}
	if rows[0].BTCCorr7d == nil || *rows[0].BTCCorr7d != 0.9 {
		t.Fatalf("corr mismatch: %+v", rows[0].BTCCorr7d)
	}
}

func TestRefreshDegradesWhenOneExchangeFails(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	defer db.Close()
	sched := New(Options{
		Store:    db,
		Defaults: settings(),
		Binance:  fakeClient{err: errors.New("blocked")},
		Bybit:    fakeClient{snapshot: snapshot(domain.ExchangeBybit, "ETHUSDT", "ETH", 1_000_000_000, 25)},
		Now:      fixedNow,
	})
	if err := sched.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	status := sched.Status()
	if status.BinanceOK || !status.BybitOK {
		t.Fatalf("status mismatch: %+v", status)
	}
	rows, _ := db.LatestSnapshotRows(ctx)
	if len(rows) != 1 || rows[0].Instrument.Base != "ETH" {
		t.Fatalf("rows mismatch: %+v", rows)
	}
}

func TestRefreshMutexRejectsConcurrentRun(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	defer db.Close()
	client := &blockingClient{started: make(chan struct{}), release: make(chan struct{}), snapshotValue: snapshot(domain.ExchangeBinance, "BTCUSDT", "BTC", 1_000_000_000, 25)}
	sched := New(Options{Store: db, Defaults: settings(), Binance: client, Now: fixedNow})

	started := make(chan struct{})
	done := make(chan error)
	go func() {
		close(started)
		done <- sched.Refresh(ctx)
	}()
	<-started
	client.waitStarted(t)
	err := sched.Refresh(ctx)
	if !errors.Is(err, ErrRefreshInProgress) {
		t.Fatalf("second refresh err = %v, want ErrRefreshInProgress", err)
	}
	close(client.release)
	if err := <-done; err != nil {
		t.Fatalf("first refresh: %v", err)
	}
}

func TestRecomputeCorrUpdatesCache(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	defer db.Close()
	row := domain.MetricsRow{
		Instrument: domain.Instrument{Base: "ETH", Multiplier: 1, BinanceSymbol: "ETHUSDT", OnBinance: true},
		Rank:       1, Price: 1, TurnoverUSD: 1, DayRangePct: 1,
	}
	if _, err := db.SaveSnapshot(ctx, time.Now(), []domain.MetricsRow{row}); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	returns := make([]float64, 120)
	for i := range returns {
		if i%2 == 0 {
			returns[i] = 0.01
		} else {
			returns[i] = -0.005
		}
	}
	series := klines(returns)
	client := fakeClient{klines: map[string][]domain.Kline{"BTCUSDT": series, "ETHUSDT": series}}
	sched := New(Options{Store: db, Defaults: settings(), Binance: client, Now: fixedNow})
	if err := sched.RecomputeCorr(ctx); err != nil {
		t.Fatalf("RecomputeCorr: %v", err)
	}
	cache, err := db.CorrCache(ctx)
	if err != nil {
		t.Fatalf("CorrCache: %v", err)
	}
	if cache["ETH:1"] == nil || *cache["ETH:1"] < 0.99 {
		t.Fatalf("corr cache mismatch: %+v", cache)
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	return db
}

func settings() domain.Settings {
	return domain.Settings{
		MinVolumeUSD:       100,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay:             domain.InplaySettings{MarketThresholdPct: 3, Multiplier: 2},
		Weights:            domain.ScoreWeights{Volatility: 0.4, Turnover: 0.3, Inplay: 0.2, Corr: 0.1},
		TopN:               20,
	}
}

func snapshot(exchange domain.Exchange, symbol, base string, turnover, change float64) domain.MarketSnapshot {
	return domain.MarketSnapshot{
		Exchange: exchange,
		Contracts: []domain.ContractSpec{{
			Exchange: exchange, Symbol: symbol, BaseAsset: base, QuoteAsset: "USDT", Status: "TRADING", TickSize: 0.01,
		}},
		Tickers: []domain.Ticker{{
			Symbol: symbol, SourceSymbol: symbol, Exchange: exchange, QuoteAsset: "USDT", Price: 100, High24h: 130, Low24h: 90,
			ChangePct24h: change, TurnoverUSD: turnover, Status: "TRADING", TickSize: 0.01,
		}},
	}
}

type fakeClient struct {
	snapshot domain.MarketSnapshot
	klines   map[string][]domain.Kline
	err      error
}

func (f fakeClient) Snapshot(context.Context) (domain.MarketSnapshot, error) {
	if f.err != nil {
		return domain.MarketSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func (f fakeClient) Klines(_ context.Context, symbol, _ string, _ int) ([]domain.Kline, map[string]string, error) {
	return f.klines[symbol], nil, nil
}

type blockingClient struct {
	release       chan struct{}
	once          sync.Once
	started       chan struct{}
	snapshotValue domain.MarketSnapshot
}

func (b *blockingClient) Snapshot(context.Context) (domain.MarketSnapshot, error) {
	b.once.Do(func() {
		close(b.started)
	})
	<-b.release
	return b.snapshotValue, nil
}

func (b *blockingClient) Klines(context.Context, string, string, int) ([]domain.Kline, map[string]string, error) {
	return nil, nil, nil
}

func (b *blockingClient) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("blocking client did not start")
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
}

func klines(returns []float64) []domain.Kline {
	start := time.Unix(0, 0)
	close := 100.0
	out := []domain.Kline{{OpenTime: start, Close: close}}
	for i, r := range returns {
		close *= 1 + r
		out = append(out, domain.Kline{OpenTime: start.Add(time.Duration(i+1) * time.Hour), Close: close})
	}
	return out
}
