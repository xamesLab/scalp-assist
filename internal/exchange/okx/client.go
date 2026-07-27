package okx

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
	"scalp-assist/internal/marketdata"
)

const DefaultBaseURL = "https://www.okx.com"

type Client struct {
	http           *exchange.Client
	contractsCache marketdata.Cache[[]domain.ContractSpec]
	tickersCache   marketdata.Cache[tickersResponse]
	fundingCache   marketdata.KeyedCache[string, domain.FundingInfo]
}

func New() *Client {
	return NewWithBaseURL(DefaultBaseURL)
}

func NewWithBaseURL(baseURL string) *Client {
	return &Client{http: exchange.NewClient(baseURL)}
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
