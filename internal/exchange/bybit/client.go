package bybit

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
)

const DefaultBaseURL = "https://api.bybit.com"

type Client struct {
	http *exchange.Client
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL)}
}

func (c *Client) InstrumentsInfo(ctx context.Context) ([]domain.ContractSpec, map[string]string, error) {
	var all []domain.ContractSpec
	headers := map[string]string{}
	cursor := ""

	for {
		query := url.Values{}
		query.Set("category", "linear")
		query.Set("limit", "1000")
		if cursor != "" {
			query.Set("cursor", cursor)
		}

		var resp instrumentsResponse
		meta, err := c.http.GetJSON(ctx, "/v5/market/instruments-info", query, &resp)
		if err != nil {
			return nil, nil, wrapErr(err)
		}
		if err := checkRetCode(resp.RetCode, resp.RetMsg); err != nil {
			return nil, nil, err
		}
		merge(headers, exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"))

		for _, item := range resp.Result.List {
			tickSize, err := parseFloat(item.PriceFilter.TickSize)
			if err != nil {
				return nil, nil, fmt.Errorf("parse tickSize for %s: %w", item.Symbol, err)
			}
			all = append(all, domain.ContractSpec{
				Exchange:     domain.ExchangeBybit,
				Symbol:       item.Symbol,
				BaseAsset:    item.BaseCoin,
				QuoteAsset:   item.QuoteCoin,
				Status:       item.Status,
				ContractType: item.ContractType,
				TickSize:     tickSize,
			})
		}
		cursor = resp.Result.NextPageCursor
		if cursor == "" {
			break
		}
	}

	return all, headers, nil
}

func (c *Client) Tickers(ctx context.Context) ([]domain.Ticker, map[string]string, error) {
	query := url.Values{}
	query.Set("category", "linear")

	var resp tickersResponse
	meta, err := c.http.GetJSON(ctx, "/v5/market/tickers", query, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}
	if err := checkRetCode(resp.RetCode, resp.RetMsg); err != nil {
		return nil, nil, err
	}

	tickers := make([]domain.Ticker, 0, len(resp.Result.List))
	for _, item := range resp.Result.List {
		price, err := parseFloat(item.LastPrice)
		if err != nil {
			return nil, nil, fmt.Errorf("parse lastPrice for %s: %w", item.Symbol, err)
		}
		high, err := parseFloat(item.HighPrice24h)
		if err != nil {
			return nil, nil, fmt.Errorf("parse highPrice24h for %s: %w", item.Symbol, err)
		}
		low, err := parseFloat(item.LowPrice24h)
		if err != nil {
			return nil, nil, fmt.Errorf("parse lowPrice24h for %s: %w", item.Symbol, err)
		}
		change, err := parseFloat(item.Price24hPcnt)
		if err != nil {
			return nil, nil, fmt.Errorf("parse price24hPcnt for %s: %w", item.Symbol, err)
		}
		turnover, err := parseFloat(item.Turnover24h)
		if err != nil {
			return nil, nil, fmt.Errorf("parse turnover24h for %s: %w", item.Symbol, err)
		}
		funding, err := parseFloat(item.FundingRate)
		if err != nil {
			return nil, nil, fmt.Errorf("parse fundingRate for %s: %w", item.Symbol, err)
		}
		oi, _ := parseFloat(item.OpenInterestValue)
		tickers = append(tickers, domain.Ticker{
			Symbol:          item.Symbol,
			SourceSymbol:    item.Symbol,
			Exchange:        domain.ExchangeBybit,
			Price:           price,
			High24h:         high,
			Low24h:          low,
			ChangePct24h:    change * 100,
			TurnoverUSD:     turnover,
			FundingRate:     funding,
			OpenInterestUSD: oi,
		})
	}
	return tickers, exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"), nil
}

func (c *Client) Klines(ctx context.Context, symbol, interval string, limit int) ([]domain.Kline, map[string]string, error) {
	query := url.Values{}
	query.Set("category", "linear")
	query.Set("symbol", symbol)
	query.Set("interval", interval)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	var resp klineResponse
	meta, err := c.http.GetJSON(ctx, "/v5/market/kline", query, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}
	if err := checkRetCode(resp.RetCode, resp.RetMsg); err != nil {
		return nil, nil, err
	}

	klines := make([]domain.Kline, 0, len(resp.Result.List))
	for _, row := range resp.Result.List {
		if len(row) < 5 {
			return nil, nil, fmt.Errorf("short kline row for %s", symbol)
		}
		openMS, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("parse open time for %s: %w", symbol, err)
		}
		closePrice, err := parseFloat(row[4])
		if err != nil {
			return nil, nil, fmt.Errorf("parse close for %s: %w", symbol, err)
		}
		klines = append(klines, domain.Kline{
			OpenTime: time.UnixMilli(openMS),
			Close:    closePrice,
		})
	}
	sort.Slice(klines, func(i, j int) bool {
		return klines[i].OpenTime.Before(klines[j].OpenTime)
	})
	return klines, exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"), nil
}

func (c *Client) Snapshot(ctx context.Context) (domain.MarketSnapshot, error) {
	contracts, contractHeaders, err := c.InstrumentsInfo(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	tickers, tickerHeaders, err := c.Tickers(ctx)
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
	}

	headers := map[string]string{}
	merge(headers, contractHeaders)
	merge(headers, tickerHeaders)
	return domain.MarketSnapshot{
		Exchange:   domain.ExchangeBybit,
		Contracts:  contracts,
		Tickers:    tickers,
		RateLimits: headers,
		FetchedAt:  time.Now().UTC(),
	}, nil
}

func checkRetCode(code int, msg string) error {
	if code == 0 {
		return nil
	}
	return &exchange.ExchangeError{
		Exchange: string(domain.ExchangeBybit),
		Code:     strconv.Itoa(code),
		Message:  msg,
	}
}

func wrapErr(err error) error {
	if exErr, ok := err.(*exchange.ExchangeError); ok {
		exErr.Exchange = string(domain.ExchangeBybit)
		return exErr
	}
	return err
}

func merge(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

type instrumentsResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		NextPageCursor string `json:"nextPageCursor"`
		List           []struct {
			Symbol       string `json:"symbol"`
			ContractType string `json:"contractType"`
			Status       string `json:"status"`
			BaseCoin     string `json:"baseCoin"`
			QuoteCoin    string `json:"quoteCoin"`
			PriceFilter  struct {
				TickSize string `json:"tickSize"`
			} `json:"priceFilter"`
		} `json:"list"`
	} `json:"result"`
}

type tickersResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		List []struct {
			Symbol            string `json:"symbol"`
			LastPrice         string `json:"lastPrice"`
			HighPrice24h      string `json:"highPrice24h"`
			LowPrice24h       string `json:"lowPrice24h"`
			Price24hPcnt      string `json:"price24hPcnt"`
			Turnover24h       string `json:"turnover24h"`
			FundingRate       string `json:"fundingRate"`
			OpenInterestValue string `json:"openInterestValue"`
		} `json:"list"`
	} `json:"result"`
}

type klineResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		List [][]string `json:"list"`
	} `json:"result"`
}

func parseFloat(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}
