package scoring

import (
	"testing"

	"scalp-assist/internal/domain"
)

func TestTopOrdersByVolatilityWeight(t *testing.T) {
	settings := testSettings()
	settings.TopN = 3
	settings.Weights = domain.ScoreWeights{Volatility: 1}

	rows := []domain.MetricsRow{
		row("LOW", 10, 100),
		row("HIGH", 30, 100),
		row("MID", 20, 100),
	}

	top := Top(rows, settings)
	if got := bases(top); got != "HIGH,MID,LOW" {
		t.Fatalf("order = %s", got)
	}
	if top[0].Rank != 1 || top[1].Rank != 2 || top[2].Rank != 3 {
		t.Fatalf("ranks not assigned: %+v", top)
	}
}

func TestTopOrdersByTurnoverWeight(t *testing.T) {
	settings := testSettings()
	settings.Weights = domain.ScoreWeights{Turnover: 1}

	top := Top([]domain.MetricsRow{
		row("LOW", 10, 100),
		row("HIGH", 10, 300),
		row("MID", 10, 200),
	}, settings)
	if got := bases(top); got != "HIGH,MID,LOW" {
		t.Fatalf("order = %s", got)
	}
}

func TestTopInplayBonusChangesOrder(t *testing.T) {
	settings := testSettings()
	settings.Weights = domain.ScoreWeights{Inplay: 1}

	inplay := row("INPLAY", 10, 100)
	inplay.IsInplay = true
	normal := row("NORMAL", 99, 1000)

	top := Top([]domain.MetricsRow{normal, inplay}, settings)
	if top[0].Instrument.Base != "INPLAY" {
		t.Fatalf("first = %s, want INPLAY", top[0].Instrument.Base)
	}
}

func TestTopAppliesCorrPenaltyUnlessInplay(t *testing.T) {
	settings := testSettings()
	settings.CorrThreshold = 0.8
	settings.Weights = domain.ScoreWeights{Corr: 1}

	corr := 0.9
	penalized := row("CORR", 10, 100)
	penalized.BTCCorr7d = &corr
	protected := row("INPLAY", 10, 100)
	protected.BTCCorr7d = &corr
	protected.IsInplay = true

	top := Top([]domain.MetricsRow{penalized, protected}, settings)
	if top[0].Instrument.Base != "INPLAY" {
		t.Fatalf("first = %s, want INPLAY", top[0].Instrument.Base)
	}
	if !top[1].HighCorr {
		t.Fatalf("HighCorr on penalized row = false, want true")
	}
	if top[0].HighCorr {
		t.Fatalf("HighCorr on inplay row = true, want false")
	}
}

func TestTopLimitsRowsAndDoesNotMutateInput(t *testing.T) {
	settings := testSettings()
	settings.TopN = 2
	settings.Weights = domain.ScoreWeights{Turnover: 1}

	rows := []domain.MetricsRow{
		row("A", 10, 100),
		row("B", 10, 300),
		row("C", 10, 200),
	}
	top := Top(rows, settings)
	if len(top) != 2 {
		t.Fatalf("len(top) = %d, want 2", len(top))
	}
	if rows[0].Rank != 0 || rows[0].Score != 0 {
		t.Fatalf("input mutated: %+v", rows[0])
	}
}

func TestTopTieBreaksByTurnoverThenBase(t *testing.T) {
	settings := testSettings()
	settings.Weights = domain.ScoreWeights{}

	top := Top([]domain.MetricsRow{
		row("BBB", 10, 100),
		row("AAA", 10, 100),
		row("CCC", 10, 200),
	}, settings)
	if got := bases(top); got != "CCC,AAA,BBB" {
		t.Fatalf("order = %s", got)
	}
}

func testSettings() domain.Settings {
	return domain.Settings{
		CorrThreshold: 0.8,
		Weights: domain.ScoreWeights{
			Volatility: 0.4,
			Turnover:   0.3,
			Inplay:     0.2,
			Corr:       0.1,
		},
		TopN: 20,
	}
}

func row(base string, dayRange, turnover float64) domain.MetricsRow {
	return domain.MetricsRow{
		Instrument:  domain.Instrument{Base: base, Multiplier: 1},
		DayRangePct: dayRange,
		TurnoverUSD: turnover,
	}
}

func bases(rows []domain.MetricsRow) string {
	out := ""
	for i, row := range rows {
		if i > 0 {
			out += ","
		}
		out += row.Instrument.Base
	}
	return out
}
