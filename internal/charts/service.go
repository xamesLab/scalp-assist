package charts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
)

var (
	ErrInstrumentNotFound = errors.New("chart instrument not found")
	ErrInterval           = errors.New("chart interval must be 5m or 1h")
)

type Client interface {
	Exchange() domain.Exchange
	Quotes(context.Context) ([]domain.VenueQuote, error)
	Klines(context.Context, string, string, int) ([]domain.Kline, map[string]string, error)
	StreamKlines(context.Context, string, string, chan<- domain.Kline) error
}

type Options struct {
	Clients   []Client
	Blacklist []string
	Overrides map[string]string
	Logger    *slog.Logger
}

type Instrument struct {
	ID         string          `json:"id"`
	Base       string          `json:"base"`
	Quote      string          `json:"quote"`
	Multiplier int             `json:"multiplier"`
	Exchange   domain.Exchange `json:"exchange"`
	Symbol     string          `json:"symbol"`
	TickSize   float64         `json:"tick_size,omitempty"`
}

type Candle struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Closed bool    `json:"closed,omitempty"`
}

type Snapshot struct {
	Instrument Instrument `json:"instrument"`
	Interval   string     `json:"interval"`
	Candles    []Candle   `json:"candles"`
}

type Event struct {
	Type   string  `json:"type"`
	Status string  `json:"status,omitempty"`
	Error  string  `json:"error,omitempty"`
	Candle *Candle `json:"candle,omitempty"`
}

type sharedStream struct {
	key         string
	instrument  Instrument
	interval    string
	ctx         context.Context
	cancel      context.CancelFunc
	subscribers map[chan Event]struct{}
}

type Service struct {
	clients   map[domain.Exchange]Client
	resolver  *normalize.Resolver
	logger    *slog.Logger
	refreshMu sync.Mutex
	mu        sync.RWMutex
	catalog   map[string]Instrument
	streams   map[string]*sharedStream
}

func New(opts Options) (*Service, error) {
	if len(opts.Clients) == 0 {
		return nil, errors.New("chart clients are required")
	}
	resolver, err := normalize.NewResolver(opts.Blacklist, opts.Overrides)
	if err != nil {
		return nil, err
	}
	clients := make(map[domain.Exchange]Client, len(opts.Clients))
	for _, client := range opts.Clients {
		if client != nil {
			clients[client.Exchange()] = client
		}
	}
	return &Service{
		clients:  clients,
		resolver: resolver,
		logger:   opts.Logger,
		catalog:  make(map[string]Instrument),
		streams:  make(map[string]*sharedStream),
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		if err := s.Refresh(ctx); err != nil {
			s.logWarn("initial chart catalog refresh failed", "error", err)
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Refresh(ctx); err != nil {
					s.logWarn("chart catalog refresh failed", "error", err)
				}
			}
		}
	}()
}

func (s *Service) Refresh(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	type result struct {
		exchange domain.Exchange
		quotes   []domain.VenueQuote
		err      error
	}
	results := make(chan result, len(s.clients))
	for exchange, client := range s.clients {
		go func(exchange domain.Exchange, client Client) {
			quotes, err := client.Quotes(ctx)
			results <- result{exchange: exchange, quotes: quotes, err: err}
		}(exchange, client)
	}

	candidates := make(map[string]Instrument)
	successes := 0
	var failures []string
	for range s.clients {
		item := <-results
		if item.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", item.exchange, item.err))
			continue
		}
		successes++
		for _, quote := range item.quotes {
			contract := quote.Contract
			if !strings.EqualFold(contract.Status, "TRADING") ||
				!strings.Contains(strings.ToUpper(contract.ContractType), "PERPETUAL") {
				continue
			}
			identity, include, err := s.resolver.Resolve(contract)
			if err != nil || !include {
				continue
			}
			quoteAsset := strings.ToUpper(identity.Quote)
			if quoteAsset != "USDT" && quoteAsset != "USDC" {
				continue
			}
			id := instrumentID(identity.Base, identity.Multiplier)
			next := Instrument{
				ID:         id,
				Base:       identity.Base,
				Quote:      quoteAsset,
				Multiplier: identity.Multiplier,
				Exchange:   item.exchange,
				Symbol:     quote.SourceSymbol,
				TickSize:   contract.TickSize,
			}
			if current, ok := candidates[id]; !ok || instrumentPriority(next) < instrumentPriority(current) {
				candidates[id] = next
			}
		}
	}
	if successes == 0 {
		return fmt.Errorf("all chart exchanges failed: %s", strings.Join(failures, "; "))
	}
	s.mu.Lock()
	s.catalog = candidates
	s.mu.Unlock()
	return nil
}

func (s *Service) Instruments() []Instrument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Instrument, 0, len(s.catalog))
	for _, instrument := range s.catalog {
		out = append(out, instrument)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Base != out[j].Base {
			return out[i].Base < out[j].Base
		}
		return out[i].Multiplier < out[j].Multiplier
	})
	return out
}

