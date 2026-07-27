package arbitrage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
	"scalp-assist/internal/store"
)

var ErrRefreshInProgress = errors.New("arbitrage refresh already in progress")

type QuoteClient interface {
	Exchange() domain.Exchange
	Quotes(context.Context) ([]domain.VenueQuote, error)
}

type symbolFundingClient interface {
	SymbolFunding(context.Context, string) (domain.FundingInfo, error)
}

type Options struct {
	Store     *store.Store
	Clients   []QuoteClient
	Settings  model.Settings
	Interval  time.Duration
	Blacklist []string
	Overrides map[string]string
	Logger    *slog.Logger
	Now       func() time.Time
}

type Service struct {
	store        *store.Store
	clients      []QuoteClient
	settings     model.Settings
	interval     time.Duration
	resolver     *normalize.Resolver
	logger       *slog.Logger
	now          func() time.Time
	refreshMu    sync.Mutex
	stateMu      sync.RWMutex
	latest       model.Run
	lastPersist  time.Time
	active       map[string]seenState
	chartMu      sync.RWMutex
	chartCatalog map[string]model.ChartInstrument
	chartStreams map[string]*sharedChartStream
}

type seenState struct {
	first time.Time
	last  time.Time
}

func New(opts Options) (*Service, error) {
	resolver, err := normalize.NewResolver(opts.Blacklist, opts.Overrides)
	if err != nil {
		return nil, err
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:        opts.Store,
		clients:      append([]QuoteClient(nil), opts.Clients...),
		settings:     opts.Settings,
		interval:     interval,
		resolver:     resolver,
		logger:       opts.Logger,
		now:          now,
		active:       make(map[string]seenState),
		chartCatalog: make(map[string]model.ChartInstrument),
		chartStreams: make(map[string]*sharedChartStream),
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		if err := s.Refresh(ctx); err != nil {
			s.logWarn("initial arbitrage refresh failed", "error", err)
		}
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Refresh(ctx); err != nil {
					s.logWarn("arbitrage refresh failed", "error", err)
				}
			}
		}
	}()
}

func (s *Service) Refresh(ctx context.Context) error {
	if !s.refreshMu.TryLock() {
		return ErrRefreshInProgress
	}
	defer s.refreshMu.Unlock()

	now := s.now().UTC()
	quotes, states, successes := s.fetchQuotes(ctx, now)
	run := model.Run{
		ObservedAt:    now,
		ExchangeState: states,
	}
	if successes < 2 {
		s.setLatest(run)
		return fmt.Errorf("arbitrage requires at least two exchanges, available: %d", successes)
	}
	if successes == len(s.clients) {
		s.updateChartCatalog(quotes)
	}

	result := Scan(quotes, s.settings, s.resolver, now)
	run.Instruments = result.Instruments
	run.Opportunities = result.Opportunities
	s.enrichFunding(ctx, run.Opportunities)
	s.applyFirstSeen(run.Opportunities, now)
	s.setLatest(run)

	if s.store != nil && (s.lastPersist.IsZero() || now.Sub(s.lastPersist) >= s.settings.HistoryInterval) {
		runID, err := s.store.SaveArbitrageRun(ctx, run)
		if err != nil {
			return err
		}
		run.ID = runID
		s.lastPersist = now
		s.setLatest(run)
		s.broadcastStoredGross(run)
		cutoff := now.AddDate(0, 0, -s.settings.RetentionDays)
		if err := s.store.DeleteArbitrageBefore(ctx, cutoff); err != nil {
			s.logWarn("arbitrage retention cleanup failed", "error", err)
		}
	}
	return nil
}

func (s *Service) Latest() model.Run {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return cloneRun(s.latest)
}

func (s *Service) FeeRates() []model.ExchangeFeeRate {
	rates := make([]model.ExchangeFeeRate, 0, 3)
	for _, exchange := range []domain.Exchange{
		domain.ExchangeBinance,
		domain.ExchangeBybit,
		domain.ExchangeOKX,
	} {
		key := string(exchange)
		rates = append(rates, model.ExchangeFeeRate{
			Exchange: key,
			MakerBps: s.settings.MakerFeesBps[key],
			TakerBps: s.settings.FeesBps[key],
		})
	}
	return rates
}

func (s *Service) fetchQuotes(ctx context.Context, now time.Time) ([]domain.VenueQuote, map[string]model.ExchangeState, int) {
	type result struct {
		exchange domain.Exchange
		quotes   []domain.VenueQuote
		err      error
	}
	results := make(chan result, len(s.clients))
	for _, client := range s.clients {
		client := client
		go func() {
			quotes, err := client.Quotes(ctx)
			results <- result{exchange: client.Exchange(), quotes: quotes, err: err}
		}()
	}

	var quotes []domain.VenueQuote
	states := make(map[string]model.ExchangeState, len(s.clients))
	successes := 0
	for range s.clients {
		item := <-results
		state := model.ExchangeState{OK: item.err == nil, UpdatedAt: now, Quotes: len(item.quotes)}
		if item.err != nil {
			state.Error = item.err.Error()
		} else {
			successes++
			quotes = append(quotes, item.quotes...)
		}
		states[string(item.exchange)] = state
	}
	return quotes, states, successes
}

