package propscreen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"scalp-assist/internal/corr"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/store"
)

var ErrRefreshInProgress = errors.New("prop-screen refresh already in progress")

type MarketClient interface {
	Exchange() domain.Exchange
	MarketSnapshot(context.Context) (domain.MarketSnapshot, error)
}

type PropStore interface {
	PropList(context.Context) ([]store.PropInstrument, error)
	CorrCache(context.Context) (map[string]*float64, error)
	UpsertCorr(context.Context, domain.Instrument, *float64, time.Time) error
}

type Options struct {
	Store        PropStore
	Clients      []MarketClient
	Interval     time.Duration
	CorrInterval time.Duration
	CorrFetchers corr.Fetchers
	Blacklist    []string
	Overrides    map[string]string
	Logger       *slog.Logger
	Now          func() time.Time
}

type Service struct {
	store        PropStore
	clients      []MarketClient
	interval     time.Duration
	corrInterval time.Duration
	corrFetchers corr.Fetchers
	resolver     *normalize.Resolver
	logger       *slog.Logger
	now          func() time.Time
	refreshMu    sync.Mutex
	corrMu       sync.Mutex
	latestMu     sync.RWMutex
	latest       Run
}

type candidate struct {
	identity normalize.InstrumentIdentity
	contract domain.ContractSpec
	ticker   domain.Ticker
}

func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("prop-screen store is required")
	}
	if len(opts.Clients) == 0 {
		return nil, errors.New("prop-screen clients are required")
	}
	if opts.Interval <= 0 {
		return nil, errors.New("prop-screen interval must be positive")
	}
	if opts.CorrInterval <= 0 {
		opts.CorrInterval = 2 * time.Hour
	}
	resolver, err := normalize.NewResolver(opts.Blacklist, opts.Overrides)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:        opts.Store,
		clients:      append([]MarketClient(nil), opts.Clients...),
		interval:     opts.Interval,
		corrInterval: opts.CorrInterval,
		corrFetchers: opts.CorrFetchers,
		resolver:     resolver,
		logger:       opts.Logger,
		now:          now,
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	initialDone := make(chan struct{})
	go func() {
		if err := s.Refresh(ctx); err != nil {
			s.logWarn("initial prop-screen refresh failed", "error", err)
		}
		close(initialDone)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Refresh(ctx); err != nil {
					s.logWarn("prop-screen refresh failed", "error", err)
				}
			}
		}
	}()
	if s.corrFetchers.Binance != nil || s.corrFetchers.Bybit != nil {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-initialDone:
			}
			if err := s.RecomputeCorr(ctx); err != nil {
				s.logWarn("initial prop-screen correlation refresh failed", "error", err)
			}
			ticker := time.NewTicker(s.corrInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := s.RecomputeCorr(ctx); err != nil {
						s.logWarn("prop-screen correlation refresh failed", "error", err)
					}
				}
			}
		}()
	}
}

func (s *Service) Refresh(ctx context.Context) error {
	if !s.refreshMu.TryLock() {
		return ErrRefreshInProgress
	}
	defer s.refreshMu.Unlock()

	props, err := s.store.PropList(ctx)
	if err != nil {
		return fmt.Errorf("read prop list: %w", err)
	}

	now := s.now().UTC()
	snapshots, states := s.fetchSnapshots(ctx, now)
	run := Run{
		ObservedAt:    now,
		ExchangeState: states,
		PropTotal:     len(props),
		Rows:          []Row{},
	}
	if len(snapshots) == 0 {
		s.setLatest(run)
		return errors.New("all prop-screen exchanges failed")
	}

	run.Rows = BuildRows(props, snapshots, s.resolver)
	if cache, err := s.store.CorrCache(ctx); err != nil {
		s.logWarn("prop-screen correlation cache read failed", "error", err)
	} else {
		applyCorrCache(run.Rows, cache)
	}
	run.Listed = len(run.Rows)
	s.setLatest(run)
	return nil
}

func (s *Service) Latest() Run {
	s.latestMu.RLock()
	defer s.latestMu.RUnlock()
	return cloneRun(s.latest)
}