func (s *Service) Snapshot(ctx context.Context, instrumentID, interval string, limit int) (Snapshot, error) {
	if !validInterval(interval) {
		return Snapshot{}, ErrInterval
	}
	instrument, ok := s.instrument(instrumentID)
	if !ok {
		return Snapshot{}, ErrInstrumentNotFound
	}
	if limit <= 0 || limit > 300 {
		limit = 300
	}
	client := s.clients[instrument.Exchange]
	if client == nil {
		return Snapshot{}, errors.New("chart exchange client unavailable")
	}
	klines, _, err := client.Klines(ctx, instrument.Symbol, historyInterval(instrument.Exchange, interval), limit)
	if err != nil {
		return Snapshot{}, err
	}
	candles := make([]Candle, 0, len(klines))
	for _, kline := range klines {
		candle, ok := candleFromKline(kline, interval)
		if ok {
			candles = append(candles, candle)
		}
	}
	return Snapshot{Instrument: instrument, Interval: interval, Candles: candles}, nil
}

func (s *Service) Subscribe(instrumentID, interval string) (<-chan Event, func(), error) {
	if !validInterval(interval) {
		return nil, nil, ErrInterval
	}
	instrument, ok := s.instrument(instrumentID)
	if !ok {
		return nil, nil, ErrInstrumentNotFound
	}
	key := instrumentID + "|" + interval
	events := make(chan Event, 256)
	s.mu.Lock()
	stream := s.streams[key]
	start := stream == nil
	if start {
		streamCtx, cancel := context.WithCancel(context.Background())
		stream = &sharedStream{
			key:         key,
			instrument:  instrument,
			interval:    interval,
			ctx:         streamCtx,
			cancel:      cancel,
			subscribers: make(map[chan Event]struct{}),
		}
		s.streams[key] = stream
	}
	stream.subscribers[events] = struct{}{}
	s.mu.Unlock()
	if start {
		go s.runStream(stream)
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			if current := s.streams[key]; current == stream {
				delete(stream.subscribers, events)
				if len(stream.subscribers) == 0 {
					delete(s.streams, key)
					stream.cancel()
				}
			}
			s.mu.Unlock()
		})
	}
	return events, unsubscribe, nil
}

func (s *Service) runStream(stream *sharedStream) {
	client := s.clients[stream.instrument.Exchange]
	if client == nil {
		s.broadcast(stream, Event{Type: "status", Status: "unavailable", Error: "chart exchange client unavailable"})
		return
	}
	backoff := time.Second
	for stream.ctx.Err() == nil {
		s.broadcast(stream, Event{Type: "status", Status: "connecting"})
		updates := make(chan domain.Kline, 32)
		errs := make(chan error, 1)
		go func() {
			errs <- client.StreamKlines(stream.ctx, stream.instrument.Symbol, stream.interval, updates)
		}()
		connected := false
		var streamErr error
	readLoop:
		for {
			select {
			case <-stream.ctx.Done():
				return
			case kline := <-updates:
				candle, ok := candleFromKline(kline, stream.interval)
				if !ok {
					continue
				}
				if !connected {
					connected = true
					backoff = time.Second
					s.broadcast(stream, Event{Type: "status", Status: "connected"})
				}
				s.broadcast(stream, Event{Type: "bar", Candle: &candle})
			case streamErr = <-errs:
				break readLoop
			}
		}
		message := "websocket disconnected"
		if streamErr != nil {
			message = streamErr.Error()
		}
		s.broadcast(stream, Event{Type: "status", Status: "reconnecting", Error: message})
		timer := time.NewTimer(backoff)
		select {
		case <-stream.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *Service) broadcast(stream *sharedStream, event Event) {
	s.mu.RLock()
	if s.streams[stream.key] != stream {
		s.mu.RUnlock()
		return
	}
	subscribers := make([]chan Event, 0, len(stream.subscribers))
	for subscriber := range stream.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	s.mu.RUnlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (s *Service) instrument(id string) (Instrument, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instrument, ok := s.catalog[id]
	return instrument, ok
}

func instrumentID(base string, multiplier int) string {
	return strings.ToUpper(base) + ":" + strconv.Itoa(multiplier)
}

func instrumentPriority(instrument Instrument) int {
	priority := 100
	if instrument.Exchange == domain.ExchangeBinance {
		priority = 0
	} else if instrument.Exchange == domain.ExchangeBybit {
		priority = 20
	}
	if instrument.Quote == "USDC" {
		priority += 10
	}
	return priority
}

func historyInterval(exchange domain.Exchange, interval string) string {
	if exchange != domain.ExchangeBybit {
		return interval
	}
	if interval == "1h" {
		return "60"
	}
	return "5"
}

func validInterval(interval string) bool {
	return interval == "5m" || interval == "1h"
}

func intervalDuration(interval string) time.Duration {
	if interval == "1h" {
		return time.Hour
	}
	return 5 * time.Minute
}

func candleFromKline(kline domain.Kline, interval string) (Candle, bool) {
	if kline.OpenTime.IsZero() || kline.Close <= 0 {
		return Candle{}, false
	}
	open, high, low := kline.Open, kline.High, kline.Low
	if open <= 0 {
		open = kline.Close
	}
	if high <= 0 {
		high = max(open, kline.Close)
	}
	if low <= 0 {
		low = min(open, kline.Close)
	}
	bucket := int64(intervalDuration(interval) / time.Second)
	at := kline.OpenTime.UTC().Unix()
	return Candle{
		Time:   at - at%bucket,
		Open:   open,
		High:   high,
		Low:    low,
		Close:  kline.Close,
		Closed: kline.Closed,
	}, true
}

func (s *Service) logWarn(message string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(message, args...)
	}
}