func (s *Service) enrichFunding(ctx context.Context, opportunities []model.Opportunity) {
	limit := s.settings.FundingCandidates
	if limit <= 0 || len(opportunities) == 0 {
		return
	}
	if limit > len(opportunities) {
		limit = len(opportunities)
	}
	clients := make(map[domain.Exchange]symbolFundingClient)
	for _, client := range s.clients {
		if fundingClient, ok := client.(symbolFundingClient); ok {
			clients[client.Exchange()] = fundingClient
		}
	}
	type request struct {
		exchange domain.Exchange
		symbol   string
	}
	requests := make(map[request]struct{})
	for _, item := range opportunities[:limit] {
		if item.BuyFundingRate == nil && clients[item.BuyExchange] != nil {
			requests[request{exchange: item.BuyExchange, symbol: item.BuySymbol}] = struct{}{}
		}
		if item.SellFundingRate == nil && clients[item.SellExchange] != nil {
			requests[request{exchange: item.SellExchange, symbol: item.SellSymbol}] = struct{}{}
		}
	}
	if len(requests) == 0 {
		return
	}

	type result struct {
		request request
		info    domain.FundingInfo
		err     error
	}
	results := make(chan result, len(requests))
	semaphore := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for req := range requests {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{request: req, err: ctx.Err()}
				return
			}
			info, err := clients[req.exchange].SymbolFunding(ctx, req.symbol)
			results <- result{request: req, info: info, err: err}
		}()
	}
	wg.Wait()
	close(results)

	funding := make(map[request]domain.FundingInfo, len(requests))
	for result := range results {
		if result.err != nil {
			s.logWarn("symbol funding fetch failed", "exchange", result.request.exchange, "symbol", result.request.symbol, "error", result.err)
			continue
		}
		funding[result.request] = result.info
	}
	for i := range opportunities {
		item := &opportunities[i]
		if info, ok := funding[request{exchange: item.BuyExchange, symbol: item.BuySymbol}]; ok {
			rate := info.Rate
			item.BuyFundingRate = &rate
			item.BuyFundingIntervalHours = info.Interval.Hours()
			item.BuyNextFundingTime = info.NextFundingTime
		}
		if info, ok := funding[request{exchange: item.SellExchange, symbol: item.SellSymbol}]; ok {
			rate := info.Rate
			item.SellFundingRate = &rate
			item.SellFundingIntervalHours = info.Interval.Hours()
			item.SellNextFundingTime = info.NextFundingTime
		}
		item.FundingEdgeBpsHour = opportunityFundingEdge(*item)
	}
}

func (s *Service) applyFirstSeen(opportunities []model.Opportunity, now time.Time) {
	next := make(map[string]seenState, len(opportunities))
	maxGap := 3 * s.interval
	for i := range opportunities {
		key := opportunityKey(opportunities[i])
		first := now
		if previous, ok := s.active[key]; ok && now.Sub(previous.last) <= maxGap {
			first = previous.first
		}
		opportunities[i].FirstSeenAt = first
		next[key] = seenState{first: first, last: now}
	}
	s.active = next
}

func (s *Service) setLatest(run model.Run) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.latest = cloneRun(run)
}

func (s *Service) logWarn(message string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(message, args...)
	}
}

func opportunityFundingEdge(item model.Opportunity) *float64 {
	if item.BuyFundingRate == nil || item.SellFundingRate == nil ||
		item.BuyFundingIntervalHours <= 0 || item.SellFundingIntervalHours <= 0 {
		return nil
	}
	value := (*item.SellFundingRate/item.SellFundingIntervalHours -
		*item.BuyFundingRate/item.BuyFundingIntervalHours) * 10_000
	return &value
}

func opportunityKey(item model.Opportunity) string {
	return fmt.Sprintf("%s:%s:%d:%s:%s", item.Instrument.Base, item.Instrument.Quote,
		item.Instrument.Multiplier, item.BuyExchange, item.SellExchange)
}

func cloneRun(run model.Run) model.Run {
	copy := run
	copy.ExchangeState = make(map[string]model.ExchangeState, len(run.ExchangeState))
	for exchange, state := range run.ExchangeState {
		copy.ExchangeState[exchange] = state
	}
	copy.Opportunities = append([]model.Opportunity(nil), run.Opportunities...)
	return copy
}
