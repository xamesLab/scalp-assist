package normalize

import (
	"strconv"
	"testing"

	"scalp-assist/internal/domain"
)

func TestFromSnapshotsNormalizesSymbols(t *testing.T) {
	snapshot := domain.MarketSnapshot{
		Exchange: domain.ExchangeBinance,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBinance, "BTCUSDT", "BTC", "USDT"),
			contract(domain.ExchangeBinance, "1000PEPEUSDT", "1000PEPE", "USDT"),
			contract(domain.ExchangeBinance, "1000SHIBUSDT", "1000SHIB", "USDT"),
			contract(domain.ExchangeBinance, "ETHUSDC", "ETH", "USDC"),
			contract(domain.ExchangeBinance, "USDCUSDT", "USDC", "USDT"),
			contract(domain.ExchangeBinance, "BADUSDT", "BAD", "USDT"),
		},
		Tickers: []domain.Ticker{
			ticker(domain.ExchangeBinance, "BTCUSDT"),
			ticker(domain.ExchangeBinance, "1000PEPEUSDT"),
			ticker(domain.ExchangeBinance, "1000SHIBUSDT"),
			ticker(domain.ExchangeBinance, "ETHUSDC"),
			ticker(domain.ExchangeBinance, "USDCUSDT"),
			ticker(domain.ExchangeBinance, "BADUSDT"),
		},
	}

	got, err := FromSnapshots([]domain.MarketSnapshot{snapshot}, Options{Blacklist: []string{"BAD"}})
	if err != nil {
		t.Fatalf("FromSnapshots returned error: %v", err)
	}
	byID := index(got)

	assertPresent(t, byID, "BTC:1")
	assertPresent(t, byID, "PEPE:1000")
	assertPresent(t, byID, "SHIB:1000")
	assertPresent(t, byID, "ETH:1")
	assertAbsent(t, byID, "USDC:1")
	assertAbsent(t, byID, "BAD:1")
}

func TestFromSnapshotsHandlesBybitSuffixMultiplier(t *testing.T) {
	snapshot := domain.MarketSnapshot{
		Exchange: domain.ExchangeBybit,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBybit, "SHIB1000USDT", "SHIB1000", "USDT"),
		},
		Tickers: []domain.Ticker{
			ticker(domain.ExchangeBybit, "SHIB1000USDT"),
		},
	}

	got, err := FromSnapshots([]domain.MarketSnapshot{snapshot}, Options{})
	if err != nil {
		t.Fatalf("FromSnapshots returned error: %v", err)
	}
	item, ok := index(got)["SHIB:1000"]
	if !ok {
		t.Fatalf("SHIB:1000 missing in %+v", got)
	}
	if !item.Instrument.OnBybit || item.Instrument.BybitSymbol != "SHIB1000USDT" {
		t.Fatalf("bybit flags not set: %+v", item)
	}
}

func TestFromSnapshotsDeduplicatesAcrossExchanges(t *testing.T) {
	binance := domain.MarketSnapshot{
		Exchange: domain.ExchangeBinance,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBinance, "BTCUSDT", "BTC", "USDT"),
		},
		Tickers: []domain.Ticker{ticker(domain.ExchangeBinance, "BTCUSDT")},
	}
	bybit := domain.MarketSnapshot{
		Exchange: domain.ExchangeBybit,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBybit, "BTCUSDT", "BTC", "USDT"),
		},
		Tickers: []domain.Ticker{ticker(domain.ExchangeBybit, "BTCUSDT")},
	}

	got, err := FromSnapshots([]domain.MarketSnapshot{binance, bybit}, Options{})
	if err != nil {
		t.Fatalf("FromSnapshots returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %+v", len(got), got)
	}
	item := got[0]
	if !item.Instrument.OnBinance || !item.Instrument.OnBybit {
		t.Fatalf("exchange flags not set: %+v", item.Instrument)
	}
	if item.BinanceTicker == nil || item.BybitTicker == nil {
		t.Fatalf("tickers not attached: %+v", item)
	}
}

func TestFromSnapshotsDoesNotMergeDifferentMultipliersWithoutOverride(t *testing.T) {
	binance := domain.MarketSnapshot{
		Exchange: domain.ExchangeBinance,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBinance, "1000PEPEUSDT", "1000PEPE", "USDT"),
		},
		Tickers: []domain.Ticker{ticker(domain.ExchangeBinance, "1000PEPEUSDT")},
	}
	bybit := domain.MarketSnapshot{
		Exchange: domain.ExchangeBybit,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBybit, "PEPEUSDT", "PEPE", "USDT"),
		},
		Tickers: []domain.Ticker{ticker(domain.ExchangeBybit, "PEPEUSDT")},
	}

	got, err := FromSnapshots([]domain.MarketSnapshot{binance, bybit}, Options{})
	if err != nil {
		t.Fatalf("FromSnapshots returned error: %v", err)
	}
	byID := index(got)
	assertPresent(t, byID, "PEPE:1")
	assertPresent(t, byID, "PEPE:1000")
}

func TestFromSnapshotsAppliesOverride(t *testing.T) {
	snapshot := domain.MarketSnapshot{
		Exchange: domain.ExchangeBybit,
		Contracts: []domain.ContractSpec{
			contract(domain.ExchangeBybit, "PEPEUSDT", "PEPE", "USDT"),
		},
		Tickers: []domain.Ticker{ticker(domain.ExchangeBybit, "PEPEUSDT")},
	}

	got, err := FromSnapshots([]domain.MarketSnapshot{snapshot}, Options{
		Overrides: map[string]string{"PEPEUSDT": "PEPE:1000"},
	})
	if err != nil {
		t.Fatalf("FromSnapshots returned error: %v", err)
	}
	assertPresent(t, index(got), "PEPE:1000")
}

func contract(exchange domain.Exchange, symbol, base, quote string) domain.ContractSpec {
	return domain.ContractSpec{
		Exchange:     exchange,
		Symbol:       symbol,
		BaseAsset:    base,
		QuoteAsset:   quote,
		Status:       "Trading",
		ContractType: "PERPETUAL",
		TickSize:     0.01,
	}
}

func ticker(exchange domain.Exchange, symbol string) domain.Ticker {
	return domain.Ticker{
		Symbol:       symbol,
		SourceSymbol: symbol,
		Exchange:     exchange,
		Price:        1,
		High24h:      2,
		Low24h:       1,
		TurnoverUSD:  100,
		Status:       "Trading",
	}
}

func index(items []InstrumentData) map[string]InstrumentData {
	out := make(map[string]InstrumentData, len(items))
	for _, item := range items {
		key := item.Instrument.Base + ":" + intString(item.Instrument.Multiplier)
		out[key] = item
	}
	return out
}

func intString(v int) string {
	return strconv.Itoa(v)
}

func assertPresent(t *testing.T, values map[string]InstrumentData, key string) {
	t.Helper()
	if _, ok := values[key]; !ok {
		t.Fatalf("%s missing in %+v", key, values)
	}
}

func assertAbsent(t *testing.T, values map[string]InstrumentData, key string) {
	t.Helper()
	if _, ok := values[key]; ok {
		t.Fatalf("%s unexpectedly present in %+v", key, values)
	}
}
