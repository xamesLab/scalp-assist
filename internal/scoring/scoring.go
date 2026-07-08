package scoring

import (
	"sort"

	"scalp-assist/internal/domain"
)

func Top(rows []domain.MetricsRow, settings domain.Settings) []domain.MetricsRow {
	if len(rows) == 0 {
		return nil
	}

	out := append([]domain.MetricsRow(nil), rows...)
	volatilityPercentiles := percentiles(out, func(row domain.MetricsRow) float64 {
		return row.DayRangePct
	})
	turnoverPercentiles := percentiles(out, func(row domain.MetricsRow) float64 {
		return row.TurnoverUSD
	})

	for i := range out {
		volatilityScore := volatilityPercentiles[i]
		turnoverScore := turnoverPercentiles[i]
		inplayScore := 0.0
		if out[i].IsInplay {
			inplayScore = 1
		}
		corrPenalty := 0.0
		if out[i].BTCCorr7d != nil && !out[i].IsInplay {
			corrPenalty = *out[i].BTCCorr7d
			out[i].HighCorr = *out[i].BTCCorr7d > settings.CorrThreshold
		}

		out[i].Score = settings.Weights.Volatility*volatilityScore +
			settings.Weights.Turnover*turnoverScore +
			settings.Weights.Inplay*inplayScore -
			settings.Weights.Corr*corrPenalty
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].TurnoverUSD != out[j].TurnoverUSD {
			return out[i].TurnoverUSD > out[j].TurnoverUSD
		}
		if out[i].Instrument.Base != out[j].Instrument.Base {
			return out[i].Instrument.Base < out[j].Instrument.Base
		}
		return out[i].Instrument.Multiplier < out[j].Instrument.Multiplier
	})

	limit := settings.TopN
	if limit <= 0 || limit > len(out) {
		limit = len(out)
	}
	out = out[:limit]
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

type valueAt struct {
	index int
	value float64
}

func percentiles(rows []domain.MetricsRow, value func(domain.MetricsRow) float64) []float64 {
	if len(rows) == 1 {
		return []float64{1}
	}

	values := make([]valueAt, len(rows))
	for i, row := range rows {
		values[i] = valueAt{index: i, value: value(row)}
	}
	sort.SliceStable(values, func(i, j int) bool {
		return values[i].value < values[j].value
	})

	out := make([]float64, len(rows))
	for start := 0; start < len(values); {
		end := start + 1
		for end < len(values) && values[end].value == values[start].value {
			end++
		}
		avgRank := float64(start+end-1) / 2
		percentile := avgRank / float64(len(values)-1)
		for i := start; i < end; i++ {
			out[values[i].index] = percentile
		}
		start = end
	}
	return out
}
