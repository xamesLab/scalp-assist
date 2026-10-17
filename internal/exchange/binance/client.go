package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
	"scalp-assist/internal/marketdata"
)

const DefaultBaseURL = "https://fapi.binance.com"
const DefaultWSURL = "wss://fstream.binance.com/market/ws"

type Client struct {
	http           *exchange.Client
	wsURL          string
	contractsCache marketdata.Cache[contractsResult]
	statsCache     marketdata.Cache[tickersResult]
	fundingCache   marketdata.Cache[fundingResult]
	bookCache      marketdata.Cache[bookResult]
}

type binanceKlineEvent struct {
	Kline struct {
		OpenTime int64  `json:"t"`
		Symbol   string `json:"s"`
		Open     string `json:"o"`
		High     string `json:"h"`
		Low      string `json:"l"`
		Close    string `json:"c"`
		Closed   bool   `json:"x"`
	} `json:"k"`
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL), wsURL: DefaultWSURL}
}

func (c *Client) Exchange() domain.Exchange {
	return domain.ExchangeBinance
}

func (c *Client) ExchangeInfo(ctx context.Context) ([]domain.ContractSpec, map[string]string, error) {
	result, err := c.contractsCache.Get(ctx, 6*time.Hour, func(ctx context.Context) (contractsResult, error) {
		var resp exchangeInfoResponse
		meta, err := c.http.GetJSON(ctx, "/fapi/v1/exchangeInfo", nil, &resp)
		if err != nil {
			return contractsResult{}, wrapErr(err)
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
		return contractsResult{
			contracts: specs,
			headers:   exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"),
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return append([]domain.ContractSpec(nil), result.contracts...), cloneMap(result.headers), nil
}

func (c *Client) Tickers24h(ctx context.Context) ([]domain.Ticker, map[string]string, error) {
	result, err := c.statsCache.Get(ctx, time.Minute, func(ctx context.Context) (tickersResult, error) {
		var resp []ticker24hResponse
		meta, err := c.http.GetJSON(ctx, "/fapi/v1/ticker/24hr", nil, &resp)
		if err != nil {
			return tickersResult{}, wrapErr(err)
		}

		tickers := make([]domain.Ticker, 0, len(resp))
		for _, t := range resp {
			price, err := parseFloat(t.LastPrice)
			if err != nil {
				return tickersResult{}, fmt.Errorf("parse lastPrice for %s: %w", t.Symbol, err)
			}
			high, err := parseFloat(t.HighPrice)
			if err != nil {
				return tickersResult{}, fmt.Errorf("parse highPrice for %s: %w", t.Symbol, err)
			}
			low, err := parseFloat(t.LowPrice)
			if err != nil {
				return tickersResult{}, fmt.Errorf("parse lowPrice for %s: %w", t.Symbol, err)
			}
			change, err := parseFloat(t.PriceChangePercent)
			if err != nil {
				return tickersResult{}, fmt.Errorf("parse priceChangePercent for %s: %w", t.Symbol, err)
			}
			turnover, err := parseFloat(t.QuoteVolume)
			if err != nil {
				return tickersResult{}, fmt.Errorf("parse quoteVolume for %s: %w", t.Symbol, err)
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
		return tickersResult{
			tickers: tickers,
			headers: exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"),
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return append([]domain.Ticker(nil), result.tickers...), cloneMap(result.headers), nil
}

func (c *Client) PremiumIndex(ctx context.Context) (map[string]float64, map[string]string, error) {
	funding, headers, err := c.Funding(ctx)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]float64, len(funding))
	for symbol, info := range funding {
		values[symbol] = info.Rate
	}
	return values, headers, nil
}

func (c *Client) Funding(ctx context.Context) (map[string]domain.FundingInfo, map[string]string, error) {
	result, err := c.fundingCache.Get(ctx, time.Minute, func(ctx context.Context) (fundingResult, error) {
		var resp []premiumIndexResponse
		meta, err := c.http.GetJSON(ctx, "/fapi/v1/premiumIndex", nil, &resp)
		if err != nil {
			return fundingResult{}, wrapErr(err)
		}
		values := make(map[string]domain.FundingInfo, len(resp))
		for _, p := range resp {
			value, err := parseFloat(p.LastFundingRate)
			if err != nil {
				return fundingResult{}, fmt.Errorf("parse lastFundingRate for %s: %w", p.Symbol, err)
			}
			var markPrice float64
			if p.MarkPrice != "" {
				markPrice, err = parseFloat(p.MarkPrice)
				if err != nil {
					return fundingResult{}, fmt.Errorf("parse markPrice for %s: %w", p.Symbol, err)
				}
			}
			values[p.Symbol] = domain.FundingInfo{
				Rate:            value,
				MarkPrice:       markPrice,
				Interval:        8 * time.Hour,
				NextFundingTime: time.UnixMilli(p.NextFundingTime),
				UpdatedAt:       time.UnixMilli(p.Time),
			}
		}
		return fundingResult{
			funding: values,
			headers: exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"),
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return cloneFunding(result.funding), cloneMap(result.headers), nil
}

func (c *Client) BookTickers(ctx context.Context) ([]domain.VenueQuote, map[string]string, error) {
	result, err := c.bookCache.Get(ctx, 2*time.Second, func(ctx context.Context) (bookResult, error) {
		var resp []bookTickerResponse
		meta, err := c.http.GetJSON(ctx, "/fapi/v1/ticker/bookTicker", nil, &resp)
		if err != nil {
			return bookResult{}, wrapErr(err)
		}
		quotes := make([]domain.VenueQuote, 0, len(resp))
		for _, item := range resp {
			bid, err := parseFloat(item.BidPrice)
			if err != nil {
				return bookResult{}, fmt.Errorf("parse bidPrice for %s: %w", item.Symbol, err)
			}
			ask, err := parseFloat(item.AskPrice)
			if err != nil {
				return bookResult{}, fmt.Errorf("parse askPrice for %s: %w", item.Symbol, err)
			}
			bidQty, err := parseFloat(item.BidQty)
			if err != nil {
				return bookResult{}, fmt.Errorf("parse bidQty for %s: %w", item.Symbol, err)
			}
			askQty, err := parseFloat(item.AskQty)
			if err != nil {
				return bookResult{}, fmt.Errorf("parse askQty for %s: %w", item.Symbol, err)
			}
			quotes = append(quotes, domain.VenueQuote{
				Exchange:           domain.ExchangeBinance,
				SourceSymbol:       item.Symbol,
				BidPrice:           bid,
				AskPrice:           ask,
				BidQuantity:        bidQty,
				AskQuantity:        askQty,
				QuantityMultiplier: 1,
				UpdatedAt:          time.UnixMilli(item.Time),
			})
		}
		return bookResult{
			quotes:  quotes,
			headers: exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"),
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return append([]domain.VenueQuote(nil), result.quotes...), cloneMap(result.headers), nil
}

func (c *Client) Quotes(ctx context.Context) ([]domain.VenueQuote, error) {
	var contracts []domain.ContractSpec
	var books []domain.VenueQuote
	var funding map[string]domain.FundingInfo
	var contractErr, bookErr, fundingErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		contracts, _, contractErr = c.ExchangeInfo(ctx)
	}()
	go func() {
		defer wg.Done()
		books, _, bookErr = c.BookTickers(ctx)
	}()
	go func() {
		defer wg.Done()
		funding, _, fundingErr = c.Funding(ctx)
	}()
	wg.Wait()
	if contractErr != nil {
		return nil, contractErr
	}
	if bookErr != nil {
		return nil, bookErr
	}
	if fundingErr != nil {
		return nil, fundingErr
	}

	bySymbol := make(map[string]domain.ContractSpec, len(contracts))
	for _, contract := range contracts {
		bySymbol[contract.Symbol] = contract
	}
	out := make([]domain.VenueQuote, 0, len(books))
	for _, quote := range books {
		contract, ok := bySymbol[quote.SourceSymbol]
		if !ok {
			continue
		}
		quote.Contract = contract
		if info, ok := funding[quote.SourceSymbol]; ok {
			rate := info.Rate
			quote.FundingRate = &rate
			quote.FundingInterval = info.Interval
			quote.NextFundingTime = info.NextFundingTime
			quote.MarkPrice = info.MarkPrice
		}
		out = append(out, quote)
	}
	return out, nil
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
		openPrice, err := anyToFloat(row[1])
		if err != nil {
			return nil, nil, fmt.Errorf("parse open for %s: %w", symbol, err)
		}
		highPrice, err := anyToFloat(row[2])
		if err != nil {
			return nil, nil, fmt.Errorf("parse high for %s: %w", symbol, err)
		}
		lowPrice, err := anyToFloat(row[3])
		if err != nil {
			return nil, nil, fmt.Errorf("parse low for %s: %w", symbol, err)
		}
		closePrice, err := anyToFloat(row[4])
		if err != nil {
			return nil, nil, fmt.Errorf("parse close for %s: %w", symbol, err)
		}
		klines = append(klines, domain.Kline{
			OpenTime: time.UnixMilli(openMS),
			Open:     openPrice,
			High:     highPrice,
			Low:      lowPrice,
			Close:    closePrice,
		})
	}
	return klines, exchange.HeaderSubset(meta.Headers, "X-MBX-USED-WEIGHT-1M"), nil
}

func (c *Client) StreamKlines(ctx context.Context, symbol, interval string, out chan<- domain.Kline) error {
	if interval != "1m" && interval != "5m" && interval != "1h" {
		return fmt.Errorf("unsupported Binance kline interval %q", interval)
	}
	conn, _, err := websocket.Dial(ctx, c.wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect Binance websocket: %w", err)
	}
	defer conn.CloseNow()

	topic := strings.ToLower(symbol) + "@kline_" + interval
	if err := wsjson.Write(ctx, conn, map[string]any{
		"method": "SUBSCRIBE",
		"params": []string{topic},
		"id":     1,
	}); err != nil {
		return fmt.Errorf("subscribe Binance %s: %w", topic, err)
	}

	for {
		var event binanceKlineEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return err
		}
		if event.Kline.Symbol == "" || event.Kline.OpenTime == 0 {
			continue
		}
		closePrice, err := parseFloat(event.Kline.Close)
		if err != nil {
			return fmt.Errorf("parse Binance websocket close for %s: %w", symbol, err)
		}
		openPrice, highPrice, lowPrice := closePrice, closePrice, closePrice
		if event.Kline.Open != "" {
			if openPrice, err = parseFloat(event.Kline.Open); err != nil {
				return fmt.Errorf("parse Binance websocket open for %s: %w", symbol, err)
			}
		}
		if event.Kline.High != "" {
			if highPrice, err = parseFloat(event.Kline.High); err != nil {
				return fmt.Errorf("parse Binance websocket high for %s: %w", symbol, err)
			}
		}
		if event.Kline.Low != "" {
			if lowPrice, err = parseFloat(event.Kline.Low); err != nil {
				return fmt.Errorf("parse Binance websocket low for %s: %w", symbol, err)
			}
		}
		point := domain.Kline{
			OpenTime: time.UnixMilli(event.Kline.OpenTime),
			Open:     openPrice,
			High:     highPrice,
			Low:      lowPrice,
			Close:    closePrice,
			Closed:   event.Kline.Closed,
		}
		select {
		case out <- point:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) MarketSnapshot(ctx context.Context) (domain.MarketSnapshot, error) {
	contracts, contractHeaders, err := c.ExchangeInfo(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	tickers, tickerHeaders, err := c.Tickers24h(ctx)
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
		Exchange:   domain.ExchangeBinance,
		Contracts:  contracts,
		Tickers:    tickers,
		RateLimits: headers,
		FetchedAt:  time.Now().UTC(),
	}, nil
}

func (c *Client) Snapshot(ctx context.Context) (domain.MarketSnapshot, error) {
	snapshot, err := c.MarketSnapshot(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	funding, fundingHeaders, err := c.Funding(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}
	for i := range snapshot.Tickers {
		if info, ok := funding[snapshot.Tickers[i].Symbol]; ok {
			snapshot.Tickers[i].FundingRate = info.Rate
			snapshot.Tickers[i].FundingKnown = true
			snapshot.Tickers[i].NextFundingTime = info.NextFundingTime
		}
	}
	merge(snapshot.RateLimits, fundingHeaders)
	return snapshot, nil
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
	MarkPrice       string `json:"markPrice"`
	NextFundingTime int64  `json:"nextFundingTime"`
	Time            int64  `json:"time"`
}

type bookTickerResponse struct {
	Symbol   string `json:"symbol"`
	BidPrice string `json:"bidPrice"`
	BidQty   string `json:"bidQty"`
	AskPrice string `json:"askPrice"`
	AskQty   string `json:"askQty"`
	Time     int64  `json:"time"`
}

type contractsResult struct {
	contracts []domain.ContractSpec
	headers   map[string]string
}

type tickersResult struct {
	tickers []domain.Ticker
	headers map[string]string
}

type fundingResult struct {
	funding map[string]domain.FundingInfo
	headers map[string]string
}

type bookResult struct {
	quotes  []domain.VenueQuote
	headers map[string]string
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

func cloneMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneFunding(src map[string]domain.FundingInfo) map[string]domain.FundingInfo {
	out := make(map[string]domain.FundingInfo, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

var _ = http.MethodGet
