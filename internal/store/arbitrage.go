package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
)

type SnapshotOverview struct {
	CreatedAt time.Time `json:"updated_at"`
	Rows      int       `json:"rows"`
}

func (s *Store) SaveArbitrageRun(ctx context.Context, run model.Run) (int64, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stateJSON, err := json.Marshal(run.ExchangeState)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO arbitrage_runs(observed_at, exchange_state_json, instruments, opportunities)
VALUES (?, ?, ?, ?)`, formatTime(run.ObservedAt), string(stateJSON), run.Instruments, len(run.Opportunities))
	if err != nil {
		return 0, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, item := range run.Opportunities {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO arbitrage_opportunities (
  run_id, base, quote, multiplier, kind, buy_exchange, sell_exchange, buy_symbol, sell_symbol,
  buy_ask, sell_bid, gross_spread_bps, net_spread_bps, net_maker_taker_bps, capacity_usd, buy_funding_rate,
  sell_funding_rate, buy_funding_interval_hours, sell_funding_interval_hours,
  buy_next_funding_time, sell_next_funding_time, funding_edge_bps_hour,
  buy_quote_at, sell_quote_at, first_seen_at, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, item.Instrument.Base, item.Instrument.Quote, item.Instrument.Multiplier, item.Instrument.Kind,
			item.BuyExchange, item.SellExchange, item.BuySymbol, item.SellSymbol, item.BuyAsk, item.SellBid,
			item.GrossSpreadBps, item.NetSpreadBps, item.NetMakerTakerBps, item.CapacityUSD, nullableFloat(item.BuyFundingRate),
			nullableFloat(item.SellFundingRate), item.BuyFundingIntervalHours, item.SellFundingIntervalHours,
			optionalTime(item.BuyNextFundingTime), optionalTime(item.SellNextFundingTime), nullableFloat(item.FundingEdgeBpsHour),
			formatTime(item.BuyQuoteAt), formatTime(item.SellQuoteAt), formatTime(item.FirstSeenAt), formatTime(item.ObservedAt),
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return runID, nil
}

func (s *Store) LatestArbitrageRun(ctx context.Context) (model.Run, error) {
	var run model.Run
	var observedAt, stateJSON string
	err := s.reader.QueryRowContext(ctx, `
SELECT id, observed_at, exchange_state_json, instruments
FROM arbitrage_runs
ORDER BY observed_at DESC, id DESC
LIMIT 1`).Scan(&run.ID, &observedAt, &stateJSON, &run.Instruments)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, nil
	}
	if err != nil {
		return model.Run{}, err
	}
	run.ObservedAt, _ = parseTime(observedAt)
	if err := json.Unmarshal([]byte(stateJSON), &run.ExchangeState); err != nil {
		return model.Run{}, err
	}
	items, err := s.arbitrageOpportunities(ctx, run.ID)
	if err != nil {
		return model.Run{}, err
	}
	run.Opportunities = items
	return run, nil
}

func (s *Store) ArbitrageStats(ctx context.Context, days int) (model.Stats, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	stats := model.Stats{Days: days}
	if err := s.reader.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM arbitrage_runs
WHERE (? <= 0 OR observed_at >= ?)`, days, formatTime(cutoff)).Scan(&stats.Runs); err != nil {
		return model.Stats{}, err
	}

	var average, maximum sql.NullFloat64
	if err := s.reader.QueryRowContext(ctx, `
SELECT COUNT(*), AVG(net_spread_bps), MAX(net_spread_bps),
       SUM(CASE WHEN net_spread_bps > 0 THEN 1 ELSE 0 END)
FROM arbitrage_opportunities
WHERE (? <= 0 OR observed_at >= ?)`, days, formatTime(cutoff)).
		Scan(&stats.Observations, &average, &maximum, &stats.ProfitableCount); err != nil {
		return model.Stats{}, err
	}
	if average.Valid {
		stats.AverageNetBps = average.Float64
	}
	if maximum.Valid {
		stats.MaxNetBps = maximum.Float64
	}

	rows, err := s.reader.QueryContext(ctx, `
SELECT buy_exchange, sell_exchange, COUNT(*), MAX(net_spread_bps)
FROM arbitrage_opportunities
WHERE (? <= 0 OR observed_at >= ?)
GROUP BY buy_exchange, sell_exchange
ORDER BY COUNT(*) DESC, buy_exchange, sell_exchange`, days, formatTime(cutoff))
	if err != nil {
		return model.Stats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var route model.RouteStat
		if err := rows.Scan(&route.BuyExchange, &route.SellExchange, &route.Count, &route.MaxNetBps); err != nil {
			return model.Stats{}, err
		}
		stats.Routes = append(stats.Routes, route)
	}
	return stats, rows.Err()
}

func (s *Store) DeleteArbitrageBefore(ctx context.Context, cutoff time.Time) error {
	_, err := s.writer.ExecContext(ctx, `DELETE FROM arbitrage_runs WHERE observed_at < ?`, formatTime(cutoff))
	return err
}

