package okx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
	"scalp-assist/internal/marketdata"
)

const DefaultBaseURL = "https://www.okx.com"
const DefaultWSURL = "wss://ws.okx.com:8443/ws/v5/business"

type Client struct {
	http           *exchange.Client
	wsURL          string
	contractsCache marketdata.Cache[[]domain.ContractSpec]
	tickersCache   marketdata.Cache[tickersResponse]
	fundingCache   marketdata.KeyedCache[string, domain.FundingInfo]
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL), wsURL: DefaultWSURL}
}

func (c *Client) Exchange() domain.Exchange {
	return domain.ExchangeOKX
}

func (c *Client) Instruments(ctx context.Context) ([]domain.ContractSpec, error) {
	contracts, err := c.contractsCache.Get(ctx, 6*time.Hour, func(ctx context.Context) ([]domain.ContractSpec, error) {
		query := url.Values{"instType": {"SWAP"}}
		var resp instrumentsResponse
		if _, err := c.http.GetJSON(ctx, "/api/v5/public/instruments", query, &resp); err != nil {
			return nil, wrapErr(err)
		}
		if err := checkCode(resp.Code, resp.Msg); err != nil {
			return nil, err
		}
		out := make([]domain.ContractSpec, 0, len(resp.Data))
		for _, item := range resp.Data {
			base, quote := instrumentCurrencies(item.InstID, item.BaseCcy, item.QuoteCcy)
			tickSize, err := parseFloat(item.TickSz)
			if err != nil {
				return nil, fmt.Errorf("parse tickSz for %s: %w", item.InstID, err)
			}
			contractValue, err := parseFloat(item.CtVal)
			if err != nil {
				return nil, fmt.Errorf("parse ctVal for %s: %w", item.InstID, err)
			}
			quantityStep, err := parseFloat(item.LotSz)
			if err != nil {
				return nil, fmt.Errorf("parse lotSz for %s: %w", item.InstID, err)
			}
			out = append(out, domain.ContractSpec{
				Exchange:      domain.ExchangeOKX,
				Symbol:        item.InstID,
				BaseAsset:     base,
				QuoteAsset:    quote,
				Status:        normalizeStatus(item.State),
				ContractType:  "PERPETUAL",
				TickSize:      tickSize,
				ContractValue: contractValue,
				QuantityStep:  quantityStep,
			})
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.ContractSpec(nil), contracts...), nil
}

func (c *Client) Quotes(ctx context.Context) ([]domain.VenueQuote, error) {
	contracts, err := c.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.tickersCache.Get(ctx, 2*time.Second, func(ctx context.Context) (tickersResponse, error) {
		query := url.Values{"instType": {"SWAP"}}
		var result tickersResponse
		if _, err := c.http.GetJSON(ctx, "/api/v5/market/tickers", query, &result); err != nil {
			return tickersResponse{}, wrapErr(err)
		}
		if err := checkCode(result.Code, result.Msg); err != nil {
			return tickersResponse{}, err
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}

	bySymbol := make(map[string]domain.ContractSpec, len(contracts))
	for _, contract := range contracts {
		bySymbol[contract.Symbol] = contract
	}
	out := make([]domain.VenueQuote, 0, len(resp.Data))
	for _, item := range resp.Data {
		contract, ok := bySymbol[item.InstID]
		if !ok {
			continue
		}
		bid, err := parseFloat(item.BidPx)
		if err != nil {
			return nil, fmt.Errorf("parse bidPx for %s: %w", item.InstID, err)
		}
		ask, err := parseFloat(item.AskPx)
		if err != nil {
			return nil, fmt.Errorf("parse askPx for %s: %w", item.InstID, err)
		}
		bidQty, err := parseFloat(item.BidSz)
		if err != nil {
			return nil, fmt.Errorf("parse bidSz for %s: %w", item.InstID, err)
		}
		askQty, err := parseFloat(item.AskSz)
		if err != nil {
			return nil, fmt.Errorf("parse askSz for %s: %w", item.InstID, err)
		}
		last, _ := parseFloat(item.Last)
		volumeCcy, _ := parseFloat(item.VolCcy24h)
		timestamp, err := strconv.ParseInt(item.Timestamp, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp for %s: %w", item.InstID, err)
		}
		quantityMultiplier := contract.ContractValue
		if quantityMultiplier <= 0 {
			quantityMultiplier = 1
		}
		out = append(out, domain.VenueQuote{
			Exchange:           domain.ExchangeOKX,
			SourceSymbol:       item.InstID,
			Contract:           contract,
			BidPrice:           bid,
			AskPrice:           ask,
			BidQuantity:        bidQty,
			AskQuantity:        askQty,
			QuantityMultiplier: quantityMultiplier,
			LastPrice:          last,
			TurnoverUSD:        volumeCcy * last,
			UpdatedAt:          time.UnixMilli(timestamp),
		})
	}
	return out, nil
}

func (c *Client) SymbolFunding(ctx context.Context, symbol string) (domain.FundingInfo, error) {
	return c.fundingCache.Get(ctx, symbol, time.Minute, func(ctx context.Context) (domain.FundingInfo, error) {
		query := url.Values{"instId": {symbol}}
		var resp fundingResponse
		if _, err := c.http.GetJSON(ctx, "/api/v5/public/funding-rate", query, &resp); err != nil {
			return domain.FundingInfo{}, wrapErr(err)
		}
		if err := checkCode(resp.Code, resp.Msg); err != nil {
			return domain.FundingInfo{}, err
		}
		if len(resp.Data) == 0 {
			return domain.FundingInfo{}, fmt.Errorf("okx funding returned no data for %s", symbol)
		}
		item := resp.Data[0]
		rate, err := parseFloat(item.FundingRate)
		if err != nil {
			return domain.FundingInfo{}, fmt.Errorf("parse fundingRate for %s: %w", symbol, err)
		}
		fundingMS, _ := strconv.ParseInt(item.FundingTime, 10, 64)
		previousMS, _ := strconv.ParseInt(item.PrevFundingTime, 10, 64)
		timestampMS, _ := strconv.ParseInt(item.Timestamp, 10, 64)
		interval := time.Duration(fundingMS-previousMS) * time.Millisecond
		if interval <= 0 {
			interval = 8 * time.Hour
		}
		return domain.FundingInfo{
			Rate:            rate,
			Interval:        interval,
			NextFundingTime: time.UnixMilli(fundingMS),
			UpdatedAt:       time.UnixMilli(timestampMS),
		}, nil
	})
}

func (c *Client) Klines(ctx context.Context, symbol, interval string, limit int) ([]domain.Kline, map[string]string, error) {
	query := url.Values{}
	query.Set("instId", symbol)
	query.Set("bar", interval)
	if limit > 0 {
		if limit > 300 {
			limit = 300
		}
		query.Set("limit", strconv.Itoa(limit))
	}

	var resp candlesResponse
	meta, err := c.http.GetJSON(ctx, "/api/v5/market/candles", query, &resp)
	if err != nil {
		return nil, nil, wrapErr(err)
	}
	if err := checkCode(resp.Code, resp.Msg); err != nil {
		return nil, nil, err
	}

	klines := make([]domain.Kline, 0, len(resp.Data))
	for _, row := range resp.Data {
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
		closed := len(row) < 9 || row[8] == "1"
		klines = append(klines, domain.Kline{
			OpenTime: time.UnixMilli(openMS),
			Close:    closePrice,
			Closed:   closed,
		})
	}
	sort.Slice(klines, func(i, j int) bool {
		return klines[i].OpenTime.Before(klines[j].OpenTime)
	})
	return klines, exchange.HeaderSubset(meta.Headers), nil
}

func (c *Client) StreamKlines(ctx context.Context, symbol, interval string, out chan<- domain.Kline) error {
	channel := ""
	switch interval {
	case "1m":
		channel = "candle1m"
	case "5m":
		channel = "candle5m"
	default:
		return fmt.Errorf("unsupported OKX kline interval %q", interval)
	}

	conn, _, err := websocket.Dial(ctx, c.wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect OKX websocket: %w", err)
	}
	defer conn.CloseNow()

	if err := wsjson.Write(ctx, conn, map[string]any{
		"op": "subscribe",
		"args": []map[string]string{{
			"channel": channel,
			"instId":  symbol,
		}},
	}); err != nil {
		return fmt.Errorf("subscribe OKX %s/%s: %w", channel, symbol, err)
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
				_ = conn.Write(pingCtx, websocket.MessageText, []byte("ping"))
			}
		}
	}()

	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageText || string(payload) == "pong" {
			continue
		}
		var event okxKlineEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return fmt.Errorf("decode OKX websocket kline: %w", err)
		}
		if event.Arg.Channel != channel || event.Arg.InstID != symbol {
			continue
		}
		for _, row := range event.Data {
			if len(row) < 5 {
				return fmt.Errorf("short OKX websocket kline row for %s", symbol)
			}
			openMS, err := strconv.ParseInt(row[0], 10, 64)
			if err != nil {
				return fmt.Errorf("parse OKX websocket open time for %s: %w", symbol, err)
			}
			closePrice, err := parseFloat(row[4])
			if err != nil {
				return fmt.Errorf("parse OKX websocket close for %s: %w", symbol, err)
			}
			point := domain.Kline{
				OpenTime: time.UnixMilli(openMS),
				Close:    closePrice,
				Closed:   len(row) >= 9 && row[8] == "1",
			}
			select {
			case out <- point:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func checkCode(code, message string) error {
	if code == "0" {
		return nil
	}
	return &exchange.ExchangeError{
		Exchange: string(domain.ExchangeOKX),
		Code:     code,
		Message:  message,
	}
}

func wrapErr(err error) error {
	if exErr, ok := err.(*exchange.ExchangeError); ok {
		exErr.Exchange = string(domain.ExchangeOKX)
		return exErr
	}
	return err
}

func instrumentCurrencies(symbol, base, quote string) (string, string) {
	if base != "" && quote != "" {
		return strings.ToUpper(base), strings.ToUpper(quote)
	}
	parts := strings.Split(strings.ToUpper(symbol), "-")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return strings.ToUpper(base), strings.ToUpper(quote)
}

func normalizeStatus(status string) string {
	if strings.EqualFold(status, "live") {
		return "TRADING"
	}
	return strings.ToUpper(status)
}

func parseFloat(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}

type instrumentsResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		InstType string `json:"instType"`
		InstID   string `json:"instId"`
		BaseCcy  string `json:"baseCcy"`
		QuoteCcy string `json:"quoteCcy"`
		CtVal    string `json:"ctVal"`
		TickSz   string `json:"tickSz"`
		LotSz    string `json:"lotSz"`
		State    string `json:"state"`
	} `json:"data"`
}

type tickersResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		InstID    string `json:"instId"`
		Last      string `json:"last"`
		AskPx     string `json:"askPx"`
		AskSz     string `json:"askSz"`
		BidPx     string `json:"bidPx"`
		BidSz     string `json:"bidSz"`
		VolCcy24h string `json:"volCcy24h"`
		Timestamp string `json:"ts"`
	} `json:"data"`
}

type fundingResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		FundingRate     string `json:"fundingRate"`
		FundingTime     string `json:"fundingTime"`
		PrevFundingTime string `json:"prevFundingTime"`
		Timestamp       string `json:"ts"`
	} `json:"data"`
}

type candlesResponse struct {
	Code string     `json:"code"`
	Msg  string     `json:"msg"`
	Data [][]string `json:"data"`
}

type okxKlineEvent struct {
	Arg struct {
		Channel string `json:"channel"`
		InstID  string `json:"instId"`
	} `json:"arg"`
	Data [][]string `json:"data"`
}
