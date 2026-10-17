package bybit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/exchange"
)

func TestInstrumentsInfoPaginatesByCursor(t *testing.T) {
	var pages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/instruments-info" {
			http.NotFound(w, r)
			return
		}
		pages++
		w.Header().Set("X-Bapi-Limit", "100")
		if r.URL.Query().Get("cursor") == "" {
			writeJSON(w, `{"retCode":0,"retMsg":"OK","result":{"nextPageCursor":"next","list":[{"symbol":"BTCUSDT","contractType":"LinearPerpetual","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","priceFilter":{"tickSize":"0.10"}}]}}`)
			return
		}
		writeJSON(w, `{"retCode":0,"retMsg":"OK","result":{"nextPageCursor":"","list":[{"symbol":"ETHUSDT","contractType":"LinearPerpetual","status":"Trading","baseCoin":"ETH","quoteCoin":"USDT","priceFilter":{"tickSize":"0.01"}}]}}`)
	}))
	defer server.Close()

	contracts, headers, err := NewWithBaseURL(server.URL).InstrumentsInfo(context.Background())
	if err != nil {
		t.Fatalf("InstrumentsInfo returned error: %v", err)
	}
	if pages != 2 {
		t.Fatalf("pages = %d, want 2", pages)
	}
	if len(contracts) != 2 {
		t.Fatalf("len(contracts) = %d, want 2", len(contracts))
	}
	if headers["X-Bapi-Limit"] != "100" {
		t.Fatalf("rate limit header missing: %+v", headers)
	}
}

func TestSnapshotMergesInstrumentsAndTickers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5/market/instruments-info":
			writeJSON(w, `{"retCode":0,"retMsg":"OK","result":{"nextPageCursor":"","list":[{"symbol":"BTCUSDT","contractType":"LinearPerpetual","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","priceFilter":{"tickSize":"0.10"}}]}}`)
		case "/v5/market/tickers":
			writeJSON(w, `{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"BTCUSDT","lastPrice":"100000","highPrice24h":"110000","lowPrice24h":"90000","price24hPcnt":"0.25","turnover24h":"250000000","fundingRate":"0.0002","openInterestValue":"1234"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := NewWithBaseURL(server.URL).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snapshot.Tickers) != 1 {
		t.Fatalf("len(tickers) = %d, want 1", len(snapshot.Tickers))
	}
	ticker := snapshot.Tickers[0]
	if ticker.ChangePct24h != 25 {
		t.Fatalf("ChangePct24h = %v, want 25", ticker.ChangePct24h)
	}
	if ticker.TickSize != 0.10 || ticker.Status != "Trading" || ticker.QuoteAsset != "USDT" {
		t.Fatalf("ticker merge failed: %+v", ticker)
	}
}

func TestKlinesSortsAscending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/kline" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, `{"retCode":0,"retMsg":"OK","result":{"list":[["2000","1","2","1","2","10","20"],["1000","1","2","1","1.5","10","15"]]}}`)
	}))
	defer server.Close()

	klines, _, err := NewWithBaseURL(server.URL).Klines(context.Background(), "BTCUSDT", "60", 2)
	if err != nil {
		t.Fatalf("Klines returned error: %v", err)
	}
	if len(klines) != 2 {
		t.Fatalf("len(klines) = %d, want 2", len(klines))
	}
	if klines[0].Close != 1.5 || klines[1].Close != 2 {
		t.Fatalf("klines not sorted ascending: %+v", klines)
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
		args, _ := subscription["args"].([]any)
		if len(args) != 1 || args[0] != "kline.60.BTCUSDT" {
			t.Errorf("unexpected Bybit subscription: %+v", subscription)
			return
		}
		_ = wsjson.Write(r.Context(), conn, map[string]any{
			"topic": "kline.60.BTCUSDT",
			"data": []map[string]any{{
				"start": int64(1700000000000), "close": "101.5", "confirm": true,
			}},
		})
		<-r.Context().Done()
	}))
	defer server.Close()

	client := New()
	client.wsURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan domain.Kline, 1)
	go func() { _ = client.StreamKlines(ctx, "BTCUSDT", "1h", out) }()

	select {
	case point := <-out:
		if point.Close != 101.5 || point.OpenTime.UnixMilli() != 1700000000000 || !point.Closed {
			t.Fatalf("unexpected point: %+v", point)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for Bybit websocket point")
	}
}

func TestRetCodeErrorIsExchangeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"retCode":10006,"retMsg":"rate limit","result":{"list":[]}}`)
	}))
	defer server.Close()

	_, _, err := NewWithBaseURL(server.URL).Tickers(context.Background())
	if err == nil {
		t.Fatal("Tickers returned nil error")
	}
	var exErr *exchange.ExchangeError
	if !errors.As(err, &exErr) {
		t.Fatalf("error type = %T, want *ExchangeError", err)
	}
	if exErr.Code != "10006" {
		t.Fatalf("code = %q, want 10006", exErr.Code)
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
