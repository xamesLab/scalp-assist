package corr

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"scalp-assist/internal/domain"
)

func TestComputePerfectPositiveCorrelation(t *testing.T) {
	returns := []float64{0.01, -0.02, 0.03, 0.015, -0.01, 0.02}
	asset := klinesFromReturns(time.Unix(0, 0), returns)
	btc := klinesFromReturns(time.Unix(0, 0), returns)

	got := Compute(asset, btc, len(returns))
	if got == nil {
		t.Fatal("Compute returned nil")
	}
	if math.Abs(*got-1) > 1e-12 {
		t.Fatalf("corr = %.12f, want 1", *got)
	}
}

func TestComputePerfectNegativeCorrelation(t *testing.T) {
	returns := []float64{0.01, -0.02, 0.03, 0.015, -0.01, 0.02}
	inverse := make([]float64, len(returns))
	for i := range returns {
		inverse[i] = -returns[i]
	}
	asset := klinesFromReturns(time.Unix(0, 0), returns)
	btc := klinesFromReturns(time.Unix(0, 0), inverse)

	got := Compute(asset, btc, len(returns))
	if got == nil {
		t.Fatal("Compute returned nil")
	}
	if math.Abs(*got+1) > 1e-12 {
		t.Fatalf("corr = %.12f, want -1", *got)
	}
}

func TestComputeIndependentNoiseNearZero(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a := make([]float64, 300)
	b := make([]float64, 300)
	for i := range a {
		a[i] = rng.NormFloat64() * 0.01
		b[i] = rng.NormFloat64() * 0.01
	}

	got := Compute(klinesFromReturns(time.Unix(0, 0), a), klinesFromReturns(time.Unix(0, 0), b), 100)
	if got == nil {
		t.Fatal("Compute returned nil")
	}
	if math.Abs(*got) >= 0.2 {
		t.Fatalf("|corr| = %.4f, want < 0.2", math.Abs(*got))
	}
}

func TestComputeAlignsByOpenTime(t *testing.T) {
	start := time.Unix(0, 0)
	asset := []domain.Kline{
		{OpenTime: start, Close: 100},
		{OpenTime: start.Add(time.Hour), Close: 110},
		{OpenTime: start.Add(2 * time.Hour), Close: 121},
		{OpenTime: start.Add(3 * time.Hour), Close: 133.1},
	}
	btc := []domain.Kline{
		{OpenTime: start, Close: 200},
		{OpenTime: start.Add(2 * time.Hour), Close: 242},
		{OpenTime: start.Add(3 * time.Hour), Close: 266.2},
	}

	got := Compute(asset, btc, 2)
	if got == nil {
		t.Fatal("Compute returned nil")
	}
	if math.Abs(*got-1) > 1e-12 {
		t.Fatalf("corr = %.12f, want 1", *got)
	}
}

func TestComputeReturnsNilWhenTooFewPoints(t *testing.T) {
	returns := []float64{0.01, -0.02}
	got := Compute(klinesFromReturns(time.Unix(0, 0), returns), klinesFromReturns(time.Unix(0, 0), returns), 100)
	if got != nil {
		t.Fatalf("Compute returned %.4f, want nil", *got)
	}
}

func TestAttachFetchesBTCOncePerExchangeAndSetsRows(t *testing.T) {
	start := time.Unix(0, 0)
	btc := klinesFromReturns(start, []float64{0.01, -0.02, 0.03, 0.015, -0.01, 0.02})
	asset := klinesFromReturns(start, []float64{0.01, -0.02, 0.03, 0.015, -0.01, 0.02})
	fetcher := &fakeFetcher{
		series: map[string][]domain.Kline{
			"BTCUSDT": btc,
			"ETHUSDT": asset,
			"SOLUSDT": asset,
		},
	}

	rows := []domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "ETH", Multiplier: 1, OnBinance: true, BinanceSymbol: "ETHUSDT"}},
		{Instrument: domain.Instrument{Base: "SOL", Multiplier: 1, OnBinance: true, BinanceSymbol: "SOLUSDT"}},
	}
	got := Attach(context.Background(), rows, Fetchers{Binance: fetcher}, AttachOptions{MinPoints: 6})

	if got[0].BTCCorr7d == nil || got[1].BTCCorr7d == nil {
		t.Fatalf("corr values not set: %+v", got)
	}
	if fetcher.calls["BTCUSDT"] != 1 {
		t.Fatalf("BTC calls = %d, want 1", fetcher.calls["BTCUSDT"])
	}
	if rows[0].BTCCorr7d != nil {
		t.Fatal("Attach mutated input")
	}
}

type fakeFetcher struct {
	series map[string][]domain.Kline
	calls  map[string]int
}

func (f *fakeFetcher) Klines(_ context.Context, symbol, _ string, _ int) ([]domain.Kline, map[string]string, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[symbol]++
	return f.series[symbol], nil, nil
}

func klinesFromReturns(start time.Time, returns []float64) []domain.Kline {
	klines := make([]domain.Kline, 0, len(returns)+1)
	close := 100.0
	klines = append(klines, domain.Kline{OpenTime: start, Close: close})
	for i, r := range returns {
		close *= 1 + r
		klines = append(klines, domain.Kline{
			OpenTime: start.Add(time.Duration(i+1) * time.Hour),
			Close:    close,
		})
	}
	return klines
}
