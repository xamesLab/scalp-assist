package metrics

import (
	"math"
	"sort"
	"strings"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
)

type Options struct {
	Blacklist []string
}

func BuildRows(items []normalize.InstrumentData, settings domain.Settings, opts Options) []domain.MetricsRow {
	blacklist := makeSet(opts.Blacklist)
	changes := marketChanges(items, blacklist)
	median := median(changes)

	rows := make([]domain.MetricsRow, 0, len(items))
	for _, item := range items {
		if shouldDropBase(item.Instrument.Base, blacklist) {
			continue
		}
		source := preferredTicker(item)
		if source == nil {
			continue
		}
		if !isTrading(source.Status) {
			continue
		}
		if source.Low24h <= 0 || source.Price <= 0 {
			continue
		}

		turnover := sumTurnover(item)
		if turnover < settings.MinVolumeUSD {
			continue
		}
		if settings.EnableChangeFilter && math.Abs(source.ChangePct24h) < settings.MinChangePct {
			continue
		}

		dayRange := (source.High24h - source.Low24h) / source.Low24h * 100
		relativeTick := source.TickSize / source.Price * 100
		rows = append(rows, domain.MetricsRow{
			Instrument:      item.Instrument,
			Price:           source.Price,
			TurnoverUSD:     turnover,
			DayRangePct:     dayRange,
			ChangePct24h:    source.ChangePct24h,
			FundingRate:     source.FundingRate,
			RelativeTickPct: relativeTick,
			IsInplay:        isInplay(source.ChangePct24h, median, settings.Inplay),
			BybitOnly:       item.Instrument.OnBybit && !item.Instrument.OnBinance,
		})
	}
	return rows
}

func preferredTicker(item normalize.InstrumentData) *domain.Ticker {
	if item.BinanceTicker != nil {
		return item.BinanceTicker
	}
	return item.BybitTicker
}

func sumTurnover(item normalize.InstrumentData) float64 {
	var turnover float64
	if item.BinanceTicker != nil {
		turnover += item.BinanceTicker.TurnoverUSD
	}
	if item.BybitTicker != nil {
		turnover += item.BybitTicker.TurnoverUSD
	}
	return turnover
}

func marketChanges(items []normalize.InstrumentData, blacklist map[string]struct{}) []float64 {
	changes := make([]float64, 0, len(items))
	for _, item := range items {
		if shouldDropBase(item.Instrument.Base, blacklist) {
			continue
		}
		source := preferredTicker(item)
		if source == nil || !isTrading(source.Status) || source.Low24h <= 0 || source.Price <= 0 {
			continue
		}
		changes = append(changes, source.ChangePct24h)
	}
	return changes
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func isInplay(change, marketMedian float64, settings domain.InplaySettings) bool {
	if marketMedian < settings.MarketThresholdPct {
		return false
	}
	return change >= marketMedian*settings.Multiplier || change < 0
}

func isTrading(status string) bool {
	normalized := strings.ToUpper(status)
	return normalized == "TRADING" || normalized == "TRADING_STATUS_TRADING"
}

func makeSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[strings.ToUpper(value)] = struct{}{}
	}
	return out
}

func shouldDropBase(base string, blacklist map[string]struct{}) bool {
	_, ok := blacklist[strings.ToUpper(base)]
	return ok
}
