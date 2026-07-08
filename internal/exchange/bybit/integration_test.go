//go:build integration

package bybit

import (
	"context"
	"errors"
	"testing"
	"time"

	"scalp-assist/internal/exchange"
)

func TestIntegrationSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot, err := New().Snapshot(ctx)
	if err != nil {
		var exErr *exchange.ExchangeError
		if errors.As(err, &exErr) && exErr.StatusCode == 403 {
			t.Skipf("Bybit blocked this network location: %v", err)
		}
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snapshot.Contracts) == 0 || len(snapshot.Tickers) == 0 {
		t.Fatalf("empty snapshot: contracts=%d tickers=%d", len(snapshot.Contracts), len(snapshot.Tickers))
	}
}
