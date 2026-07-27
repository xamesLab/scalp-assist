package arbitrage

import (
	"math"
	"sort"
	"strings"
	"time"

	"scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/normalize"
)

type ScanResult struct {
	Instruments   int
	Opportunities []model.Opportunity
}

func Scan(quotes []domain.VenueQuote, settings model.Settings, resolver *normalize.Resolver, now time.Time) ScanResult {
	grouped := make(map[domain.InstrumentKey][]domain.VenueQuote)
	for _, quote := range quotes {
		if !validQuote(quote, settings, now) {
			continue
		}
		identity, include, err := resolver.Resolve(quote.Contract)
		if err != nil || !include || identity.Quote != "USDT" {
			continue
		}
		key := domain.InstrumentKey{
			Base:       identity.Base,
			Quote:      identity.Quote,
			Multiplier: identity.Multiplier,
			Kind:       "linear_perpetual",
		}
		grouped[key] = append(grouped[key], quote)
	}

	var opportunities []model.Opportunity
	instruments := 0
	for key, venues := range grouped {
		if uniqueExchangeCount(venues) < 2 {
			continue
		}
		instruments++
		for buyIndex := range venues {
			for sellIndex := range venues {
				if buyIndex == sellIndex || venues[buyIndex].Exchange == venues[sellIndex].Exchange {
					continue
				}
				buy := venues[buyIndex]
				sell := venues[sellIndex]
				if durationAbs(buy.UpdatedAt.Sub(sell.UpdatedAt)) > settings.MaxQuoteSkew {
					continue
				}
				gross := (sell.BidPrice - buy.AskPrice) / buy.AskPrice * 10_000
				if gross < settings.MinGrossSpreadBps ||
					(settings.MaxGrossSpreadBps > 0 && gross > settings.MaxGrossSpreadBps) {
					continue
				}
				net := gross - fee(settings, buy.Exchange) - fee(settings, sell.Exchange)
				netMakerTaker := gross - makerFee(settings, buy.Exchange) - fee(settings, sell.Exchange)
				if net < settings.MinNetSpreadBps {
					continue
				}
				capacity := capacityUSD(buy, sell)
				if capacity < settings.MinCapacityUSD {
					continue
				}
				opportunity := model.Opportunity{
					Instrument:               key,
					BuyExchange:              buy.Exchange,
					SellExchange:             sell.Exchange,
					BuySymbol:                buy.SourceSymbol,
					SellSymbol:               sell.SourceSymbol,
					BuyAsk:                   buy.AskPrice,
					SellBid:                  sell.BidPrice,
					GrossSpreadBps:           gross,
					NetSpreadBps:             net,
					NetMakerTakerBps:         netMakerTaker,
					CapacityUSD:              capacity,
					BuyFundingRate:           cloneFloat(buy.FundingRate),
					SellFundingRate:          cloneFloat(sell.FundingRate),
					BuyFundingIntervalHours:  buy.FundingInterval.Hours(),
					SellFundingIntervalHours: sell.FundingInterval.Hours(),
					BuyNextFundingTime:       buy.NextFundingTime,
					SellNextFundingTime:      sell.NextFundingTime,
					BuyQuoteAt:               buy.UpdatedAt.UTC(),
					SellQuoteAt:              sell.UpdatedAt.UTC(),
					FirstSeenAt:              now.UTC(),
					ObservedAt:               now.UTC(),
				}
				opportunity.FundingEdgeBpsHour = fundingEdge(buy, sell)
				opportunities = append(opportunities, opportunity)
			}
		}
	}

	sort.Slice(opportunities, func(i, j int) bool {
		if opportunities[i].NetSpreadBps == opportunities[j].NetSpreadBps {
			return opportunities[i].GrossSpreadBps > opportunities[j].GrossSpreadBps
		}
		return opportunities[i].NetSpreadBps > opportunities[j].NetSpreadBps
	})
	if settings.TopN > 0 && len(opportunities) > settings.TopN {
		opportunities = opportunities[:settings.TopN]
	}
	return ScanResult{Instruments: instruments, Opportunities: opportunities}
}

func validQuote(quote domain.VenueQuote, settings model.Settings, now time.Time) bool {
	if quote.BidPrice <= 0 || quote.AskPrice <= 0 || quote.AskPrice < quote.BidPrice {
		return false
	}
	if !strings.EqualFold(quote.Contract.Status, "TRADING") {
		return false
	}
	if !strings.Contains(strings.ToUpper(quote.Contract.ContractType), "PERPETUAL") {
		return false
	}
	if quote.UpdatedAt.IsZero() {
		return false
	}
	age := now.Sub(quote.UpdatedAt)
	return age <= settings.MaxQuoteAge && age >= -settings.MaxQuoteSkew
}

func uniqueExchangeCount(quotes []domain.VenueQuote) int {
	seen := make(map[domain.Exchange]struct{}, len(quotes))
	for _, quote := range quotes {
		seen[quote.Exchange] = struct{}{}
	}
	return len(seen)
}

func fee(settings model.Settings, exchange domain.Exchange) float64 {
	return settings.FeesBps[string(exchange)]
}

func makerFee(settings model.Settings, exchange domain.Exchange) float64 {
	return settings.MakerFeesBps[string(exchange)]
}

func capacityUSD(buy, sell domain.VenueQuote) float64 {
	buyMultiplier := buy.QuantityMultiplier
	if buyMultiplier <= 0 {
		buyMultiplier = 1
	}
	sellMultiplier := sell.QuantityMultiplier
	if sellMultiplier <= 0 {
		sellMultiplier = 1
	}
	buyCapacity := buy.AskQuantity * buyMultiplier * buy.AskPrice
	sellCapacity := sell.BidQuantity * sellMultiplier * sell.BidPrice
	return math.Min(buyCapacity, sellCapacity)
}

func fundingEdge(buy, sell domain.VenueQuote) *float64 {
	if buy.FundingRate == nil || sell.FundingRate == nil ||
		buy.FundingInterval <= 0 || sell.FundingInterval <= 0 {
		return nil
	}
	buyHourly := *buy.FundingRate / buy.FundingInterval.Hours()
	sellHourly := *sell.FundingRate / sell.FundingInterval.Hours()
	value := (sellHourly - buyHourly) * 10_000
	return &value
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func durationAbs(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
