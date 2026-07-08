package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"scalp-assist/internal/config"
	"scalp-assist/internal/domain"
)

func TestOpenRunsMigrationsIdempotently(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}
	defer second.Close()

	var count int
	if err := second.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}

func TestSaveAndReadSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	corr := 0.91
	rows := []domain.MetricsRow{
		{
			Instrument: domain.Instrument{
				Base: "BTC", Multiplier: 1, BinanceSymbol: "BTCUSDT", BybitSymbol: "BTCUSDT", OnBinance: true, OnBybit: true,
			},
			Rank: 1, Price: 100, TurnoverUSD: 200, DayRangePct: 30, ChangePct24h: 25,
			FundingRate: 0.001, RelativeTickPct: 0.01, BTCCorr7d: &corr, HighCorr: true,
			IsInplay: true, InPropList: true, BybitOnly: false, Score: 0.88,
		},
	}

	id, err := store.SaveSnapshot(ctx, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), rows)
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	got, err := store.SnapshotRows(ctx, id)
	if err != nil {
		t.Fatalf("SnapshotRows: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	assertRowEqual(t, got[0], rows[0])

	latest, err := store.LatestSnapshotRows(ctx)
	if err != nil {
		t.Fatalf("LatestSnapshotRows: %v", err)
	}
	if len(latest) != 1 || latest[0].Instrument.Base != "BTC" {
		t.Fatalf("unexpected latest rows: %+v", latest)
	}
}

func TestPropListCRUDPreservesKnownInstrumentSymbols(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	_, err := store.SaveSnapshot(ctx, time.Now(), []domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1, BinanceSymbol: "BTCUSDT", OnBinance: true}},
	})
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	err = store.ReplacePropList(ctx, []PropInstrument{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Note: "main"},
		{Instrument: domain.Instrument{Base: "ETH", Multiplier: 1, BybitSymbol: "ETHUSDT", OnBybit: true}, Note: "alt"},
	})
	if err != nil {
		t.Fatalf("ReplacePropList: %v", err)
	}
	props, err := store.PropList(ctx)
	if err != nil {
		t.Fatalf("PropList: %v", err)
	}
	if len(props) != 2 {
		t.Fatalf("len(props) = %d, want 2", len(props))
	}
	if props[0].Instrument.Base != "BTC" || props[0].Instrument.BinanceSymbol != "BTCUSDT" || !props[0].Instrument.OnBinance {
		t.Fatalf("known symbol was not preserved: %+v", props[0])
	}
}

func TestSettingsOverridesRoundTripAndEffectiveConfig(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	defaults := testSettings()

	effective, err := store.ReplaceSettingsOverrides(ctx, defaults, map[string]string{
		"min_volume_usd":   "250000000",
		"weights.turnover": "0.55",
	})
	if err != nil {
		t.Fatalf("ReplaceSettingsOverrides: %v", err)
	}
	if effective.MinVolumeUSD != 250_000_000 || effective.Weights.Turnover != 0.55 {
		t.Fatalf("effective mismatch: %+v", effective)
	}
	overrides, err := store.SettingsOverrides(ctx)
	if err != nil {
		t.Fatalf("SettingsOverrides: %v", err)
	}
	if overrides["min_volume_usd"] != "250000000" {
		t.Fatalf("override missing: %+v", overrides)
	}

	if _, err := store.ReplaceSettingsOverrides(ctx, defaults, map[string]string{"corr_threshold": "1.5"}); err == nil {
		t.Fatal("invalid settings returned nil error")
	}
	afterInvalid, err := store.SettingsOverrides(ctx)
	if err != nil {
		t.Fatalf("SettingsOverrides after invalid: %v", err)
	}
	if afterInvalid["min_volume_usd"] != "250000000" {
		t.Fatalf("invalid update partially applied: %+v", afterInvalid)
	}

	effective, err = store.ReplaceSettingsOverrides(ctx, defaults, nil)
	if err != nil {
		t.Fatalf("reset settings: %v", err)
	}
	if effective != defaults {
		t.Fatalf("effective after reset = %+v, want %+v", effective, defaults)
	}
	empty, err := store.SettingsOverrides(ctx)
	if err != nil {
		t.Fatalf("SettingsOverrides after reset: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("overrides after reset = %+v, want empty", empty)
	}

	if _, err := config.Effective(defaults, overrides); err != nil {
		t.Fatalf("config.Effective with stored overrides: %v", err)
	}
}

func TestCorrCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	value := 0.77
	instrument := domain.Instrument{Base: "SOL", Multiplier: 1, BinanceSymbol: "SOLUSDT", OnBinance: true}
	if err := store.UpsertCorr(ctx, instrument, &value, time.Now()); err != nil {
		t.Fatalf("UpsertCorr value: %v", err)
	}
	if err := store.UpsertCorr(ctx, domain.Instrument{Base: "NEW", Multiplier: 1}, nil, time.Now()); err != nil {
		t.Fatalf("UpsertCorr nil: %v", err)
	}
	cache, err := store.CorrCache(ctx)
	if err != nil {
		t.Fatalf("CorrCache: %v", err)
	}
	if cache["SOL:1"] == nil || *cache["SOL:1"] != 0.77 {
		t.Fatalf("SOL corr mismatch: %+v", cache)
	}
	if _, ok := cache["NEW:1"]; !ok || cache["NEW:1"] != nil {
		t.Fatalf("NEW nil corr mismatch: %+v", cache)
	}
}

