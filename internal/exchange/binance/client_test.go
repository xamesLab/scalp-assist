package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshotMergesExchangeInfoTickersAndFunding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-MBX-USED-WEIGHT-1M", "42")
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			writeJSON(w, `{
				"symbols": [
					{
						"symbol": "BTCUSDT",
						"status": "TRADING",
						"baseAsset": "BTC",
						"quoteAsset": "USDT",
						"contractType": "PERPETUAL",
						"filters": [{"filterType":"PRICE_FILTER","tickSize":"0.10"}]
					}
				]
			}`)
		case "/fapi/v1/ticker/24hr":
			writeJSON(w, `[
				{
					"symbol":"BTCUSDT",
					"priceChangePercent":"21.5",
					"lastPrice":"100000.5",
					"highPrice":"110000",
					"lowPrice":"90000",
					"quoteVolume":"123456789.12",
					"closeTime":1700000000000
				}
			]`)
		case "/fapi/v1/premiumIndex":
			writeJSON(w, `[{"symbol":"BTCUSDT","lastFundingRate":"0.0001"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := NewWithBaseURL(server.URL).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snapshot.Contracts) != 1 || len(snapshot.Tickers) != 1 {
		t.Fatalf("unexpected snapshot sizes: %+v", snapshot)
	}
	ticker := snapshot.Tickers[0]
	if ticker.TickSize != 0.10 {
		t.Fatalf("TickSize = %v, want 0.10", ticker.TickSize)
	}
	if ticker.FundingRate != 0.0001 {
		t.Fatalf("FundingRate = %v, want 0.0001", ticker.FundingRate)
	}
	if ticker.QuoteAsset != "USDT" || ticker.Status != "TRADING" {
		t.Fatalf("ticker merge failed: %+v", ticker)
	}
	if snapshot.RateLimits["X-MBX-USED-WEIGHT-1M"] != "42" {
		t.Fatalf("rate limit header missing: %+v", snapshot.RateLimits)
	}
}

func TestKlinesParsesOpenTimeAndClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/klines" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Fatalf("limit query = %q, want 2", got)
		}
		writeJSON(w, `[
			[1499040000000,"1","2","0.5","1.5","10",1499043599999,"15",10,"0","0","0"],
			[1499043600000,"1.5","3","1","2.5","10",1499047199999,"25",10,"0","0","0"]
		]`)
	}))
	defer server.Close()

	klines, _, err := NewWithBaseURL(server.URL).Klines(context.Background(), "BTCUSDT", "1h", 2)
	if err != nil {
		t.Fatalf("Klines returned error: %v", err)
	}
	if len(klines) != 2 {
		t.Fatalf("len(klines) = %d, want 2", len(klines))
	}
	if klines[0].Close != 1.5 || klines[1].Close != 2.5 {
		t.Fatalf("unexpected closes: %+v", klines)
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
