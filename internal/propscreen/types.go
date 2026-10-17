package propscreen

import (
	"time"

	"scalp-assist/internal/domain"
)

type Row struct {
	Instrument         domain.Instrument `json:"instrument"`
	SourceExchange     domain.Exchange   `json:"source_exchange"`
	SourceSymbol       string            `json:"source_symbol"`
	ContractMultiplier int               `json:"contract_multiplier"`
	ChangePct24h       float64           `json:"change_pct_24h"`
	TurnoverUSD        float64           `json:"turnover_usd"`
	BTCCorr7d          *float64          `json:"btc_corr_7d,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type ExchangeState struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Tickers   int       `json:"tickers"`
}

type Run struct {
	ObservedAt    time.Time                `json:"observed_at"`
	ExchangeState map[string]ExchangeState `json:"exchanges"`
	PropTotal     int                      `json:"prop_total"`
	Listed        int                      `json:"listed"`
	Rows          []Row                    `json:"rows"`
}
