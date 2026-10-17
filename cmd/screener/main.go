package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"scalp-assist/internal/api"
	"scalp-assist/internal/arbitrage"
	"scalp-assist/internal/charts"
	"scalp-assist/internal/config"
	"scalp-assist/internal/corr"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange/binance"
	"scalp-assist/internal/exchange/bybit"
	"scalp-assist/internal/exchange/okx"
	"scalp-assist/internal/metrics"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/propscreen"
	"scalp-assist/internal/scheduler"
	"scalp-assist/internal/scoring"
	"scalp-assist/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration file")
	once := flag.Bool("once", false, "fetch market data once and print the scored top")
	resetPassword := flag.Bool("reset-password", false, "reset the auth.bootstrap_email account password")
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

	if *resetPassword {
		if err := runResetPassword(context.Background(), cfg); err != nil {
			slog.Error("reset password failed", "error", err)
			os.Exit(1)
		}
		slog.Info("password reset complete", "email", cfg.Auth.BootstrapEmail)
		return
	}

	if err := runServer(context.Background(), cfg, logger, *configPath); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func runServer(ctx context.Context, cfg config.Config, logger *slog.Logger, configPath string) error {
	serverCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.Auth.BootstrapEmail != "" && cfg.Auth.BootstrapPassword != "" {
		created, err := db.BootstrapUser(ctx, cfg.Auth.BootstrapEmail, cfg.Auth.BootstrapPassword)
		if err != nil {
			return err
		}
		if created {
			logger.Info("bootstrap user created", "email", cfg.Auth.BootstrapEmail)
		}
	}
	secret, err := api.LoadOrCreateJWTSecret(filepath.Join(filepath.Dir(cfg.DBPath), "jwt.secret"))
	if err != nil {
		return err
	}
	binanceClient := binance.New()
	bybitClient := bybit.New()
	okxClient := okx.New()
	sched := scheduler.New(scheduler.Options{
		Store:        db,
		Defaults:     cfg.Settings,
		Blacklist:    cfg.Blacklist,
		Overrides:    cfg.NormalizationOverrides,
		FastInterval: cfg.Cycles.FastInterval,
		SlowInterval: cfg.Cycles.SlowInterval,
		Binance:      binanceClient,
		Bybit:        bybitClient,
		Logger:       logger,
	})
	sched.Start(serverCtx)
	arbitrageService, err := arbitrage.New(arbitrage.Options{
		Store:     db,
		Clients:   []arbitrage.QuoteClient{binanceClient, bybitClient, okxClient},
		Settings:  cfg.Arbitrage,
		Interval:  cfg.Cycles.ArbitrageInterval,
		Blacklist: cfg.Blacklist,
		Overrides: cfg.NormalizationOverrides,
		Logger:    logger,
	})
	if err != nil {
		return err
	}
	arbitrageService.Start(serverCtx)
	propScreenService, err := propscreen.New(propscreen.Options{
		Store:        db,
		Clients:      []propscreen.MarketClient{binanceClient, bybitClient},
		Interval:     cfg.Cycles.PropScreenInterval,
		CorrInterval: cfg.Cycles.SlowInterval,
		CorrFetchers: corr.Fetchers{Binance: binanceClient, Bybit: bybitClient},
		Blacklist:    cfg.Blacklist,
		Overrides:    cfg.NormalizationOverrides,
		Logger:       logger,
	})
	if err != nil {
		return err
	}
	propScreenService.Start(serverCtx)
	chartService, err := charts.New(charts.Options{
		Clients:   []charts.Client{binanceClient, bybitClient},
		Blacklist: cfg.Blacklist,
		Overrides: cfg.NormalizationOverrides,
		Logger:    logger,
	})
	if err != nil {
		return err
	}
	chartService.Start(serverCtx)
	addr := cfg.DevHTTP
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := api.New(api.Options{
		Store:          db,
		Defaults:       cfg.Settings,
		JWTSecret:      secret,
		SecureCookies:  cfg.Auth.SecureCookies,
		TrustedProxies: cfg.Auth.TrustedProxies,
		Refresh:        sched.Refresh,
		Arbitrage:      arbitrageService,
		PropScreen:     propScreenService,
		Charts:         chartService,
	})
	logger.Info("config loaded",
		"path", configPath,
		"domain", cfg.Domain,
		"db_path", cfg.DBPath,
		"dev_http", addr,
		"top_n", cfg.Settings.TopN,
	)
	logger.Info("starting dev http api", "addr", addr)
	httpServer := &http.Server{Addr: addr, Handler: server.Handler()}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-serverCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func runResetPassword(ctx context.Context, cfg config.Config) error {
	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.ResetPassword(ctx, cfg.Auth.BootstrapEmail, cfg.Auth.BootstrapPassword)
}

func runOnce(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var snapshots []domain.MarketSnapshot
	var failures []string
	binanceClient := binance.New()
	bybitClient := bybit.New()

	binanceSnapshot, err := binanceClient.Snapshot(ctx)
	if err != nil {
		failures = append(failures, "binance: "+err.Error())
		logger.Warn("binance snapshot failed", "error", err)
	} else {
		snapshots = append(snapshots, binanceSnapshot)
	}

	bybitSnapshot, err := bybitClient.Snapshot(ctx)
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
	rows = corr.Attach(ctx, rows, corr.Fetchers{Binance: binanceClient, Bybit: bybitClient}, corr.AttachOptions{Logger: logger})
	top := scoring.Top(rows, cfg.Settings)
	printTop(top, len(instruments), len(rows), failures)
	return nil
}

func printTop(rows []domain.MetricsRow, scanned, filtered int, failures []string) {
	fmt.Printf("Scanned: %d  Passed filters: %d  Top: %d\n", scanned, filtered, len(rows))
	if len(failures) > 0 {
		fmt.Printf("Degraded: %s\n", strings.Join(failures, "; "))
	}
	fmt.Println("#  SYMBOL        EXCH   TURNOVER_24H   CHANGE%   RANGE%    CORR   INPLAY   SCORE")
	for _, row := range rows {
		exchanges := exchangeLabel(row.Instrument)
		fmt.Printf("%-2d %-12s %-6s %12.0f %8.2f %8.2f %7s %-8t %.4f\n",
			row.Rank,
			displaySymbol(row.Instrument),
			exchanges,
			row.TurnoverUSD,
			row.ChangePct24h,
			row.DayRangePct,
			formatCorr(row.BTCCorr7d),
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

func formatCorr(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *value)
}
