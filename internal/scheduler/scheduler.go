package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"scalp-assist/internal/config"
	"scalp-assist/internal/corr"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/metrics"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/scoring"
	"scalp-assist/internal/store"
)

var ErrRefreshInProgress = errors.New("refresh already in progress")

type MarketClient interface {
	corr.KlineFetcher
	Snapshot(ctx context.Context) (domain.MarketSnapshot, error)
}

type Options struct {
	Store        *store.Store
	Defaults     domain.Settings
	Blacklist    []string
	Overrides    map[string]string
	FastInterval time.Duration
	SlowInterval time.Duration
	Binance      MarketClient
	Bybit        MarketClient
	Logger       *slog.Logger
	Now          func() time.Time
}

type Scheduler struct {
	store        *store.Store
	defaults     domain.Settings
	blacklist    []string
	overrides    map[string]string
	fastInterval time.Duration
	slowInterval time.Duration
	binance      MarketClient
	bybit        MarketClient
	logger       *slog.Logger
	now          func() time.Time
	refreshMu    sync.Mutex
	statusMu     sync.RWMutex
	status       Status
}

type Status struct {
	LastFastRun    time.Time
	LastSlowRun    time.Time
	LastSnapshotID int64
	LastFastError  string
	LastSlowError  string
	BinanceOK      bool
	BybitOK        bool
}

func New(opts Options) *Scheduler {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	fast := opts.FastInterval
	if fast <= 0 {
		fast = 15 * time.Minute
	}
	slow := opts.SlowInterval
	if slow <= 0 {
		slow = 2 * time.Hour
	}
	return &Scheduler{
		store:        opts.Store,
		defaults:     opts.Defaults,
		blacklist:    append([]string(nil), opts.Blacklist...),
		overrides:    opts.Overrides,
		fastInterval: fast,
		slowInterval: slow,
		binance:      opts.Binance,
		bybit:        opts.Bybit,
		logger:       opts.Logger,
		now:          now,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.loop(ctx, s.fastInterval, s.Refresh)
	go s.loop(ctx, s.slowInterval, s.RecomputeCorr)
}

func (s *Scheduler) Refresh(ctx context.Context) error {
	if !s.refreshMu.TryLock() {
		return ErrRefreshInProgress
	}
	defer s.refreshMu.Unlock()

	settings, err := s.effectiveSettings(ctx)
	if err != nil {
		s.setFastError(err)
		return err
	}

	snapshots, failures := s.fetchSnapshots(ctx)
	if len(snapshots) == 0 {
		err := fmt.Errorf("all exchanges failed: %v", failures)
		s.setFastError(err)
		return err
	}

	items, err := normalize.FromSnapshots(snapshots, normalize.Options{
		Blacklist: s.blacklist,
		Overrides: s.overrides,
		Logger:    s.logger,
	})
	if err != nil {
		s.setFastError(err)
		return err
	}
	rows := metrics.BuildRows(items, settings, metrics.Options{Blacklist: s.blacklist})
	if err := s.applyCorrCache(ctx, rows); err != nil {
		s.logWarn("corr cache read failed", "error", err)
	}
	if err := s.markPropRows(ctx, rows); err != nil {
		s.logWarn("prop list read failed", "error", err)
	}
	top := scoring.Top(rows, settings)
	snapshotID, err := s.store.SaveSnapshot(ctx, s.now().UTC(), top)
	if err != nil {
		s.setFastError(err)
		return err
	}
	s.statusMu.Lock()
	s.status.LastFastRun = s.now().UTC()
	s.status.LastSnapshotID = snapshotID
	s.status.LastFastError = ""
	s.statusMu.Unlock()
	return nil
}

func (s *Scheduler) RecomputeCorr(ctx context.Context) error {
	rows, err := s.store.LatestSnapshotRows(ctx)
	if err != nil {
		s.setSlowError(err)
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	attached := corr.Attach(ctx, rows, corr.Fetchers{Binance: s.binance, Bybit: s.bybit}, corr.AttachOptions{Logger: s.logger})
	for _, row := range attached {
		if err := s.store.UpsertCorr(ctx, row.Instrument, row.BTCCorr7d, s.now().UTC()); err != nil {
			s.setSlowError(err)
			return err
		}
	}
	s.statusMu.Lock()
	s.status.LastSlowRun = s.now().UTC()
	s.status.LastSlowError = ""
	s.statusMu.Unlock()
	return nil
}

func (s *Scheduler) Status() Status {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.status
}

func (s *Scheduler) loop(ctx context.Context, interval time.Duration, run func(context.Context) error) {
	if err := run(ctx); err != nil {
		s.logWarn("scheduler run failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := run(ctx); err != nil {
				s.logWarn("scheduler run failed", "error", err)
			}
		}
	}
}

func (s *Scheduler) effectiveSettings(ctx context.Context) (domain.Settings, error) {
	overrides, err := s.store.SettingsOverrides(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	return config.Effective(s.defaults, overrides)
}

func (s *Scheduler) fetchSnapshots(ctx context.Context) ([]domain.MarketSnapshot, []string) {
	var snapshots []domain.MarketSnapshot
	var failures []string
	status := s.Status()
	if s.binance != nil {
		snapshot, err := s.binance.Snapshot(ctx)
		if err != nil {
			failures = append(failures, "binance: "+err.Error())
			status.BinanceOK = false
		} else {
			snapshots = append(snapshots, snapshot)
			status.BinanceOK = true
		}
	}
	if s.bybit != nil {
		snapshot, err := s.bybit.Snapshot(ctx)
		if err != nil {
			failures = append(failures, "bybit: "+err.Error())
			status.BybitOK = false
		} else {
			snapshots = append(snapshots, snapshot)
			status.BybitOK = true
		}
	}
	s.statusMu.Lock()
	s.status.BinanceOK = status.BinanceOK
	s.status.BybitOK = status.BybitOK
	s.statusMu.Unlock()
	return snapshots, failures
}

func (s *Scheduler) applyCorrCache(ctx context.Context, rows []domain.MetricsRow) error {
	cache, err := s.store.CorrCache(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		value, ok := cache[instrumentKey(rows[i].Instrument)]
		if ok {
			rows[i].BTCCorr7d = value
		}
	}
	return nil
}

func (s *Scheduler) markPropRows(ctx context.Context, rows []domain.MetricsRow) error {
	props, err := s.store.PropList(ctx)
	if err != nil {
		return err
	}
	propSet := make(map[string]struct{}, len(props))
	for _, prop := range props {
		propSet[instrumentKey(prop.Instrument)] = struct{}{}
	}
	for i := range rows {
		_, rows[i].InPropList = propSet[instrumentKey(rows[i].Instrument)]
	}
	return nil
}

func (s *Scheduler) setFastError(err error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastFastRun = s.now().UTC()
	s.status.LastFastError = err.Error()
}

func (s *Scheduler) setSlowError(err error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastSlowRun = s.now().UTC()
	s.status.LastSlowError = err.Error()
}

func (s *Scheduler) logWarn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}

func instrumentKey(instrument domain.Instrument) string {
	return fmt.Sprintf("%s:%d", instrument.Base, instrument.Multiplier)
}
