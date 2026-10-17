package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/arbitrage"
	arbitragemodel "scalp-assist/internal/arbitrage/model"
	marketcharts "scalp-assist/internal/charts"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/propscreen"
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
	resp = request(t, ts, http.MethodGet, "/api/auth/me", nil, cookie)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("old session after logout status = %d, want 401", resp.Code)
	}
}

func TestRegisterCreatesSessionAndNormalizesEmail(t *testing.T) {
	ts := newTestServer(t)
	ownerCookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)
	sharedProps := []store.PropInstrument{{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Note: "shared"}}
	resp := request(t, ts, http.MethodPut, "/api/prop-list", sharedProps, ownerCookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("seed shared prop list status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = request(t, ts, http.MethodPost, "/api/auth/register", map[string]string{
		"email": "  New.User@Example.COM ", "password": "a sufficiently long password",
	}, nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("register status = %d body=%s", resp.Code, resp.Body.String())
	}
	cookie := resp.Result().Cookies()[0]
	if cookie.Name != sessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}
	resp = request(t, ts, http.MethodGet, "/api/auth/me", nil, cookie)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"email":"new.user@example.com"`) {
		t.Fatalf("registered session status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = request(t, ts, http.MethodGet, "/api/prop-list", nil, cookie)
	var gotProps []store.PropInstrument
	decode(t, resp, &gotProps)
	if resp.Code != http.StatusOK || len(gotProps) != 1 || gotProps[0].Note != "shared" {
		t.Fatalf("new account should see shared prop list: status=%d props=%+v", resp.Code, gotProps)
	}
	resp = request(t, ts, http.MethodPost, "/api/auth/register", map[string]string{
		"email": "NEW.USER@example.com", "password": "a sufficiently long password",
	}, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
}

func TestRegisterRejectsInvalidInputAndRateLimits(t *testing.T) {
	ts := newTestServer(t)
	for _, body := range []map[string]string{
		{"email": "not-an-email", "password": "a sufficiently long password"},
		{"email": "valid@example.com", "password": "too short"},
		{"email": "valid@example.com", "password": strings.Repeat("x", 73)},
	} {
		resp := request(t, ts, http.MethodPost, "/api/auth/register", body, nil)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("registration with invalid input status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	ts = newTestServer(t)
	for i := 0; i < 5; i++ {
		resp := request(t, ts, http.MethodPost, "/api/auth/register", map[string]string{
			"email": fmt.Sprintf("user%d@example.com", i), "password": "a sufficiently long password",
		}, nil)
		if resp.Code != http.StatusCreated {
			t.Fatalf("registration %d status=%d body=%s", i, resp.Code, resp.Body.String())
		}
	}
	resp := request(t, ts, http.MethodPost, "/api/auth/register", map[string]string{
		"email": "last@example.com", "password": "a sufficiently long password",
	}, nil)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("registration limit status=%d, want 429", resp.Code)
	}
}

func TestClientIPTrustsForwardedAddressOnlyFromConfiguredProxy(t *testing.T) {
	_, proxy, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:8080"
	request.Header.Set("X-Forwarded-For", "198.51.100.42")
	if got := clientIP(request, nil); got != "127.0.0.1" {
		t.Fatalf("untrusted forwarded address used: got %q", got)
	}
	if got := clientIP(request, []*net.IPNet{proxy}); got != "198.51.100.42" {
		t.Fatalf("trusted forwarded address = %q", got)
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

func TestPropScreenEndpoints(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prop-screen.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.BootstrapUser(context.Background(), "owner@example.com", "secret"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := db.ReplacePropList(context.Background(), []store.PropInstrument{{
		Instrument: domain.Instrument{Base: "BTC", Multiplier: 1},
	}}); err != nil {
		t.Fatalf("ReplacePropList: %v", err)
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	propService, err := propscreen.New(propscreen.Options{
		Store: db,
		Clients: []propscreen.MarketClient{apiPropClient{
			exchange: domain.ExchangeBinance,
			snapshot: domain.MarketSnapshot{
				Exchange:  domain.ExchangeBinance,
				FetchedAt: now,
				Contracts: []domain.ContractSpec{{
					Exchange: domain.ExchangeBinance, Symbol: "BTCUSDT", BaseAsset: "BTC",
					QuoteAsset: "USDT", Status: "TRADING", ContractType: "PERPETUAL",
				}},
				Tickers: []domain.Ticker{{
					Exchange: domain.ExchangeBinance, Symbol: "BTCUSDT",
					ChangePct24h: 4.5, TurnoverUSD: 123_000_000, UpdatedAt: now,
				}},
			},
		}},
		Interval: time.Minute,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("propscreen.New: %v", err)
	}
	srv := New(Options{
		Store: db, Defaults: testSettings(),
		JWTSecret:  []byte("01234567890123456789012345678901"),
		PropScreen: propService,
	})
	ts := testServer{handler: srv.Handler(), store: db}
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)

	resp := request(t, ts, http.MethodGet, "/api/prop-screen", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET prop-screen status=%d body=%s", resp.Code, resp.Body.String())
	}
	var run propscreen.Run
	decode(t, resp, &run)
	if run.Listed != 1 || len(run.Rows) != 1 || run.Rows[0].SourceExchange != domain.ExchangeBinance {
		t.Fatalf("run = %+v", run)
	}

	resp = request(t, ts, http.MethodGet, "/api/dashboard/summary", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", resp.Code, resp.Body.String())
	}
	var summary struct {
		PropScreen struct {
			PropTotal      int `json:"prop_total"`
			Listed         int `json:"listed"`
			BinanceSources int `json:"binance_sources"`
		} `json:"prop_screen"`
	}
	decode(t, resp, &summary)
	if summary.PropScreen.PropTotal != 1 || summary.PropScreen.Listed != 1 || summary.PropScreen.BinanceSources != 1 {
		t.Fatalf("prop-screen summary = %+v", summary.PropScreen)
	}

	resp = request(t, ts, http.MethodPost, "/api/prop-screen/refresh", nil, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST prop-screen refresh status=%d body=%s", resp.Code, resp.Body.String())
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

func TestDashboardAndArbitrageEndpoints(t *testing.T) {
	ts := newTestServer(t)
	cookie := login(t, ts, "owner@example.com", "secret", http.StatusOK)
	now := time.Now().UTC()
	saveRows(t, ts.store, []domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "BTC", Multiplier: 1}, Rank: 1, Price: 100},
	})
	if _, err := ts.store.SaveArbitrageRun(context.Background(), arbitragemodel.Run{
		ObservedAt: now,
		ExchangeState: map[string]arbitragemodel.ExchangeState{
			"binance": {OK: true, Quotes: 1, UpdatedAt: now},
			"bybit":   {OK: true, Quotes: 1, UpdatedAt: now},
		},
		Instruments: 1,
		Opportunities: []arbitragemodel.Opportunity{{
			Instrument:     domain.InstrumentKey{Base: "BTC", Quote: "USDT", Multiplier: 1, Kind: "linear_perpetual"},
			BuyExchange:    domain.ExchangeBinance,
			SellExchange:   domain.ExchangeBybit,
			BuySymbol:      "BTCUSDT",
			SellSymbol:     "BTCUSDT",
			BuyAsk:         100,
			SellBid:        101,
			GrossSpreadBps: 100,
			NetSpreadBps:   90,
			BuyQuoteAt:     now,
			SellQuoteAt:    now,
			FirstSeenAt:    now,
			ObservedAt:     now,
		}},
	}); err != nil {
		t.Fatalf("SaveArbitrageRun: %v", err)
	}

	for _, path := range []string{
		"/api/dashboard/summary",
		"/api/arbitrage/opportunities",
		"/api/arbitrage/stats?days=7",
		"/api/screener/top",
	} {
		resp := request(t, ts, http.MethodGet, path, nil, cookie)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, resp.Code, resp.Body.String())
		}
	}
}

func TestDashboardPositiveArbitrageLimitsRowsAndCountsRemainder(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	items := make([]arbitragemodel.Opportunity, 0, 8)
	for index := range 7 {
		items = append(items, arbitragemodel.Opportunity{
			Instrument:   domain.InstrumentKey{Base: fmt.Sprintf("ASSET%d", index), Quote: "USDT", Multiplier: 1, Kind: "linear_perpetual"},
			BuyExchange:  domain.ExchangeBinance,
			SellExchange: domain.ExchangeBybit,
			NetSpreadBps: float64(70 - index),
			FirstSeenAt:  now.Add(-time.Duration(index+1) * time.Minute),
			ObservedAt:   now,
		})
	}
	items = append(items, arbitragemodel.Opportunity{NetSpreadBps: 0})

	rows, total := dashboardPositiveArbitrage(items, 5)
	if len(rows) != 5 || total != 7 {
		t.Fatalf("rows=%d total=%d, want rows=5 total=7", len(rows), total)
	}
	if rows[0].Instrument.Base != "ASSET0" || rows[4].NetSpreadBps != 66 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestDashboardTopScreenerSortsByScore(t *testing.T) {
	rows := dashboardTopScreener([]domain.MetricsRow{
		{Instrument: domain.Instrument{Base: "LOW", Multiplier: 1}, Rank: 1, Score: 0.2},
		{Instrument: domain.Instrument{Base: "HIGH", Multiplier: 1}, Rank: 3, Score: 0.9},
		{Instrument: domain.Instrument{Base: "MID", Multiplier: 1}, Rank: 2, Score: 0.5},
	}, 2)
	if len(rows) != 2 || rows[0].Instrument.Base != "HIGH" || rows[1].Instrument.Base != "MID" {
		t.Fatalf("unexpected top score rows: %+v", rows)
	}
}

func TestArbitrageChartWebSocketRequiresAuthAndStreamsSnapshot(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "charts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	if _, err := db.BootstrapUser(context.Background(), "owner@example.com", "secret"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	clients := []arbitrage.QuoteClient{
		apiChartClient{exchange: domain.ExchangeBinance, symbol: "BTCUSDT", price: 100, now: now},
		apiChartClient{exchange: domain.ExchangeBybit, symbol: "BTCUSDT", price: 101, now: now},
		apiChartClient{exchange: domain.ExchangeOKX, symbol: "BTC-USDT-SWAP", price: 102, now: now},
	}
	chartService, err := arbitrage.New(arbitrage.Options{
		Store:   db,
		Clients: clients,
		Settings: arbitragemodel.Settings{
			MinGrossSpreadBps: 0, MaxGrossSpreadBps: 1000, MinNetSpreadBps: -100,
			MaxQuoteAge: time.Minute, MaxQuoteSkew: time.Minute, TopN: 30,
			HistoryInterval: time.Minute, RetentionDays: 30,
			FeesBps:      map[string]float64{"binance": 5, "bybit": 5, "okx": 5},
			MakerFeesBps: map[string]float64{"binance": 2, "bybit": 2, "okx": 2},
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("arbitrage.New: %v", err)
	}
	if err := chartService.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	srv := New(Options{
		Store: db, Defaults: testSettings(),
		JWTSecret: []byte("01234567890123456789012345678901"),
		Arbitrage: chartService,
	})
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	unauthorizedURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/arbitrage/charts/stream?instrument=BTC%3AUSDT%3A1%3Alinear_perpetual&interval=1m"
	if conn, resp, err := websocket.Dial(context.Background(), unauthorizedURL, nil); err == nil {
		conn.CloseNow()
		t.Fatal("unauthorized websocket dial succeeded")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized websocket response = %+v err=%v", resp, err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginBody := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret"}`)
	resp, err := client.Post(httpServer.URL+"/api/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	feesResponse, err := client.Get(httpServer.URL + "/api/arbitrage/fees")
	if err != nil {
		t.Fatalf("fees request: %v", err)
	}
	var fees struct {
		Source string                           `json:"source"`
		Rates  []arbitragemodel.ExchangeFeeRate `json:"rates"`
	}
	if err := json.NewDecoder(feesResponse.Body).Decode(&fees); err != nil {
		_ = feesResponse.Body.Close()
		t.Fatalf("decode fees: %v", err)
	}
	_ = feesResponse.Body.Close()
	if feesResponse.StatusCode != http.StatusOK || fees.Source != "configuration" ||
		len(fees.Rates) != 3 || fees.Rates[0].MakerBps != 2 {
		t.Fatalf("unexpected fees response: status=%d body=%+v", feesResponse.StatusCode, fees)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, unauthorizedURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	var snapshot arbitragemodel.ChartSnapshot
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.Type != "snapshot" || len(snapshot.Series["binance"]) != 1 || len(snapshot.StoredGross) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestMarketChartWebSocketMultiplexesSnapshotAndBar(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "market-charts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	if _, err := db.BootstrapUser(context.Background(), "owner@example.com", "secret"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	chartService, err := marketcharts.New(marketcharts.Options{
		Clients: []marketcharts.Client{
			apiChartClient{exchange: domain.ExchangeBinance, symbol: "BTCUSDT", price: 100, now: now},
		},
	})
	if err != nil {
		t.Fatalf("charts.New: %v", err)
	}
	if err := chartService.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	srv := New(Options{
		Store: db, Defaults: testSettings(),
		JWTSecret: []byte("01234567890123456789012345678901"),
		Charts:    chartService,
	})
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginBody := bytes.NewBufferString(`{"email":"owner@example.com","password":"secret"}`)
	loginResponse, err := client.Post(httpServer.URL+"/api/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.StatusCode)
	}

	instrumentsResponse, err := client.Get(httpServer.URL + "/api/charts/instruments")
	if err != nil {
		t.Fatalf("instruments request: %v", err)
	}
	var instruments []marketcharts.Instrument
	if err := json.NewDecoder(instrumentsResponse.Body).Decode(&instruments); err != nil {
		_ = instrumentsResponse.Body.Close()
		t.Fatalf("decode instruments: %v", err)
	}
	_ = instrumentsResponse.Body.Close()
	if instrumentsResponse.StatusCode != http.StatusOK || len(instruments) != 1 || instruments[0].ID != "BTC:1" {
		t.Fatalf("unexpected instruments: status=%d body=%+v", instrumentsResponse.StatusCode, instruments)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streamURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/charts/stream"
	conn, _, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chartCommand{
		Type: "subscribe", SlotID: "slot-btc", InstrumentID: "BTC:1", Interval: "1h",
	}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	var snapshot struct {
		Type    string                `json:"type"`
		SlotID  string                `json:"slot_id"`
		Candles []marketcharts.Candle `json:"candles"`
	}
	for snapshot.Type != "snapshot" {
		if err := wsjson.Read(ctx, conn, &snapshot); err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
	}
	if snapshot.SlotID != "slot-btc" || len(snapshot.Candles) != 1 || snapshot.Candles[0].Close != 100 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	var bar struct {
		Type   string               `json:"type"`
		SlotID string               `json:"slot_id"`
		Candle *marketcharts.Candle `json:"candle"`
	}
	for bar.Type != "bar" {
		if err := wsjson.Read(ctx, conn, &bar); err != nil {
			t.Fatalf("read bar: %v", err)
		}
	}
	if bar.SlotID != "slot-btc" || bar.Candle == nil || bar.Candle.Close != 100.5 {
		t.Fatalf("unexpected bar: %+v", bar)
	}
}

type apiChartClient struct {
	exchange domain.Exchange
	symbol   string
	price    float64
	now      time.Time
}

type apiPropClient struct {
	exchange domain.Exchange
	snapshot domain.MarketSnapshot
}

func (f apiPropClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f apiPropClient) MarketSnapshot(context.Context) (domain.MarketSnapshot, error) {
	return f.snapshot, nil
}

func (f apiChartClient) Exchange() domain.Exchange {
	return f.exchange
}

func (f apiChartClient) Quotes(context.Context) ([]domain.VenueQuote, error) {
	return []domain.VenueQuote{{
		Exchange: f.exchange, SourceSymbol: f.symbol,
		Contract: domain.ContractSpec{
			Exchange: f.exchange, Symbol: f.symbol, BaseAsset: "BTC", QuoteAsset: "USDT",
			Status: "TRADING", ContractType: "PERPETUAL",
		},
		BidPrice: f.price - 1, AskPrice: f.price, BidQuantity: 10, AskQuantity: 10,
		QuantityMultiplier: 1, UpdatedAt: f.now,
	}}, nil
}

func (f apiChartClient) Klines(context.Context, string, string, int) ([]domain.Kline, map[string]string, error) {
	return []domain.Kline{{OpenTime: f.now, Close: f.price}}, nil, nil
}

func (f apiChartClient) StreamKlines(ctx context.Context, _ string, _ string, out chan<- domain.Kline) error {
	select {
	case out <- domain.Kline{OpenTime: f.now, Close: f.price + 0.5}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
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