func (s *Service) RecomputeCorr(ctx context.Context) error {
	if !s.corrMu.TryLock() {
		return errors.New("prop-screen correlation refresh already in progress")
	}
	defer s.corrMu.Unlock()

	run := s.Latest()
	if len(run.Rows) == 0 {
		return nil
	}
	metricRows := make([]domain.MetricsRow, 0, len(run.Rows))
	for _, row := range run.Rows {
		metricRows = append(metricRows, domain.MetricsRow{
			Instrument: row.Instrument,
			BTCCorr7d:  cloneFloat(row.BTCCorr7d),
		})
	}
	attached := corr.Attach(ctx, metricRows, s.corrFetchers, corr.AttachOptions{Logger: s.logger})
	values := make(map[string]*float64, len(attached))
	updatedAt := s.now().UTC()
	for _, row := range attached {
		values[instrumentKey(row.Instrument)] = cloneFloat(row.BTCCorr7d)
		if err := s.store.UpsertCorr(ctx, row.Instrument, row.BTCCorr7d, updatedAt); err != nil {
			return fmt.Errorf("store prop-screen correlation for %s: %w", row.Instrument.Base, err)
		}
	}

	s.latestMu.Lock()
	for i := range s.latest.Rows {
		if value, ok := values[instrumentKey(s.latest.Rows[i].Instrument)]; ok {
			s.latest.Rows[i].BTCCorr7d = cloneFloat(value)
		}
	}
	s.latestMu.Unlock()
	return nil
}

func (s *Service) fetchSnapshots(ctx context.Context, now time.Time) ([]domain.MarketSnapshot, map[string]ExchangeState) {
	type result struct {
		exchange domain.Exchange
		snapshot domain.MarketSnapshot
		err      error
	}
	results := make(chan result, len(s.clients))
	for _, client := range s.clients {
		client := client
		go func() {
			snapshot, err := client.MarketSnapshot(ctx)
			results <- result{exchange: client.Exchange(), snapshot: snapshot, err: err}
		}()
	}

	snapshots := make([]domain.MarketSnapshot, 0, len(s.clients))
	states := make(map[string]ExchangeState, len(s.clients))
	for range s.clients {
		item := <-results
		state := ExchangeState{UpdatedAt: now}
		if item.err != nil {
			state.Error = item.err.Error()
		} else {
			state.OK = true
			state.Tickers = len(item.snapshot.Tickers)
			if !item.snapshot.FetchedAt.IsZero() {
				state.UpdatedAt = item.snapshot.FetchedAt
			}
			snapshots = append(snapshots, item.snapshot)
		}
		states[string(item.exchange)] = state
	}
	return snapshots, states
}

