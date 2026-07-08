package domain

import "time"

type Exchange string

const (
	ExchangeBinance Exchange = "binance"
	ExchangeBybit   Exchange = "bybit"
)

type Instrument struct {
	Base          string `json:"base"`
	Multiplier    int    `json:"multiplier"`
	BinanceSymbol string `json:"binance_symbol,omitempty"`
	BybitSymbol   string `json:"bybit_symbol,omitempty"`
	OnBinance     bool   `json:"on_binance"`
	OnBybit       bool   `json:"on_bybit"`
}

type Ticker struct {
	Symbol       string    `json:"symbol"`
	Exchange     Exchange  `json:"exchange"`
	Price        float64   `json:"price"`
	High24h      float64   `json:"high_24h"`
	Low24h       float64   `json:"low_24h"`
	ChangePct24h float64   `json:"change_pct_24h"`
	TurnoverUSD  float64   `json:"turnover_usd"`
	FundingRate  float64   `json:"funding_rate"`
	TickSize     float64   `json:"tick_size"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Kline struct {
	OpenTime time.Time `json:"open_time"`
	Close    float64   `json:"close"`
}

type MetricsRow struct {
	Instrument      Instrument `json:"instrument"`
	Rank            int        `json:"rank"`
	Price           float64    `json:"price"`
	TurnoverUSD     float64    `json:"turnover_usd"`
	DayRangePct     float64    `json:"day_range_pct"`
	ChangePct24h    float64    `json:"change_pct_24h"`
	FundingRate     float64    `json:"funding_rate"`
	RelativeTickPct float64    `json:"relative_tick_pct"`
	BTCCorr7d       *float64   `json:"btc_corr_7d,omitempty"`
	HighCorr        bool       `json:"high_corr"`
	IsInplay        bool       `json:"is_inplay"`
	InPropList      bool       `json:"in_prop_list"`
	BybitOnly       bool       `json:"bybit_only"`
	Score           float64    `json:"score"`
}

type Settings struct {
	MinVolumeUSD       float64        `json:"min_volume_usd" yaml:"min_volume_usd"`
	MinChangePct       float64        `json:"min_change_pct" yaml:"min_change_pct"`
	EnableChangeFilter bool           `json:"enable_change_filter" yaml:"enable_change_filter"`
	CorrThreshold      float64        `json:"corr_threshold" yaml:"corr_threshold"`
	Inplay             InplaySettings `json:"inplay" yaml:"inplay"`
	Weights            ScoreWeights   `json:"weights" yaml:"weights"`
	TopN               int            `json:"top_n" yaml:"top_n"`
}

type InplaySettings struct {
	MarketThresholdPct float64 `json:"market_threshold_pct" yaml:"market_threshold_pct"`
	Multiplier         float64 `json:"multiplier" yaml:"multiplier"`
}

type ScoreWeights struct {
	Volatility float64 `json:"volatility" yaml:"volatility"`
	Turnover   float64 `json:"turnover" yaml:"turnover"`
	Inplay     float64 `json:"inplay" yaml:"inplay"`
	Corr       float64 `json:"corr" yaml:"corr"`
}
