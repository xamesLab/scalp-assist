package normalize

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"scalp-assist/internal/domain"
)

type InstrumentData struct {
	Instrument    domain.Instrument
	BinanceTicker *domain.Ticker
	BybitTicker   *domain.Ticker
}

type Options struct {
	Blacklist []string
	Overrides map[string]string
	Logger    *slog.Logger
}

type InstrumentIdentity struct {
	Base       string
	Quote      string
	Multiplier int
}

type Resolver struct {
	blacklist map[string]struct{}
	overrides map[string]identity
}

type identity struct {
	base       string
	multiplier int
}

var stableBases = map[string]struct{}{
	"USDT": {}, "USDC": {}, "BUSD": {}, "TUSD": {}, "FDUSD": {}, "DAI": {}, "USDP": {},
}

var multipliers = []int{1000000, 10000, 1000, 100, 10}

func FromSnapshots(snapshots []domain.MarketSnapshot, opts Options) ([]InstrumentData, error) {
	resolver, err := NewResolver(opts.Blacklist, opts.Overrides)
	if err != nil {
		return nil, err
	}

	contractsByExchangeSymbol := make(map[domain.Exchange]map[string]domain.ContractSpec)
	for _, snapshot := range snapshots {
		if contractsByExchangeSymbol[snapshot.Exchange] == nil {
			contractsByExchangeSymbol[snapshot.Exchange] = map[string]domain.ContractSpec{}
		}
		for _, contract := range snapshot.Contracts {
			contractsByExchangeSymbol[snapshot.Exchange][contract.Symbol] = contract
		}
	}

	byID := map[identity]*InstrumentData{}
	for _, snapshot := range snapshots {
		for _, ticker := range snapshot.Tickers {
			contract, ok := contractsByExchangeSymbol[snapshot.Exchange][ticker.Symbol]
			if !ok {
				contract = domain.ContractSpec{
					Exchange:   ticker.Exchange,
					Symbol:     ticker.Symbol,
					QuoteAsset: ticker.QuoteAsset,
					Status:     ticker.Status,
					TickSize:   ticker.TickSize,
				}
			}
			resolved, include, err := resolver.Resolve(contract)
			if err != nil {
				if opts.Logger != nil {
					opts.Logger.Warn("unknown symbol pattern", "exchange", contract.Exchange, "symbol", contract.Symbol, "error", err)
				}
				continue
			}
			if !include {
				continue
			}
			id := identity{base: resolved.Base, multiplier: resolved.Multiplier}
			item := byID[id]
			if item == nil {
				item = &InstrumentData{
					Instrument: domain.Instrument{
						Base:       id.base,
						Multiplier: id.multiplier,
					},
				}
				byID[id] = item
			}

			tickerCopy := ticker
			tickerCopy.Symbol = normalizedSymbol(id)
			tickerCopy.SourceSymbol = contract.Symbol
			tickerCopy.QuoteAsset = contract.QuoteAsset
			if tickerCopy.Status == "" {
				tickerCopy.Status = contract.Status
			}
			if tickerCopy.TickSize == 0 {
				tickerCopy.TickSize = contract.TickSize
			}

			switch snapshot.Exchange {
			case domain.ExchangeBinance:
				item.Instrument.OnBinance = true
				item.Instrument.BinanceSymbol = contract.Symbol
				item.BinanceTicker = &tickerCopy
			case domain.ExchangeBybit:
				item.Instrument.OnBybit = true
				item.Instrument.BybitSymbol = contract.Symbol
				item.BybitTicker = &tickerCopy
			}
		}
	}

	out := make([]InstrumentData, 0, len(byID))
	for _, item := range byID {
		out = append(out, *item)
	}
	return out, nil
}

func NewResolver(blacklist []string, overrides map[string]string) (*Resolver, error) {
	parsed, err := parseOverrides(overrides)
	if err != nil {
		return nil, err
	}
	return &Resolver{
		blacklist: makeSet(blacklist),
		overrides: parsed,
	}, nil
}

func (r *Resolver) Resolve(contract domain.ContractSpec) (InstrumentIdentity, bool, error) {
	id, include, err := normalizeContract(contract, r.blacklist, r.overrides)
	if err != nil || !include {
		return InstrumentIdentity{}, include, err
	}
	quote := strings.ToUpper(contract.QuoteAsset)
	if quote == "" {
		quote = detectQuote(strings.ToUpper(contract.Symbol))
	}
	return InstrumentIdentity{
		Base:       id.base,
		Quote:      quote,
		Multiplier: id.multiplier,
	}, true, nil
}

func normalizeContract(contract domain.ContractSpec, blacklist map[string]struct{}, overrides map[string]identity) (identity, bool, error) {
	symbol := strings.ToUpper(contract.Symbol)
	quote := strings.ToUpper(contract.QuoteAsset)
	if quote == "" {
		quote = detectQuote(symbol)
	}
	if quote != "USDT" && quote != "USDC" {
		return identity{}, false, nil
	}

	if override, ok := overrides[symbol]; ok {
		if shouldDropBase(override.base, blacklist) {
			return identity{}, false, nil
		}
		return override, true, nil
	}

	base := strings.ToUpper(contract.BaseAsset)
	if base == "" {
		if quote == "" || !strings.HasSuffix(symbol, quote) {
			return identity{}, false, fmt.Errorf("cannot detect quote")
		}
		base = strings.TrimSuffix(symbol, quote)
	}

	base, multiplier := parseMultiplier(base)
	if shouldDropBase(base, blacklist) {
		return identity{}, false, nil
	}
	if base == "" {
		return identity{}, false, fmt.Errorf("empty base")
	}
	return identity{base: base, multiplier: multiplier}, true, nil
}

func parseMultiplier(base string) (string, int) {
	base = strings.ToUpper(base)
	for _, multiplier := range multipliers {
		prefix := strconv.Itoa(multiplier)
		if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
			return strings.TrimPrefix(base, prefix), multiplier
		}
		if strings.HasSuffix(base, prefix) && len(base) > len(prefix) {
			return strings.TrimSuffix(base, prefix), multiplier
		}
	}
	return base, 1
}

func detectQuote(symbol string) string {
	for _, quote := range []string{"USDT", "USDC"} {
		if strings.HasSuffix(symbol, quote) {
			return quote
		}
	}
	return ""
}

func parseOverrides(raw map[string]string) (map[string]identity, error) {
	out := make(map[string]identity, len(raw))
	for symbol, value := range raw {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("normalization override %s must have BASE:MULTIPLIER format", symbol)
		}
		multiplier, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("normalization override %s multiplier: %w", symbol, err)
		}
		if multiplier <= 0 {
			return nil, fmt.Errorf("normalization override %s multiplier must be positive", symbol)
		}
		base := strings.ToUpper(strings.TrimSpace(parts[0]))
		if base == "" {
			return nil, fmt.Errorf("normalization override %s base must not be empty", symbol)
		}
		out[strings.ToUpper(symbol)] = identity{base: base, multiplier: multiplier}
	}
	return out, nil
}

func makeSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[strings.ToUpper(value)] = struct{}{}
	}
	return out
}

func shouldDropBase(base string, blacklist map[string]struct{}) bool {
	base = strings.ToUpper(base)
	if _, ok := blacklist[base]; ok {
		return true
	}
	_, ok := stableBases[base]
	return ok
}

func normalizedSymbol(id identity) string {
	if id.multiplier == 1 {
		return id.base + "USDT"
	}
	return strconv.Itoa(id.multiplier) + id.base + "USDT"
}