func (s *Store) ArbitrageGrossHistory(
	ctx context.Context,
	instrument domain.InstrumentKey,
	from time.Time,
	bucket time.Duration,
	limit int,
) ([]model.ChartPoint, error) {
	bucketSeconds := int64(bucket / time.Second)
	if bucketSeconds <= 0 {
		return nil, errors.New("gross history bucket must be positive")
	}
	rows, err := s.reader.QueryContext(ctx, `
SELECT observed_at, buy_ask, sell_bid
FROM arbitrage_opportunities
WHERE base = ? AND quote = ? AND multiplier = ? AND kind = ?
  AND observed_at >= ? AND buy_ask > 0 AND sell_bid > 0
ORDER BY observed_at ASC, id ASC`,
		instrument.Base, instrument.Quote, instrument.Multiplier, instrument.Kind, formatTime(from.UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bestByTime := make(map[int64]float64)
	for rows.Next() {
		var observedAt string
		var buyAsk, sellBid float64
		if err := rows.Scan(&observedAt, &buyAsk, &sellBid); err != nil {
			return nil, err
		}
		at, err := parseTime(observedAt)
		if err != nil {
			return nil, err
		}
		timestamp := at.UTC().Unix()
		timestamp -= timestamp % bucketSeconds
		gross := (sellBid/buyAsk - 1) * 10000
		if current, ok := bestByTime[timestamp]; !ok || gross > current {
			bestByTime[timestamp] = gross
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	points := make([]model.ChartPoint, 0, len(bestByTime))
	for timestamp, gross := range bestByTime {
		points = append(points, model.ChartPoint{Time: timestamp, Value: gross, Closed: true})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
	if limit > 0 && len(points) > limit {
		points = append([]model.ChartPoint(nil), points[len(points)-limit:]...)
	}
	return points, nil
}

func (s *Store) LatestSnapshotOverview(ctx context.Context) (SnapshotOverview, error) {
	var overview SnapshotOverview
	var createdAt string
	err := s.reader.QueryRowContext(ctx, `
SELECT s.created_at, COUNT(sr.instrument_id)
FROM snapshots s
LEFT JOIN snapshot_rows sr ON sr.snapshot_id = s.id
WHERE s.id = (SELECT id FROM snapshots ORDER BY created_at DESC, id DESC LIMIT 1)
GROUP BY s.id`).Scan(&createdAt, &overview.Rows)
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotOverview{}, nil
	}
	if err != nil {
		return SnapshotOverview{}, err
	}
	overview.CreatedAt, _ = parseTime(createdAt)
	return overview, nil
}

func (s *Store) arbitrageOpportunities(ctx context.Context, runID int64) ([]model.Opportunity, error) {
	rows, err := s.reader.QueryContext(ctx, `
SELECT base, quote, multiplier, kind, buy_exchange, sell_exchange, buy_symbol, sell_symbol,
       buy_ask, sell_bid, gross_spread_bps, net_spread_bps,
       COALESCE(net_maker_taker_bps, net_spread_bps), capacity_usd, buy_funding_rate,
       sell_funding_rate, buy_funding_interval_hours, sell_funding_interval_hours,
       buy_next_funding_time, sell_next_funding_time, funding_edge_bps_hour,
       buy_quote_at, sell_quote_at, first_seen_at, observed_at
FROM arbitrage_opportunities
WHERE run_id = ?
ORDER BY net_spread_bps DESC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Opportunity
	for rows.Next() {
		var item model.Opportunity
		var buyFunding, sellFunding, fundingEdge sql.NullFloat64
		var buyNextFunding, sellNextFunding, buyAt, sellAt, firstSeenAt, observedAt string
		if err := rows.Scan(
			&item.Instrument.Base, &item.Instrument.Quote, &item.Instrument.Multiplier, &item.Instrument.Kind,
			&item.BuyExchange, &item.SellExchange, &item.BuySymbol, &item.SellSymbol,
			&item.BuyAsk, &item.SellBid, &item.GrossSpreadBps, &item.NetSpreadBps, &item.NetMakerTakerBps, &item.CapacityUSD,
			&buyFunding, &sellFunding, &item.BuyFundingIntervalHours, &item.SellFundingIntervalHours,
			&buyNextFunding, &sellNextFunding, &fundingEdge, &buyAt, &sellAt, &firstSeenAt, &observedAt,
		); err != nil {
			return nil, err
		}
		item.BuyFundingRate = nullFloatPointer(buyFunding)
		item.SellFundingRate = nullFloatPointer(sellFunding)
		item.FundingEdgeBpsHour = nullFloatPointer(fundingEdge)
		item.BuyNextFundingTime, _ = parseOptionalTime(buyNextFunding)
		item.SellNextFundingTime, _ = parseOptionalTime(sellNextFunding)
		item.BuyQuoteAt, _ = parseTime(buyAt)
		item.SellQuoteAt, _ = parseTime(sellAt)
		item.FirstSeenAt, _ = parseTime(firstSeenAt)
		item.ObservedAt, _ = parseTime(observedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseTime(value)
}

func nullFloatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}
