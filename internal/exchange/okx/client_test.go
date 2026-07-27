package okx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
