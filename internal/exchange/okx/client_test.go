package okx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/domain"
)

func TestQuotesAndFunding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v5/public/instruments":
			writeJSON(w, `{"code":"0","msg":"","data":[{"instType":"SWAP","instId":"BTC-USDT-SWAP","ctVal":"0.01","tickSz":"0.1","lotSz":"0.01","state":"live"}]}`)
		case "/api/v5/market/tickers":
			writeJSON(w, `{"code":"0","msg":"","data":[{"instId":"BTC-USDT-SWAP","last":"100","askPx":"101","askSz":"2","bidPx":"99","bidSz":"3","volCcy24h":"1000","ts":"1700000000000"}]}`)
		case "/api/v5/public/funding-rate":
			writeJSON(w, `{"code":"0","msg":"","data":[{"fundingRate":"0.0001","fundingTime":"1700028800000","prevFundingTime":"1700000000000","ts":"1700001000000"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewWithBaseURL(server.URL)
	quotes, err := client.Quotes(context.Background())
	if err != nil {
		t.Fatalf("Quotes: %v", err)
	}
	if len(quotes) != 1 {
		t.Fatalf("quotes = %d, want 1", len(quotes))
	}
	quote := quotes[0]
	if quote.Contract.BaseAsset != "BTC" || quote.Contract.QuoteAsset != "USDT" {
		t.Fatalf("currencies = %s/%s", quote.Contract.BaseAsset, quote.Contract.QuoteAsset)
	}
	if quote.QuantityMultiplier != 0.01 || quote.BidPrice != 99 || quote.AskPrice != 101 {
		t.Fatalf("unexpected quote: %+v", quote)
	}

	funding, err := client.SymbolFunding(context.Background(), "BTC-USDT-SWAP")
	if err != nil {
		t.Fatalf("Funding: %v", err)
	}
	if funding.Rate != 0.0001 || funding.Interval.Hours() != 8 {
		t.Fatalf("unexpected funding: %+v", funding)
	}
}

func TestCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"code":"50011","msg":"rate limit","data":[]}`)
	}))
	defer server.Close()

	if _, err := NewWithBaseURL(server.URL).Quotes(context.Background()); err == nil {
		t.Fatal("Quotes returned nil error")
	}
}

func TestKlinesSortsAscendingAndParsesClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/market/candles" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("bar"); got != "1m" {
			t.Fatalf("bar = %q, want 1m", got)
		}
		writeJSON(w, `{"code":"0","msg":"","data":[["2000","1","3","1","2.5","10","20","20","0"],["1000","1","2","0.5","1.5","10","15","15","1"]]}`)
	}))
	defer server.Close()

	klines, _, err := NewWithBaseURL(server.URL).Klines(context.Background(), "BTC-USDT-SWAP", "1m", 2)
	if err != nil {
		t.Fatalf("Klines: %v", err)
	}
	if len(klines) != 2 || klines[0].Close != 1.5 || klines[1].Close != 2.5 {
		t.Fatalf("unexpected klines: %+v", klines)
	}
	if !klines[0].Closed || klines[1].Closed {
		t.Fatalf("unexpected closed flags: %+v", klines)
	}
}

func TestStreamKlinesParsesUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		var subscription map[string]any
		if err := wsjson.Read(r.Context(), conn, &subscription); err != nil {
			return
		}
		_ = wsjson.Write(r.Context(), conn, map[string]any{
			"arg":  map[string]string{"channel": "candle1m", "instId": "BTC-USDT-SWAP"},
			"data": [][]string{{"1700000000000", "100", "102", "99", "101.5", "10", "20", "20", "0"}},
		})
		<-r.Context().Done()
	}))
	defer server.Close()

	client := New()
	client.wsURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan domain.Kline, 1)
	go func() { _ = client.StreamKlines(ctx, "BTC-USDT-SWAP", "1m", out) }()

	select {
	case point := <-out:
		if point.Close != 101.5 || point.OpenTime.UnixMilli() != 1700000000000 || point.Closed {
			t.Fatalf("unexpected point: %+v", point)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for OKX websocket point")
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
