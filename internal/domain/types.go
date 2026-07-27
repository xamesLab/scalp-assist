package domain

import "time"

type Exchange string

const (
	ExchangeBinance Exchange = "binance"
	ExchangeBybit   Exchange = "bybit"
	ExchangeOKX     Exchange = "okx"
)

type InstrumentKey struct {
	Base       string `json:"base"`
	Quote      string `json:"quote"`
	Multiplier int    `json:"multiplier"`
	Kind       string `json:"kind"`
}

type Instrument struct {
	Base          string `json:"base"`
	Multiplier    int    `json:"multiplier"`
	BinanceSymbol string `json:"binance_symbol,omitempty"`
	BybitSymbol   string `json:"bybit_symbol,omitempty"`
	OnBinance     bool   `json:"on_binance"`
	OnBybit       bool   `json:"on_bybit"`
}

type ContractSpec struct {
	Exchange      Exchange `json:"exchange"`
	Symbol        string   `json:"symbol"`
	BaseAsset     string   `json:"base_asset"`
	QuoteAsset    string   `json:"quote_asset"`
	Status        string   `json:"status"`
	ContractType  string   `json:"contract_type"`
	TickSize      float64  `json:"tick_size"`
	ContractValue float64  `json:"contract_value,omitempty"`
	QuantityStep  float64  `json:"quantity_step,omitempty"`
}

type Ticker struct {
	Symbol          string        `json:"symbol"`
	SourceSymbol    string        `json:"source_symbol"`
	Exchange        Exchange      `json:"exchange"`
	QuoteAsset      string        `json:"quote_asset"`
	Price           float64       `json:"price"`
	High24h         float64       `json:"high_24h"`
	Low24h          float64       `json:"low_24h"`
	ChangePct24h    float64       `json:"change_pct_24h"`
	TurnoverUSD     float64       `json:"turnover_usd"`
	FundingRate     float64       `json:"funding_rate"`
	FundingKnown    bool          `json:"funding_known,omitempty"`
	OpenInterestUSD float64       `json:"open_interest_usd"`
	TickSize        float64       `json:"tick_size"`
	BidPrice        float64       `json:"bid_price,omitempty"`
	AskPrice        float64       `json:"ask_price,omitempty"`
	BidQuantity     float64       `json:"bid_quantity,omitempty"`
	AskQuantity     float64       `json:"ask_quantity,omitempty"`
	MarkPrice       float64       `json:"mark_price,omitempty"`
	NextFundingTime time.Time     `json:"next_funding_time,omitempty"`
	FundingInterval time.Duration `json:"funding_interval,omitempty"`
	Status          string        `json:"status"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type VenueQuote struct {
	Exchange           Exchange      `json:"exchange"`
	SourceSymbol       string        `json:"source_symbol"`
	Contract           ContractSpec  `json:"contract"`
	BidPrice           float64       `json:"bid_price"`
	AskPrice           float64       `json:"ask_price"`
	BidQuantity        float64       `json:"bid_quantity"`
	AskQuantity        float64       `json:"ask_quantity"`
	QuantityMultiplier float64       `json:"quantity_multiplier"`
	LastPrice          float64       `json:"last_price,omitempty"`
	MarkPrice          float64       `json:"mark_price,omitempty"`
	TurnoverUSD        float64       `json:"turnover_usd,omitempty"`
	FundingRate        *float64      `json:"funding_rate,omitempty"`
	FundingInterval    time.Duration `json:"funding_interval,omitempty"`
	NextFundingTime    time.Time     `json:"next_funding_time,omitempty"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

type FundingInfo struct {
	Rate            float64
	MarkPrice       float64
	Interval        time.Duration
	NextFundingTime time.Time
	UpdatedAt       time.Time
}

type Kline struct {
	OpenTime time.Time `json:"open_time"`
	Close    float64   `json:"close"`
}

type MarketSnapshot struct {
	Exchange   Exchange          `json:"exchange"`
	Contracts  []ContractSpec    `json:"contracts"`
	Tickers    []Ticker          `json:"tickers"`
	RateLimits map[string]string `json:"rate_limits"`
	FetchedAt  time.Time         `json:"fetched_at"`
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
