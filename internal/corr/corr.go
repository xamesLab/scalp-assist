package corr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"scalp-assist/internal/domain"
)

const (
	DefaultInterval  = "1h"
	DefaultLimit     = 168
	DefaultMinPoints = 100
)

type KlineFetcher interface {
	Klines(ctx context.Context, symbol, interval string, limit int) ([]domain.Kline, map[string]string, error)
}

type Fetchers struct {
	Binance KlineFetcher
	Bybit   KlineFetcher
}

type AttachOptions struct {
	Interval  string
	Limit     int
	MinPoints int
	Logger    *slog.Logger
}

func Attach(ctx context.Context, rows []domain.MetricsRow, fetchers Fetchers, opts AttachOptions) []domain.MetricsRow {
	out := append([]domain.MetricsRow(nil), rows...)
	interval := opts.Interval
	if interval == "" {
		interval = DefaultInterval
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	minPoints := opts.MinPoints
	if minPoints <= 0 {
		minPoints = DefaultMinPoints
	}

	btcCache := map[domain.Exchange][]domain.Kline{}
	for i := range out {
		exchange, symbol, fetcher, ok := source(out[i], fetchers)
		if !ok {
			continue
		}
		btc, ok := btcCache[exchange]
		if !ok {
			var err error
			btc, _, err = fetcher.Klines(ctx, "BTCUSDT", interval, limit)
			if err != nil {
				logWarn(opts.Logger, "btc klines failed", "exchange", exchange, "error", err)
				continue
			}
			btcCache[exchange] = btc
		}

		asset, _, err := fetcher.Klines(ctx, symbol, interval, limit)
		if err != nil {
			logWarn(opts.Logger, "asset klines failed", "exchange", exchange, "symbol", symbol, "error", err)
			continue
		}
		value := Compute(asset, btc, minPoints)
		out[i].BTCCorr7d = value
	}
	return out
}

func Compute(asset, btc []domain.Kline, minPoints int) *float64 {
	assetReturns, btcReturns := alignedReturns(asset, btc)
	if len(assetReturns) < minPoints {
		return nil
	}
	value, ok := pearson(assetReturns, btcReturns)
	if !ok {
		return nil
	}
	return &value
}

func alignedReturns(asset, btc []domain.Kline) ([]float64, []float64) {
	btcByTime := make(map[int64]float64, len(btc))
	for _, kline := range btc {
		if kline.Close > 0 {
			btcByTime[kline.OpenTime.UnixMilli()] = kline.Close
		}
	}

	var assetCloses []float64
	var btcCloses []float64
	for _, kline := range asset {
		if kline.Close <= 0 {
			continue
		}
		btcClose, ok := btcByTime[kline.OpenTime.UnixMilli()]
		if !ok {
			continue
		}
		assetCloses = append(assetCloses, kline.Close)
		btcCloses = append(btcCloses, btcClose)
	}
	if len(assetCloses) < 2 {
		return nil, nil
	}

	assetReturns := make([]float64, 0, len(assetCloses)-1)
	btcReturns := make([]float64, 0, len(btcCloses)-1)
	for i := 1; i < len(assetCloses); i++ {
		if assetCloses[i-1] <= 0 || btcCloses[i-1] <= 0 {
			continue
		}
		assetReturns = append(assetReturns, assetCloses[i]/assetCloses[i-1]-1)
		btcReturns = append(btcReturns, btcCloses[i]/btcCloses[i-1]-1)
	}
	return assetReturns, btcReturns
}

func pearson(xs, ys []float64) (float64, bool) {
	if len(xs) == 0 || len(xs) != len(ys) {
		return 0, false
	}
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX := sumX / float64(len(xs))
	meanY := sumY / float64(len(ys))

	var numerator, sumSqX, sumSqY float64
	for i := range xs {
		dx := xs[i] - meanX
		dy := ys[i] - meanY
		numerator += dx * dy
		sumSqX += dx * dx
		sumSqY += dy * dy
	}
	denominator := math.Sqrt(sumSqX * sumSqY)
	if denominator == 0 {
		return 0, false
	}
	return numerator / denominator, true
}

func source(row domain.MetricsRow, fetchers Fetchers) (domain.Exchange, string, KlineFetcher, bool) {
	if row.Instrument.OnBinance && row.Instrument.BinanceSymbol != "" && fetchers.Binance != nil {
		return domain.ExchangeBinance, row.Instrument.BinanceSymbol, fetchers.Binance, true
	}
	if row.Instrument.OnBybit && row.Instrument.BybitSymbol != "" && fetchers.Bybit != nil {
		return domain.ExchangeBybit, row.Instrument.BybitSymbol, fetchers.Bybit, true
	}
	return "", "", nil, false
}

func logWarn(logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.Warn(msg, args...)
	}
}

func ErrNoFetcher(exchange domain.Exchange) error {
	if exchange == "" {
		return errors.New("no exchange selected")
	}
	return fmt.Errorf("no kline fetcher for %s", exchange)
}