func BuildRows(props []store.PropInstrument, snapshots []domain.MarketSnapshot, resolver *normalize.Resolver) []Row {
	byExchange := map[domain.Exchange]map[string]map[int][]candidate{}
	for _, snapshot := range snapshots {
		contracts := make(map[string]domain.ContractSpec, len(snapshot.Contracts))
		for _, contract := range snapshot.Contracts {
			contracts[contract.Symbol] = contract
		}
		for _, ticker := range snapshot.Tickers {
			contract, ok := contracts[ticker.Symbol]
			if !ok || !validMarket(contract, ticker) {
				continue
			}
			identity, include, err := resolver.Resolve(contract)
			if err != nil || !include {
				continue
			}
			if byExchange[snapshot.Exchange] == nil {
				byExchange[snapshot.Exchange] = map[string]map[int][]candidate{}
			}
			if byExchange[snapshot.Exchange][identity.Base] == nil {
				byExchange[snapshot.Exchange][identity.Base] = map[int][]candidate{}
			}
			byExchange[snapshot.Exchange][identity.Base][identity.Multiplier] = append(
				byExchange[snapshot.Exchange][identity.Base][identity.Multiplier],
				candidate{identity: identity, contract: contract, ticker: ticker},
			)
		}
	}

	propSet := make(map[string]struct{}, len(props))
	for _, prop := range props {
		propSet[propKey(prop.Instrument.Base, prop.Instrument.Multiplier)] = struct{}{}
	}

	rows := make([]Row, 0, len(props))
	for _, prop := range props {
		base := strings.ToUpper(strings.TrimSpace(prop.Instrument.Base))
		multiplier := prop.Instrument.Multiplier
		if multiplier <= 0 {
			multiplier = 1
		}
		binanceCandidate, onBinance := selectCandidate(byExchange[domain.ExchangeBinance][base], multiplier, propSet, base)
		bybitCandidate, onBybit := selectCandidate(byExchange[domain.ExchangeBybit][base], multiplier, propSet, base)
		if !onBinance && !onBybit {
			continue
		}

		selected := bybitCandidate
		source := domain.ExchangeBybit
		if onBinance {
			selected = binanceCandidate
			source = domain.ExchangeBinance
		}
		instrument := prop.Instrument
		instrument.Base = base
		instrument.Multiplier = multiplier
		instrument.OnBinance = onBinance
		instrument.OnBybit = onBybit
		if onBinance {
			instrument.BinanceSymbol = binanceCandidate.contract.Symbol
		}
		if onBybit {
			instrument.BybitSymbol = bybitCandidate.contract.Symbol
		}
		rows = append(rows, Row{
			Instrument:         instrument,
			SourceExchange:     source,
			SourceSymbol:       selected.contract.Symbol,
			ContractMultiplier: selected.identity.Multiplier,
			ChangePct24h:       selected.ticker.ChangePct24h,
			TurnoverUSD:        selected.ticker.TurnoverUSD,
			UpdatedAt:          selected.ticker.UpdatedAt,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Instrument.Base != rows[j].Instrument.Base {
			return rows[i].Instrument.Base < rows[j].Instrument.Base
		}
		return rows[i].Instrument.Multiplier < rows[j].Instrument.Multiplier
	})
	return rows
}

func validMarket(contract domain.ContractSpec, ticker domain.Ticker) bool {
	if !strings.EqualFold(contract.Status, "TRADING") {
		return false
	}
	if !strings.Contains(strings.ToUpper(contract.ContractType), "PERPETUAL") {
		return false
	}
	quote := strings.ToUpper(contract.QuoteAsset)
	if quote != "USDT" && quote != "USDC" {
		return false
	}
	return ticker.TurnoverUSD >= 0
}

func selectCandidate(byMultiplier map[int][]candidate, multiplier int, propSet map[string]struct{}, base string) (candidate, bool) {
	if len(byMultiplier) == 0 {
		return candidate{}, false
	}
	if exact := bestCandidate(byMultiplier[multiplier]); exact != nil {
		return *exact, true
	}
	if multiplier != 1 || len(byMultiplier) != 1 {
		return candidate{}, false
	}
	for actualMultiplier, candidates := range byMultiplier {
		if _, claimed := propSet[propKey(base, actualMultiplier)]; claimed {
			return candidate{}, false
		}
		if selected := bestCandidate(candidates); selected != nil {
			return *selected, true
		}
	}
	return candidate{}, false
}

func bestCandidate(candidates []candidate) *candidate {
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftRank := quoteRank(candidates[i].contract.QuoteAsset)
		rightRank := quoteRank(candidates[j].contract.QuoteAsset)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return candidates[i].contract.Symbol < candidates[j].contract.Symbol
	})
	selected := candidates[0]
	return &selected
}

func quoteRank(quote string) int {
	if strings.EqualFold(quote, "USDT") {
		return 0
	}
	return 1
}

func propKey(base string, multiplier int) string {
	return strings.ToUpper(strings.TrimSpace(base)) + ":" + fmt.Sprint(multiplier)
}

func instrumentKey(instrument domain.Instrument) string {
	multiplier := instrument.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	return propKey(instrument.Base, multiplier)
}

func applyCorrCache(rows []Row, cache map[string]*float64) {
	for i := range rows {
		if value, ok := cache[instrumentKey(rows[i].Instrument)]; ok {
			rows[i].BTCCorr7d = cloneFloat(value)
		}
	}
}

func (s *Service) setLatest(run Run) {
	s.latestMu.Lock()
	s.latest = cloneRun(run)
	s.latestMu.Unlock()
}

func cloneRun(run Run) Run {
	out := run
	out.ExchangeState = make(map[string]ExchangeState, len(run.ExchangeState))
	for key, value := range run.ExchangeState {
		out.ExchangeState[key] = value
	}
	out.Rows = append([]Row(nil), run.Rows...)
	for i := range out.Rows {
		out.Rows[i].BTCCorr7d = cloneFloat(run.Rows[i].BTCCorr7d)
	}
	return out
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Service) logWarn(message string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(message, args...)
	}
}
