package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"scalp-assist/internal/config"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange/binance"
	"scalp-assist/internal/exchange/bybit"
	"scalp-assist/internal/metrics"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/scoring"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration file")
	once := flag.Bool("once", false, "fetch market data once and print the scored top")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	if *once {
		if err := runOnce(context.Background(), cfg, logger); err != nil {
			slog.Error("once run failed", "error", err)
			os.Exit(1)
		}
		return
	}

	slog.Info("config loaded",
		"path", *configPath,
		"domain", cfg.Domain,
		"db_path", cfg.DBPath,
		"dev_http", cfg.DevHTTP,
		"top_n", cfg.Settings.TopN,
	)
}

func runOnce(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var snapshots []domain.MarketSnapshot
	var failures []string

	binanceSnapshot, err := binance.New().Snapshot(ctx)
	if err != nil {
		failures = append(failures, "binance: "+err.Error())
		logger.Warn("binance snapshot failed", "error", err)
	} else {
		snapshots = append(snapshots, binanceSnapshot)
	}

	bybitSnapshot, err := bybit.New().Snapshot(ctx)
	if err != nil {
		failures = append(failures, "bybit: "+err.Error())
		logger.Warn("bybit snapshot failed", "error", err)
	} else {
		snapshots = append(snapshots, bybitSnapshot)
	}

	if len(snapshots) == 0 {
		return fmt.Errorf("all exchanges failed: %s", strings.Join(failures, "; "))
	}

	instruments, err := normalize.FromSnapshots(snapshots, normalize.Options{
		Blacklist: cfg.Blacklist,
		Overrides: cfg.NormalizationOverrides,
		Logger:    logger,
	})
	if err != nil {
		return err
	}
	rows := metrics.BuildRows(instruments, cfg.Settings, metrics.Options{Blacklist: cfg.Blacklist})
	top := scoring.Top(rows, cfg.Settings)
	printTop(top, len(instruments), len(rows), failures)
	return nil
}

func printTop(rows []domain.MetricsRow, scanned, filtered int, failures []string) {
	fmt.Printf("Scanned: %d  Passed filters: %d  Top: %d\n", scanned, filtered, len(rows))
	if len(failures) > 0 {
		fmt.Printf("Degraded: %s\n", strings.Join(failures, "; "))
	}
	fmt.Println("#  SYMBOL        EXCH   TURNOVER_24H   CHANGE%   RANGE%   INPLAY   SCORE")
	for _, row := range rows {
		exchanges := exchangeLabel(row.Instrument)
		fmt.Printf("%-2d %-12s %-6s %12.0f %8.2f %8.2f %-8t %.4f\n",
			row.Rank,
			displaySymbol(row.Instrument),
			exchanges,
			row.TurnoverUSD,
			row.ChangePct24h,
			row.DayRangePct,
			row.IsInplay,
			row.Score,
		)
	}
}

func displaySymbol(instrument domain.Instrument) string {
	if instrument.Multiplier == 1 {
		return instrument.Base + "USDT"
	}
	return fmt.Sprintf("%d%sUSDT", instrument.Multiplier, instrument.Base)
}

func exchangeLabel(instrument domain.Instrument) string {
	switch {
	case instrument.OnBinance && instrument.OnBybit:
		return "BN+BB"
	case instrument.OnBinance:
		return "BN"
	case instrument.OnBybit:
		return "BB"
	default:
		return "-"
	}
}
