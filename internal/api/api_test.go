package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"scalp-assist/internal/domain"
	"scalp-assist/internal/store"
)

func TestHealthDoesNotRequireAuth(t *testing.T) {
	ts := newTestServer(t)
	resp := request(t, ts, http.MethodGet, "/api/health", nil, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
}

func TestProtectedEndpointRequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	resp := request(t, ts, http.MethodGet, "/api/top", nil, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestLoginMeLogoutFlow(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)

	resp := request(t, ts, http.MethodGet, "/api/auth/me", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", resp.Code, resp.Body.String())
	}
	var me map[string]any
	decode(t, resp, &me)
	if me["email"] != "owner@example.com" {
		t.Fatalf("me = %+v", me)
	}

	resp = request(t, ts, http.MethodPost, "/api/auth/logout", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("logout status = %d", resp.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts := newTestServer(t)
	for i := 0; i < 5; i++ {
		login(t, ts, "owner@example.com", "wrong", http.StatusUnauthorized)
	}
	login(t, ts, "owner@example.com", "wrong", http.StatusTooManyRequests)
}

func TestTopJSONAndText(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)
	saveRows(t, ts.store, []domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1, BinanceSymbol: "BTCUSDT", OnBinance: true}, Rank: 1, Price: 100, TurnoverUSD: 200, DayRangePct: 10, ChangePct24h: 20, Score: 0.9},
		{Instrument: domain.Instrument{Base: "ETH", Multiplier: 1, BinanceSymbol: "ETHUSDT", OnBinance: true}, Rank: 2, Price: 50, TurnoverUSD: 100, DayRangePct: 8, ChangePct24h: 10, Score: 0.8},
	})

	resp := request(t, ts, http.MethodGet, "/api/top?limit=1", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("top status = %d body=%s", resp.Code, resp.Body.String())
	}
	var rows []domain.MetricsRow
	decode(t, resp, &rows)
	if len(rows) != 1 || rows[0].Instrument.Base != "BTC" {
		t.Fatalf("rows = %+v", rows)
	}

	resp = request(t, ts, http.MethodGet, "/api/top?format=text", nil, cookie)
	if resp.Code != http.StatusOK || !bytes.Contains(resp.Body.Bytes(), []byte("#1 BTCUSDT")) {
		t.Fatalf("text response status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestSettingsGetPutAndInvalidIsAtomic(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)

	resp := request(t, ts, http.MethodPut, "/api/settings", map[string]any{
		"overrides": map[string]string{"min_volume_usd": "250000000"},
	}, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("settings put status=%d body=%s", resp.Code, resp.Body.String())
	}

	resp = request(t, ts, http.MethodPut, "/api/settings", map[string]any{
		"overrides": map[string]string{"corr_threshold": "1.5"},
	}, cookie)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings status=%d want 400", resp.Code)
	}

	resp = request(t, ts, http.MethodGet, "/api/settings", nil, cookie)
	var body struct {
		Overrides map[string]string `json:"overrides"`
	}
	decode(t, resp, &body)
	if body.Overrides["min_volume_usd"] != "250000000" {
		t.Fatalf("overrides = %+v", body.Overrides)
	}
}

func TestPropListGetPut(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)

	props := []store.PropInstrument{{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Note: "main"}}
	resp := request(t, ts, http.MethodPut, "/api/prop-list", props, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = request(t, ts, http.MethodGet, "/api/prop-list", nil, cookie)
	var got []store.PropInstrument
	decode(t, resp, &got)
	if len(got) != 1 || got[0].Instrument.Base != "BTC" || got[0].Note != "main" {
		t.Fatalf("props = %+v", got)
	}
}

func TestSnapshotsHistoryStats(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)
	saveRows(t, ts.store, []domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Rank: 1, Price: 100, TurnoverUSD: 200, DayRangePct: 10, ChangePct24h: 20, Score: 0.9, InPropList: true},
	})

	for _, path := range []string{"/api/snapshots", "/api/history?symbol=BTCUSDT&days=7", "/api/stats?days=7"} {
		resp := request(t, ts, http.MethodGet, path, nil, cookie)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, resp.Code, resp.Body.String())
		}
	}
}

type testServer struct {
	handler http.Handler
	store   *store.Store
}

func newTestServer(t *testing.T) testServer {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.BootstrapUser(context.Background(), "owner@example.com", "secret"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	srv := New(Options{
		Store:     db,
		Defaults:  testSettings(),
		JWTSecret: []byte("01234567890123456789012345678901"),
	})
	srv.now = func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) }
	return testServer{handler: srv.Handler(), store: db}
}

func request(t *testing.T, ts testServer, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:12345"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, ts testServer, email, password string, want int) *http.Cookie {
	t.Helper()
	resp := request(t, ts, http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password}, nil)
	if resp.Code != want {
		t.Fatalf("login status=%d want=%d body=%s", resp.Code, want, resp.Body.String())
	}
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	return nil
}

func decode(t *testing.T, resp *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s: %v", resp.Body.String(), err)
	}
}

func saveRows(t *testing.T, db *store.Store, rows []domain.MetricsRow) {
	t.Helper()
	if _, err := db.SaveSnapshot(context.Background(), time.Now().UTC(), rows); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
}

func testSettings() domain.Settings {
	return domain.Settings{
		MinVolumeUSD:       100_000_000,
		MinChangePct:       20,
		EnableChangeFilter: true,
		CorrThreshold:      0.8,
		Inplay:             domain.InplaySettings{MarketThresholdPct: 3, Multiplier: 2},
		Weights:            domain.ScoreWeights{Volatility: 0.4, Turnover: 0.3, Inplay: 0.2, Corr: 0.1},
		TopN:               20,
	}
}
