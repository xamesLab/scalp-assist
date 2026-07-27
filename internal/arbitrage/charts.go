package arbitrage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
)

var (
	ErrChartInstrumentNotFound = errors.New("chart instrument not found")
	ErrChartInterval           = errors.New("chart interval must be 1m or 5m")
)

type chartClient interface {
	Exchange() domain.Exchange
	Klines(context.Context, string, string, int) ([]domain.Kline, map[string]string, error)
	StreamKlines(context.Context, string, string, chan<- domain.Kline) error
}

type sharedChartStream struct {
	key         string
	instrument  model.ChartInstrument
	interval    string
	ctx         context.Context
	cancel      context.CancelFunc
	subscribers map[chan model.ChartEvent]struct{}
}

func (s *Service) ChartInstruments() []model.ChartInstrument {
	s.chartMu.RLock()
	defer s.chartMu.RUnlock()

	out := make([]model.ChartInstrument, 0, len(s.chartCatalog))
	for _, item := range s.chartCatalog {
		out = append(out, cloneChartInstrument(item))
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Instrument, out[j].Instrument
		if left.Base != right.Base {
			return left.Base < right.Base
		}
		return left.Multiplier < right.Multiplier
	})
	return out
}

func (s *Service) ChartSnapshot(ctx context.Context, instrumentID, interval string, limit int) (model.ChartSnapshot, error) {
	if !validChartInterval(interval) {
		return model.ChartSnapshot{}, ErrChartInterval
	}
	instrument, ok := s.chartInstrument(instrumentID)
	if !ok {
		return model.ChartSnapshot{}, ErrChartInstrumentNotFound
	}
	if limit <= 0 || limit > 300 {
		limit = 300
	}

	snapshot := model.ChartSnapshot{
		Type:       "snapshot",
		Instrument: instrument,
		Interval:   interval,
		Series:     make(map[string][]model.ChartPoint, 3),
		Errors:     make(map[string]string),
	}
	type result struct {
		exchange domain.Exchange
		klines   []domain.Kline
		err      error
	}
	results := make(chan result, 3)
	clients := s.chartClientMap()
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit, domain.ExchangeOKX} {
		client := clients[exchange]
		symbol := instrument.Symbols[exchange]
		if client == nil || symbol == "" {
			results <- result{exchange: exchange, err: fmt.Errorf("chart client unavailable")}
			continue
		}
		go func(exchange domain.Exchange, client chartClient, symbol string) {
			wireInterval := chartHistoryInterval(exchange, interval)
			klines, _, err := client.Klines(ctx, symbol, wireInterval, limit)
			results <- result{exchange: exchange, klines: klines, err: err}
		}(exchange, client, symbol)
	}

	for range 3 {
		item := <-results
		key := string(item.exchange)
		if item.err != nil {
			snapshot.Errors[key] = item.err.Error()
			snapshot.Series[key] = []model.ChartPoint{}
			continue
		}
		points := make([]model.ChartPoint, 0, len(item.klines))
		for _, kline := range item.klines {
			if kline.OpenTime.IsZero() || kline.Close <= 0 {
				continue
			}
			points = append(points, chartPoint(kline, interval))
		}
		snapshot.Series[key] = points
	}
	if len(snapshot.Errors) == 0 {
		snapshot.Errors = nil
		alignChartSnapshot(snapshot.Series)
	}
	if s.store != nil {
		duration := chartIntervalDuration(interval)
		from := s.now().UTC().Add(-time.Duration(limit) * duration)
		storedGross, err := s.store.ArbitrageGrossHistory(ctx, instrument.Instrument, from, duration, limit)
		if err != nil {
			if snapshot.Errors == nil {
				snapshot.Errors = make(map[string]string)
			}
			snapshot.Errors["stored_gross"] = err.Error()
		} else {
			snapshot.StoredGross = storedGross
		}
	}
	return snapshot, nil
}

