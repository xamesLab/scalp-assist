package model

import (
	"time"

	"scalp-assist/internal/domain"
)

type Settings struct {
	MinGrossSpreadBps float64            `json:"min_gross_spread_bps" yaml:"min_gross_spread_bps"`
	MaxGrossSpreadBps float64            `json:"max_gross_spread_bps" yaml:"max_gross_spread_bps"`
	MinNetSpreadBps   float64            `json:"min_net_spread_bps" yaml:"min_net_spread_bps"`
	MinCapacityUSD    float64            `json:"min_capacity_usd" yaml:"min_capacity_usd"`
	MaxQuoteAge       time.Duration      `json:"max_quote_age" yaml:"max_quote_age"`
	MaxQuoteSkew      time.Duration      `json:"max_quote_skew" yaml:"max_quote_skew"`
	TopN              int                `json:"top_n" yaml:"top_n"`
	FundingCandidates int                `json:"funding_candidates" yaml:"funding_candidates"`
	HistoryInterval   time.Duration      `json:"history_interval" yaml:"history_interval"`
	RetentionDays     int                `json:"retention_days" yaml:"retention_days"`
	FeesBps           map[string]float64 `json:"fees_bps" yaml:"fees_bps"`
	MakerFeesBps      map[string]float64 `json:"maker_fees_bps" yaml:"maker_fees_bps"`
}

type Opportunity struct {
	Instrument               domain.InstrumentKey `json:"instrument"`
	BuyExchange              domain.Exchange      `json:"buy_exchange"`
	SellExchange             domain.Exchange      `json:"sell_exchange"`
	BuySymbol                string               `json:"buy_symbol"`
	SellSymbol               string               `json:"sell_symbol"`
	BuyAsk                   float64              `json:"buy_ask"`
	SellBid                  float64              `json:"sell_bid"`
	GrossSpreadBps           float64              `json:"gross_spread_bps"`
	NetSpreadBps             float64              `json:"net_spread_bps"`
	NetMakerTakerBps         float64              `json:"net_maker_taker_bps"`
	CapacityUSD              float64              `json:"capacity_usd"`
	BuyFundingRate           *float64             `json:"buy_funding_rate,omitempty"`
	SellFundingRate          *float64             `json:"sell_funding_rate,omitempty"`
	BuyFundingIntervalHours  float64              `json:"buy_funding_interval_hours,omitempty"`
	SellFundingIntervalHours float64              `json:"sell_funding_interval_hours,omitempty"`
	BuyNextFundingTime       time.Time            `json:"buy_next_funding_time,omitempty"`
	SellNextFundingTime      time.Time            `json:"sell_next_funding_time,omitempty"`
	FundingEdgeBpsHour       *float64             `json:"funding_edge_bps_hour,omitempty"`
	BuyQuoteAt               time.Time            `json:"buy_quote_at"`
	SellQuoteAt              time.Time            `json:"sell_quote_at"`
	FirstSeenAt              time.Time            `json:"first_seen_at"`
	ObservedAt               time.Time            `json:"observed_at"`
}

type ExchangeState struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Quotes    int       `json:"quotes"`
}

type Run struct {
	ID            int64                    `json:"id,omitempty"`
	ObservedAt    time.Time                `json:"observed_at"`
	ExchangeState map[string]ExchangeState `json:"exchanges"`
	Instruments   int                      `json:"instruments"`
	Opportunities []Opportunity            `json:"opportunities"`
}

type RouteStat struct {
	BuyExchange  domain.Exchange `json:"buy_exchange"`
	SellExchange domain.Exchange `json:"sell_exchange"`
	Count        int             `json:"count"`
	MaxNetBps    float64         `json:"max_net_bps"`
}

type Stats struct {
	Days            int         `json:"days"`
	Runs            int         `json:"runs"`
	Observations    int         `json:"observations"`
	AverageNetBps   float64     `json:"average_net_bps"`
	MaxNetBps       float64     `json:"max_net_bps"`
	ProfitableCount int         `json:"profitable_count"`
	Routes          []RouteStat `json:"routes"`
}

type ExchangeFeeRate struct {
	Exchange string  `json:"exchange"`
	MakerBps float64 `json:"maker_bps"`
	TakerBps float64 `json:"taker_bps"`
}

type ChartInstrument struct {
	ID         string                     `json:"id"`
	Instrument domain.InstrumentKey       `json:"instrument"`
	Symbols    map[domain.Exchange]string `json:"symbols"`
}

type ChartPoint struct {
	Time   int64   `json:"time"`
	Value  float64 `json:"value"`
	Closed bool    `json:"closed"`
}

type ChartSnapshot struct {
	Type        string                  `json:"type"`
	Instrument  ChartInstrument         `json:"instrument"`
	Interval    string                  `json:"interval"`
	Series      map[string][]ChartPoint `json:"series"`
	StoredGross []ChartPoint            `json:"stored_gross"`
	Errors      map[string]string       `json:"errors,omitempty"`
}

type ChartEvent struct {
	Type     string      `json:"type"`
	Exchange string      `json:"exchange,omitempty"`
	Status   string      `json:"status,omitempty"`
	Error    string      `json:"error,omitempty"`
	Point    *ChartPoint `json:"point,omitempty"`
}
