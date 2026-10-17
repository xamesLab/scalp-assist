package bybit

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
	"scalp-assist/internal/marketdata"
)

const DefaultBaseURL = "https://api.bybit.com"
const DefaultWSURL = "wss://stream.bybit.com/v5/public/linear"

type Client struct {
	http           *exchange.Client
	wsURL          string
	contractsCache marketdata.Cache[contractsResult]
	tickersCache   marketdata.Cache[tickerResponseResult]
}

type bybitKlineEvent struct {
	Topic string `json:"topic"`
	Data  []struct {
		Start   int64  `json:"start"`
		Open    string `json:"open"`
		High    string `json:"high"`
		Low     string `json:"low"`
		Close   string `json:"close"`
		Confirm bool   `json:"confirm"`
	} `json:"data"`
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL), wsURL: DefaultWSURL}
}

func (c *Client) Exchange() domain.Exchange {
	return domain.ExchangeBybit
}

func (c *Client) InstrumentsInfo(ctx context.Context) ([]domain.ContractSpec, map[string]string, error) {
	result, err := c.contractsCache.Get(ctx, 6*time.Hour, func(ctx context.Context) (contractsResult, error) {
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
				return contractsResult{}, wrapErr(err)
			}
			if err := checkRetCode(resp.RetCode, resp.RetMsg); err != nil {
				return contractsResult{}, err
			}
			merge(headers, exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"))

			for _, item := range resp.Result.List {
				tickSize, err := parseFloat(item.PriceFilter.TickSize)
				if err != nil {
					return contractsResult{}, fmt.Errorf("parse tickSize for %s: %w", item.Symbol, err)
				}
				quantityStep, err := parseFloat(item.LotSizeFilter.QtyStep)
				if err != nil {
					return contractsResult{}, fmt.Errorf("parse qtyStep for %s: %w", item.Symbol, err)
				}
				all = append(all, domain.ContractSpec{
					Exchange:     domain.ExchangeBybit,
					Symbol:       item.Symbol,
					BaseAsset:    item.BaseCoin,
					QuoteAsset:   item.QuoteCoin,
					Status:       item.Status,
					ContractType: item.ContractType,
					TickSize:     tickSize,
					QuantityStep: quantityStep,
				})
			}
			cursor = resp.Result.NextPageCursor
			if cursor == "" {
				break
			}
		}
		return contractsResult{contracts: all, headers: headers}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return append([]domain.ContractSpec(nil), result.contracts...), cloneMap(result.headers), nil
}

func (c *Client) Tickers(ctx context.Context) ([]domain.Ticker, map[string]string, error) {
	result, err := c.tickersCache.Get(ctx, 2*time.Second, func(ctx context.Context) (tickerResponseResult, error) {
		query := url.Values{}
		query.Set("category", "linear")
		var resp tickersResponse
		meta, err := c.http.GetJSON(ctx, "/v5/market/tickers", query, &resp)
		if err != nil {
			return tickerResponseResult{}, wrapErr(err)
		}
		if err := checkRetCode(resp.RetCode, resp.RetMsg); err != nil {
			return tickerResponseResult{}, err
		}
		return tickerResponseResult{
			response: resp,
			headers:  exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"),
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}

	tickers := make([]domain.Ticker, 0, len(result.response.Result.List))
	for _, item := range result.response.Result.List {
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
		bidPrice, _ := parseFloat(item.Bid1Price)
		askPrice, _ := parseFloat(item.Ask1Price)
		bidQty, _ := parseFloat(item.Bid1Size)
		askQty, _ := parseFloat(item.Ask1Size)
		markPrice, _ := parseFloat(item.MarkPrice)
		nextFundingMS, _ := strconv.ParseInt(item.NextFundingTime, 10, 64)
		fundingHours, _ := strconv.ParseFloat(item.FundingIntervalHour, 64)
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
			FundingKnown:    item.FundingRate != "",
			OpenInterestUSD: oi,
			BidPrice:        bidPrice,
			AskPrice:        askPrice,
			BidQuantity:     bidQty,
			AskQuantity:     askQty,
			MarkPrice:       markPrice,
			NextFundingTime: time.UnixMilli(nextFundingMS),
			FundingInterval: time.Duration(fundingHours * float64(time.Hour)),
			UpdatedAt:       time.UnixMilli(result.response.Time),
		})
	}
	return tickers, cloneMap(result.headers), nil
}

func (c *Client) Quotes(ctx context.Context) ([]domain.VenueQuote, error) {
	contracts, _, err := c.InstrumentsInfo(ctx)
	if err != nil {
		return nil, err
	}
	tickers, _, err := c.Tickers(ctx)
	if err != nil {
		return nil, err
	}
	bySymbol := make(map[string]domain.ContractSpec, len(contracts))
	for _, contract := range contracts {
		bySymbol[contract.Symbol] = contract
	}
	out := make([]domain.VenueQuote, 0, len(tickers))
	for _, ticker := range tickers {
		contract, ok := bySymbol[ticker.SourceSymbol]
		if !ok {
			continue
		}
		quote := domain.VenueQuote{
			Exchange:           domain.ExchangeBybit,
			SourceSymbol:       ticker.SourceSymbol,
			Contract:           contract,
			BidPrice:           ticker.BidPrice,
			AskPrice:           ticker.AskPrice,
			BidQuantity:        ticker.BidQuantity,
			AskQuantity:        ticker.AskQuantity,
			QuantityMultiplier: 1,
			LastPrice:          ticker.Price,
			MarkPrice:          ticker.MarkPrice,
			TurnoverUSD:        ticker.TurnoverUSD,
			FundingInterval:    ticker.FundingInterval,
			NextFundingTime:    ticker.NextFundingTime,
			UpdatedAt:          ticker.UpdatedAt,
		}
		if ticker.FundingKnown {
			rate := ticker.FundingRate
			quote.FundingRate = &rate
		}
		out = append(out, quote)
	}
	return out, nil
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
		openPrice, err := parseFloat(row[1])
		if err != nil {
			return nil, nil, fmt.Errorf("parse open for %s: %w", symbol, err)
		}
		highPrice, err := parseFloat(row[2])
		if err != nil {
			return nil, nil, fmt.Errorf("parse high for %s: %w", symbol, err)
		}
		lowPrice, err := parseFloat(row[3])
		if err != nil {
			return nil, nil, fmt.Errorf("parse low for %s: %w", symbol, err)
		}
		closePrice, err := parseFloat(row[4])
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
	sort.Slice(klines, func(i, j int) bool {
		return klines[i].OpenTime.Before(klines[j].OpenTime)
	})
	return klines, exchange.HeaderSubset(meta.Headers, "X-Bapi-Limit", "X-Bapi-Limit-Status", "X-Bapi-Limit-Reset-Timestamp"), nil
}

func (c *Client) StreamKlines(ctx context.Context, symbol, interval string, out chan<- domain.Kline) error {
	wireInterval := ""
	switch interval {
	case "1m":
		wireInterval = "1"
	case "5m":
		wireInterval = "5"
	case "1h":
		wireInterval = "60"
	default:
		return fmt.Errorf("unsupported Bybit kline interval %q", interval)
	}

	conn, _, err := websocket.Dial(ctx, c.wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect Bybit websocket: %w", err)
	}
	defer conn.CloseNow()

	topic := "kline." + wireInterval + "." + symbol
	if err := wsjson.Write(ctx, conn, map[string]any{
		"op":   "subscribe",
		"args": []string{topic},
	}); err != nil {
		return fmt.Errorf("subscribe Bybit %s: %w", topic, err)
	}

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				_ = wsjson.Write(pingCtx, conn, map[string]string{"op": "ping"})
			}
		}
	}()

	for {
		var event bybitKlineEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return err
		}
		if event.Topic != topic {
			continue
		}
		for _, item := range event.Data {
			closePrice, err := parseFloat(item.Close)
			if err != nil {
				return fmt.Errorf("parse Bybit websocket close for %s: %w", symbol, err)
			}
			openPrice, highPrice, lowPrice := closePrice, closePrice, closePrice
			if item.Open != "" {
				if openPrice, err = parseFloat(item.Open); err != nil {
					return fmt.Errorf("parse Bybit websocket open for %s: %w", symbol, err)
				}
			}
			if item.High != "" {
				if highPrice, err = parseFloat(item.High); err != nil {
					return fmt.Errorf("parse Bybit websocket high for %s: %w", symbol, err)
				}
			}
			if item.Low != "" {
				if lowPrice, err = parseFloat(item.Low); err != nil {
					return fmt.Errorf("parse Bybit websocket low for %s: %w", symbol, err)
				}
			}
			point := domain.Kline{
				OpenTime: time.UnixMilli(item.Start),
				Open:     openPrice,
				High:     highPrice,
				Low:      lowPrice,
				Close:    closePrice,
				Closed:   item.Confirm,
			}
			select {
			case out <- point:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (c *Client) MarketSnapshot(ctx context.Context) (domain.MarketSnapshot, error) {
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

func (c *Client) Snapshot(ctx context.Context) (domain.MarketSnapshot, error) {
	return c.MarketSnapshot(ctx)
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
			LotSizeFilter struct {
				QtyStep string `json:"qtyStep"`
			} `json:"lotSizeFilter"`
		} `json:"list"`
	} `json:"result"`
}

type tickersResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Time    int64  `json:"time"`
	Result  struct {
		List []struct {
			Symbol              string `json:"symbol"`
			LastPrice           string `json:"lastPrice"`
			HighPrice24h        string `json:"highPrice24h"`
			LowPrice24h         string `json:"lowPrice24h"`
			Price24hPcnt        string `json:"price24hPcnt"`
			Turnover24h         string `json:"turnover24h"`
			FundingRate         string `json:"fundingRate"`
			OpenInterestValue   string `json:"openInterestValue"`
			MarkPrice           string `json:"markPrice"`
			Bid1Price           string `json:"bid1Price"`
			Ask1Price           string `json:"ask1Price"`
			Bid1Size            string `json:"bid1Size"`
			Ask1Size            string `json:"ask1Size"`
			NextFundingTime     string `json:"nextFundingTime"`
			FundingIntervalHour string `json:"fundingIntervalHour"`
		} `json:"list"`
	} `json:"result"`
}

type contractsResult struct {
	contracts []domain.ContractSpec
	headers   map[string]string
}

type tickerResponseResult struct {
	response tickersResponse
	headers  map[string]string
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

func cloneMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