func TestBootstrapUserIsIdempotentAndHashesPassword(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	created, err := store.BootstrapUser(ctx, "owner@example.com", "secret")
	if err != nil {
		t.Fatalf("BootstrapUser first: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true")
	}
	created, err = store.BootstrapUser(ctx, "other@example.com", "other")
	if err != nil {
		t.Fatalf("BootstrapUser second: %v", err)
	}
	if created {
		t.Fatal("second bootstrap created user")
	}
	user, err := store.UserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if user.PasswordHash == "secret" {
		t.Fatal("password stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("secret")); err != nil {
		t.Fatalf("bcrypt compare: %v", err)
	}
	ok, err := store.CheckPassword(ctx, "owner@example.com", "secret")
	if err != nil || !ok {
		t.Fatalf("CheckPassword ok=%v err=%v", ok, err)
	}
	ok, err = store.CheckPassword(ctx, "owner@example.com", "wrong")
	if err != nil || ok {
		t.Fatalf("CheckPassword wrong ok=%v err=%v", ok, err)
	}
}

func TestResetPasswordUpdatesOrCreatesUser(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if err := store.ResetPassword(ctx, "owner@example.com", "first"); err != nil {
		t.Fatalf("ResetPassword create: %v", err)
	}
	ok, err := store.CheckPassword(ctx, "owner@example.com", "first")
	if err != nil || !ok {
		t.Fatalf("first password ok=%v err=%v", ok, err)
	}
	if err := store.ResetPassword(ctx, "owner@example.com", "second"); err != nil {
		t.Fatalf("ResetPassword update: %v", err)
	}
	ok, err = store.CheckPassword(ctx, "owner@example.com", "second")
	if err != nil || !ok {
		t.Fatalf("second password ok=%v err=%v", ok, err)
	}
}

func TestConcurrentReadersDuringWritesDoNotGetBusy(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, err := store.SaveSnapshot(ctx, time.Now(), []domain.MetricsRow{
					{Instrument: domain.Instrument{Base: "C" + strconv.Itoa(id), Multiplier: 1}, Rank: j + 1, Price: 1, TurnoverUSD: 1, DayRangePct: 1},
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := store.LatestSnapshotRows(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent operation error: %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), dbPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "screener.db")
}

func assertRowEqual(t *testing.T, got, want domain.MetricsRow) {
	t.Helper()
	if got.Instrument != want.Instrument ||
		got.Rank != want.Rank ||
		got.Price != want.Price ||
		got.TurnoverUSD != want.TurnoverUSD ||
		got.DayRangePct != want.DayRangePct ||
		got.ChangePct24h != want.ChangePct24h ||
		got.FundingRate != want.FundingRate ||
		got.RelativeTickPct != want.RelativeTickPct ||
		got.HighCorr != want.HighCorr ||
		got.IsInplay != want.IsInplay ||
		got.InPropList != want.InPropList ||
		got.BybitOnly != want.BybitOnly ||
		got.Score != want.Score {
		t.Fatalf("row mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
	if (got.BTCCorr7d == nil) != (want.BTCCorr7d == nil) {
		t.Fatalf("corr nil mismatch got=%v want=%v", got.BTCCorr7d, want.BTCCorr7d)
	}
	if got.BTCCorr7d != nil && *got.BTCCorr7d != *want.BTCCorr7d {
		t.Fatalf("corr mismatch got=%v want=%v", *got.BTCCorr7d, *want.BTCCorr7d)
	}
}

func testSettings() domain.Settings {
	return domain.Settings{
		MinVolumeUSD:       100_000_000,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay: domain.InplaySettings{
			MarketThresholdPct: 3,
			Multiplier:         2,
		},
		Weights: domain.ScoreWeights{
			Volatility: 0.4,
			Turnover:   0.3,
			Inplay:     0.2,
			Corr:       0.1,
		},
		TopN: 20,
	}
}