func (s *Service) SubscribeChart(instrumentID, interval string) (<-chan model.ChartEvent, func(), error) {
	if !validChartInterval(interval) {
		return nil, nil, ErrChartInterval
	}
	instrument, ok := s.chartInstrument(instrumentID)
	if !ok {
		return nil, nil, ErrChartInstrumentNotFound
	}

	key := instrumentID + "|" + interval
	events := make(chan model.ChartEvent, 256)
	s.chartMu.Lock()
	stream := s.chartStreams[key]
	start := stream == nil
	if start {
		streamCtx, cancel := context.WithCancel(context.Background())
		stream = &sharedChartStream{
			key:         key,
			instrument:  instrument,
			interval:    interval,
			ctx:         streamCtx,
			cancel:      cancel,
			subscribers: make(map[chan model.ChartEvent]struct{}),
		}
		s.chartStreams[key] = stream
	}
	stream.subscribers[events] = struct{}{}
	s.chartMu.Unlock()

	if start {
		go s.runSharedChartStream(stream)
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.chartMu.Lock()
			current := s.chartStreams[key]
			if current == stream {
				delete(stream.subscribers, events)
				if len(stream.subscribers) == 0 {
					delete(s.chartStreams, key)
					stream.cancel()
				}
			}
			s.chartMu.Unlock()
		})
	}
	return events, unsubscribe, nil
}

func (s *Service) updateChartCatalog(quotes []domain.VenueQuote) {
	candidates := make(map[string]model.ChartInstrument)
	for _, quote := range quotes {
		contract := quote.Contract
		if !strings.EqualFold(contract.Status, "TRADING") ||
			!strings.Contains(strings.ToUpper(contract.ContractType), "PERPETUAL") {
			continue
		}
		identity, include, err := s.resolver.Resolve(contract)
		if err != nil || !include || !strings.EqualFold(identity.Quote, "USDT") {
			continue
		}
		key := domain.InstrumentKey{
			Base:       identity.Base,
			Quote:      "USDT",
			Multiplier: identity.Multiplier,
			Kind:       "linear_perpetual",
		}
		id := chartInstrumentID(key)
		item := candidates[id]
		if item.Symbols == nil {
			item = model.ChartInstrument{
				ID:         id,
				Instrument: key,
				Symbols:    make(map[domain.Exchange]string, 3),
			}
		}
		item.Symbols[quote.Exchange] = quote.SourceSymbol
		candidates[id] = item
	}

	next := make(map[string]model.ChartInstrument)
	for id, item := range candidates {
		if item.Symbols[domain.ExchangeBinance] != "" &&
			item.Symbols[domain.ExchangeBybit] != "" &&
			item.Symbols[domain.ExchangeOKX] != "" {
			next[id] = item
		}
	}
	s.chartMu.Lock()
	s.chartCatalog = next
	s.chartMu.Unlock()
}

func (s *Service) runSharedChartStream(stream *sharedChartStream) {
	clients := s.chartClientMap()
	var wg sync.WaitGroup
	for _, exchange := range []domain.Exchange{domain.ExchangeBinance, domain.ExchangeBybit, domain.ExchangeOKX} {
		client := clients[exchange]
		symbol := stream.instrument.Symbols[exchange]
		if client == nil || symbol == "" {
			s.broadcastChart(stream, model.ChartEvent{
				Type: "status", Exchange: string(exchange), Status: "unavailable",
				Error: "chart client unavailable",
			})
			continue
		}
		wg.Add(1)
		go func(exchange domain.Exchange, client chartClient, symbol string) {
			defer wg.Done()
			s.runChartFeed(stream, exchange, client, symbol)
		}(exchange, client, symbol)
	}
	wg.Wait()
}

