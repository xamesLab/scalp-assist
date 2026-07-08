package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
)

const DefaultBaseURL = "https://fapi.binance.com"

type Client struct {
	http *exchange.Client
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL)}
}

func (c *Client) ExchangeInfo(ctx context.Context) ([]domain.ContractSpec, map[string]string, error) {
	var resp exchangeInfoResponse
	meta, err := c.http.GetJSON(ctx, "/fapi/v1/exchangeInfo", nil, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}

	specs := make([]domain.ContractSpec, 0, len(resp.Symbols))
	for _, s := range resp.Symbols {
		specs = append(specs, domain.ContractSpec{
			Exchange:     domain.ExchangeBinance,
			Symbol:       s.Symbol,
			BaseAsset:    s.BaseAsset,
			QuoteAsset:   s.QuoteAsset,
			Status:       s.Status,
			ContractType: s.ContractType,
			TickSize:     priceFilterTickSize(s.Filters),
		})
	}
	return specs, exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"), nil
}

func (c *Client) Tickers24h(ctx context.Context) ([]domain.Ticker, map[string]string, error) {
	var resp []ticker24hResponse
	meta, err := c.http.GetJSON(ctx, "/fapi/v1/ticker/24hr", nil, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}

	tickers := make([]domain.Ticker, 0, len(resp))
	for _, t := range resp {
		price, err := parseFloat(t.LastPrice)
		if err != nil {
			return nil, nil, fmt.Errorf("parse lastPrice for %s: %w", t.Symbol, err)
		}
		high, err := parseFloat(t.HighPrice)
		if err != nil {
			return nil, nil, fmt.Errorf("parse highPrice for %s: %w", t.Symbol, err)
		}
		low, err := parseFloat(t.LowPrice)
		if err != nil {
			return nil, nil, fmt.Errorf("parse lowPrice for %s: %w", t.Symbol, err)
		}
		change, err := parseFloat(t.PriceChangePercent)
		if err != nil {
			return nil, nil, fmt.Errorf("parse priceChangePercent for %s: %w", t.Symbol, err)
		}
		turnover, err := parseFloat(t.QuoteVolume)
		if err != nil {
			return nil, nil, fmt.Errorf("parse quoteVolume for %s: %w", t.Symbol, err)
		}
		tickers = append(tickers, domain.Ticker{
			Symbol:       t.Symbol,
			SourceSymbol: t.Symbol,
			Exchange:     domain.ExchangeBinance,
			Price:        price,
			High24h:      high,
			Low24h:       low,
			ChangePct24h: change,
			TurnoverUSD:  turnover,
			UpdatedAt:    time.UnixMilli(t.CloseTime),
		})
	}
	return tickers, exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"), nil
}

func (c *Client) PremiumIndex(ctx context.Context) (map[string]float64, map[string]string, error) {
	var resp []premiumIndexResponse
	meta, err := c.http.GetJSON(ctx, "/fapi/v1/premiumIndex", nil, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}

	funding := make(map[string]float64, len(resp))
	for _, p := range resp {
		value, err := parseFloat(p.LastFundingRate)
		if err != nil {
			return nil, nil, fmt.Errorf("parse lastFundingRate for %s: %w", p.Symbol, err)
		}
		funding[p.Symbol] = value
	}
	return funding, exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"), nil
}

func (c *Client) Klines(ctx context.Context, symbol, interval string, limit int) ([]domain.Kline, map[string]string, error) {
	query := url.Values{}
	query.Set("symbol", symbol)
	query.Set("interval", interval)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	var resp [][]any
	meta, err := c.http.GetJSON(ctx, "/fapi/v1/klines", query, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}

	klines := make([]domain.Kline, 0, len(resp))
	for _, row := range resp {
		if len(row) < 5 {
			return nil, nil, fmt.Errorf("short kline row for %s", symbol)
		}
		openMS, err := numberToInt64(row[0])
		if err != nil {
			return nil, nil, fmt.Errorf("parse open time for %s: %w", symbol, err)
		}
		closePrice, err := anyToFloat(row[4])
		if err != nil {
			return nil, nil, fmt.Errorf("parse close for %s: %w", symbol, err)
		}
		klines = append(klines, domain.Kline{
			OpenTime: time.UnixMilli(openMS),
			Close:    closePrice,
		})
	}
	return klines, exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"), nil
}

func (c *Client) Snapshot(ctx context.Context) (domain.MarketSnapshot, error) {
	contracts, contractHeaders, err := c.ExchangeInfo(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	tickers, tickerHeaders, err := c.Tickers24h(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	funding, fundingHeaders, err := c.PremiumIndex(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}

	contractsBySymbol := make(map[string]domain.ContractSpec, len(contracts))
	for _, contract := range contracts {
		contractsBySymbol[contract.Symbol] = contract
	}
	for i := range tickers {
		if contract, ok := contractsBySymbol[tickers[i].Symbol]; ok {
			tickers[i].Status = contract.Status
			tickers[i].TickSize = contract.TickSize
			tickers[i].QuoteAsset = contract.QuoteAsset
		}
		tickers[i].FundingRate = funding[tickers[i].Symbol]
	}

	headers := map[string]string{}
	merge(headers, contractHeaders)
	merge(headers, tickerHeaders)
	merge(headers, fundingHeaders)
	return domain.MarketSnapshot{
		Exchange:   domain.ExchangeBinance,
		Contracts:  contracts,
		Tickers:    tickers,
		RateLimits: headers,
		FetchedAt:  time.Now().UTC(),
	}, nil
}

func merge(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func wrapErr(err error) error {
	if exErr, ok := err.(*exchange.ExchangeError); ok {
		exErr.Exchange = string(domain.ExchangeBinance)
		return exErr
	}
	return err
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol       string           `json:"symbol"`
		Status       string           `json:"status"`
		BaseAsset    string           `json:"baseAsset"`
		QuoteAsset   string           `json:"quoteAsset"`
		ContractType string           `json:"contractType"`
		Filters      []map[string]any `json:"filters"`
	} `json:"symbols"`
}

type ticker24hResponse struct {
	Symbol             string `json:"symbol"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	QuoteVolume        string `json:"quoteVolume"`
	CloseTime          int64  `json:"closeTime"`
}

type premiumIndexResponse struct {
	Symbol          string `json:"symbol"`
	LastFundingRate string `json:"lastFundingRate"`
}

func priceFilterTickSize(filters []map[string]any) float64 {
	for _, filter := range filters {
		if filter["filterType"] == "PRICE_FILTER" {
			tick, _ := anyToFloat(filter["tickSize"])
			return tick
		}
	}
	return 0
}

func parseFloat(raw string) (float64, error) {
	return strconv.ParseFloat(raw, 64)
}

func anyToFloat(value any) (float64, error) {
	switch v := value.(type) {
	case string:
		return strconv.ParseFloat(v, 64)
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("unsupported number type %T", value)
	}
}

func numberToInt64(value any) (int64, error) {
	switch v := value.(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}

var _ = http.MethodGet