func (s *Service) runChartFeed(stream *sharedChartStream, exchange domain.Exchange, client chartClient, symbol string) {
	backoff := time.Second
	for {
		if stream.ctx.Err() != nil {
			return
		}
		s.broadcastChart(stream, model.ChartEvent{
			Type: "status", Exchange: string(exchange), Status: "connecting",
		})
		updates := make(chan domain.Kline, 32)
		errs := make(chan error, 1)
		go func() {
			errs <- client.StreamKlines(stream.ctx, symbol, stream.interval, updates)
		}()

		connected := false
		var streamErr error
	readLoop:
		for {
			select {
			case <-stream.ctx.Done():
				return
			case update := <-updates:
				if !connected {
					connected = true
					backoff = time.Second
					s.broadcastChart(stream, model.ChartEvent{
						Type: "status", Exchange: string(exchange), Status: "connected",
					})
				}
				point := chartPoint(update, stream.interval)
				s.broadcastChart(stream, model.ChartEvent{
					Type: "bar", Exchange: string(exchange), Point: &point,
				})
			case streamErr = <-errs:
				break readLoop
			}
		}
		if stream.ctx.Err() != nil {
			return
		}
		message := "websocket disconnected"
		if streamErr != nil {
			message = streamErr.Error()
		}
		s.broadcastChart(stream, model.ChartEvent{
			Type: "status", Exchange: string(exchange), Status: "reconnecting", Error: message,
		})
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

func (s *Service) broadcastChart(stream *sharedChartStream, event model.ChartEvent) {
	s.chartMu.RLock()
	current := s.chartStreams[stream.key]
	if current != stream {
		s.chartMu.RUnlock()
		return
	}
	subscribers := make([]chan model.ChartEvent, 0, len(stream.subscribers))
	for subscriber := range stream.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	s.chartMu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (s *Service) broadcastStoredGross(run model.Run) {
	bestByInstrument := make(map[string]float64)
	for _, item := range run.Opportunities {
		if item.BuyAsk <= 0 || item.SellBid <= 0 {
			continue
		}
		id := chartInstrumentID(item.Instrument)
		gross := (item.SellBid/item.BuyAsk - 1) * 10000
		if current, ok := bestByInstrument[id]; !ok || gross > current {
			bestByInstrument[id] = gross
		}
	}
	if len(bestByInstrument) == 0 {
		return
	}

	s.chartMu.RLock()
	streams := make([]*sharedChartStream, 0, len(s.chartStreams))
	for _, stream := range s.chartStreams {
		if _, ok := bestByInstrument[stream.instrument.ID]; ok {
			streams = append(streams, stream)
		}
	}
	s.chartMu.RUnlock()

	for _, stream := range streams {
		point := model.ChartPoint{
			Time:   chartBucketTime(run.ObservedAt, stream.interval),
			Value:  bestByInstrument[stream.instrument.ID],
			Closed: false,
		}
		s.broadcastChart(stream, model.ChartEvent{Type: "gross", Point: &point})
	}
}

func (s *Service) chartInstrument(id string) (model.ChartInstrument, bool) {
	s.chartMu.RLock()
	defer s.chartMu.RUnlock()
	item, ok := s.chartCatalog[id]
	return cloneChartInstrument(item), ok
}

func (s *Service) chartClientMap() map[domain.Exchange]chartClient {
	out := make(map[domain.Exchange]chartClient, 3)
	for _, client := range s.clients {
		if chart, ok := client.(chartClient); ok {
			out[client.Exchange()] = chart
		}
	}
	return out
}

func chartInstrumentID(key domain.InstrumentKey) string {
	return strings.Join([]string{
		key.Base,
		key.Quote,
		strconv.Itoa(key.Multiplier),
		key.Kind,
	}, ":")
}

func chartHistoryInterval(exchange domain.Exchange, interval string) string {
	if exchange == domain.ExchangeBybit {
		return strings.TrimSuffix(interval, "m")
	}
	return interval
}

func chartPoint(kline domain.Kline, interval string) model.ChartPoint {
	return model.ChartPoint{
		Time:   chartBucketTime(kline.OpenTime, interval),
		Value:  kline.Close,
		Closed: kline.Closed,
	}
}

func chartBucketTime(at time.Time, interval string) int64 {
	timestamp := at.UTC().Unix()
	bucketSeconds := int64(chartIntervalDuration(interval) / time.Second)
	return timestamp - timestamp%bucketSeconds
}

func chartIntervalDuration(interval string) time.Duration {
	if interval == "5m" {
		return 5 * time.Minute
	}
	return time.Minute
}

func alignChartSnapshot(series map[string][]model.ChartPoint) {
	exchanges := []string{"binance", "bybit", "okx"}
	var from, to int64
	for index, exchange := range exchanges {
		points := series[exchange]
		if len(points) == 0 {
			return
		}
		first := points[0].Time
		last := points[len(points)-1].Time
		if index == 0 || first > from {
			from = first
		}
		if index == 0 || last < to {
			to = last
		}
	}
	if from > to {
		return
	}
	for _, exchange := range exchanges {
		points := series[exchange]
		start := sort.Search(len(points), func(index int) bool { return points[index].Time >= from })
		end := sort.Search(len(points), func(index int) bool { return points[index].Time > to })
		series[exchange] = append([]model.ChartPoint(nil), points[start:end]...)
	}
}

func validChartInterval(interval string) bool {
	return interval == "1m" || interval == "5m"
}

func cloneChartInstrument(item model.ChartInstrument) model.ChartInstrument {
	copy := item
	copy.Symbols = make(map[domain.Exchange]string, len(item.Symbols))
	for exchange, symbol := range item.Symbols {
		copy.Symbols[exchange] = symbol
	}
	return copy
}
