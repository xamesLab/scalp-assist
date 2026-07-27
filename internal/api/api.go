package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"scalp-assist/internal/arbitrage"
	arbitragemodel "scalp-assist/internal/arbitrage/model"
	"scalp-assist/internal/config"
	"scalp-assist/internal/domain"
	"scalp-assist/internal/store"
)

const sessionCookie = "screener_session"

type Server struct {
	store         *store.Store
	defaults      domain.Settings
	jwtSecret     []byte
	secureCookies bool
	refresh       func(context.Context) error
	arbitrage     *arbitrage.Service
	loginLimiter  *loginLimiter
	now           func() time.Time
}

type Options struct {
	Store         *store.Store
	Defaults      domain.Settings
	JWTSecret     []byte
	SecureCookies bool
	Refresh       func(context.Context) error
	Arbitrage     *arbitrage.Service
}

type dashboardArbitrageRow struct {
	Instrument   domain.InstrumentKey `json:"instrument"`
	BuyExchange  domain.Exchange      `json:"buy_exchange"`
	SellExchange domain.Exchange      `json:"sell_exchange"`
	NetSpreadBps float64              `json:"net_spread_bps"`
	FirstSeenAt  time.Time            `json:"first_seen_at"`
	ObservedAt   time.Time            `json:"observed_at"`
}

type dashboardScreenerRow struct {
	Instrument domain.Instrument `json:"instrument"`
	Score      float64           `json:"score"`
}

func New(opts Options) *Server {
	return &Server{
		store:         opts.Store,
		defaults:      opts.Defaults,
		jwtSecret:     opts.JWTSecret,
		secureCookies: opts.SecureCookies,
		refresh:       opts.Refresh,
		arbitrage:     opts.Arbitrage,
		loginLimiter:  newLoginLimiter(5, time.Minute),
		now:           time.Now,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/auth/login", s.login)
	mux.HandleFunc("/api/auth/logout", s.withAuth(s.logout))
	mux.HandleFunc("/api/auth/me", s.withAuth(s.me))
	mux.HandleFunc("/api/top", s.withAuth(s.top))
	mux.HandleFunc("/api/history", s.withAuth(s.history))
	mux.HandleFunc("/api/snapshots", s.withAuth(s.snapshots))
	mux.HandleFunc("/api/stats", s.withAuth(s.stats))
	mux.HandleFunc("/api/prop-list", s.withAuth(s.propList))
	mux.HandleFunc("/api/settings", s.withAuth(s.settings))
	mux.HandleFunc("/api/refresh", s.withAuth(s.refreshHandler))
	mux.HandleFunc("/api/dashboard/summary", s.withAuth(s.dashboardSummary))
	mux.HandleFunc("/api/arbitrage/opportunities", s.withAuth(s.arbitrageOpportunities))
	mux.HandleFunc("/api/arbitrage/stats", s.withAuth(s.arbitrageStats))
	mux.HandleFunc("/api/arbitrage/refresh", s.withAuth(s.arbitrageRefresh))
	mux.HandleFunc("/api/arbitrage/fees", s.withAuth(s.arbitrageFees))
	mux.HandleFunc("/api/arbitrage/charts/instruments", s.withAuth(s.arbitrageChartInstruments))
	mux.HandleFunc("/api/arbitrage/charts/stream", s.withAuth(s.arbitrageChartStream))
	mux.HandleFunc("/api/screener/top", s.withAuth(s.top))
	mux.HandleFunc("/api/screener/history", s.withAuth(s.history))
	mux.HandleFunc("/api/screener/snapshots", s.withAuth(s.snapshots))
	mux.HandleFunc("/api/screener/stats", s.withAuth(s.stats))
	mux.HandleFunc("/api/screener/prop-list", s.withAuth(s.propList))
	mux.HandleFunc("/api/screener/settings", s.withAuth(s.settings))
	mux.HandleFunc("/api/screener/refresh", s.withAuth(s.refreshHandler))
	return mux
}

func (s *Server) dashboardSummary(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	screener, err := s.store.LatestSnapshotOverview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	screenerRows, err := s.store.LatestSnapshotRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	arbitrageRun, err := s.latestArbitrageRun(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	topScore := dashboardTopScreener(screenerRows, 5)
	positiveRows, positiveTotal := dashboardPositiveArbitrage(arbitrageRun.Opportunities, 5)
	writeJSON(w, http.StatusOK, map[string]any{
		"screener": map[string]any{
			"updated_at": screener.CreatedAt,
			"rows":       screener.Rows,
			"top_score":  topScore,
		},
		"arbitrage": map[string]any{
			"updated_at":             arbitrageRun.ObservedAt,
			"instruments":            arbitrageRun.Instruments,
			"opportunities":          len(arbitrageRun.Opportunities),
			"exchanges":              arbitrageRun.ExchangeState,
			"positive_opportunities": positiveRows,
			"positive_total":         positiveTotal,
		},
	})
}

func dashboardTopScreener(items []domain.MetricsRow, limit int) []dashboardScreenerRow {
	sorted := append([]domain.MetricsRow(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Score == sorted[j].Score {
			return sorted[i].Rank < sorted[j].Rank
		}
		return sorted[i].Score > sorted[j].Score
	})
	if limit >= 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	rows := make([]dashboardScreenerRow, 0, len(sorted))
	for _, item := range sorted {
		rows = append(rows, dashboardScreenerRow{Instrument: item.Instrument, Score: item.Score})
	}
	return rows
}

func dashboardPositiveArbitrage(items []arbitragemodel.Opportunity, limit int) ([]dashboardArbitrageRow, int) {
	rows := make([]dashboardArbitrageRow, 0, limit)
	total := 0
	for _, item := range items {
		if item.NetSpreadBps <= 0 {
			continue
		}
		total++
		if len(rows) >= limit {
			continue
		}
		rows = append(rows, dashboardArbitrageRow{
			Instrument:   item.Instrument,
			BuyExchange:  item.BuyExchange,
			SellExchange: item.SellExchange,
			NetSpreadBps: item.NetSpreadBps,
			FirstSeenAt:  item.FirstSeenAt,
			ObservedAt:   item.ObservedAt,
		})
	}
	return rows, total
}

func (s *Server) arbitrageOpportunities(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	run, err := s.latestArbitrageRun(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	limit := queryInt(r, "limit", len(run.Opportunities))
	if limit > 0 && limit < len(run.Opportunities) {
		run.Opportunities = run.Opportunities[:limit]
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) arbitrageStats(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	stats, err := s.store.ArbitrageStats(r.Context(), queryInt(r, "days", 7))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) arbitrageRefresh(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.arbitrage == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "scheduled": false})
		return
	}
	if err := s.arbitrage.Refresh(r.Context()); err != nil {
		writeError(w, http.StatusConflict, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) arbitrageFees(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.arbitrage == nil {
		writeError(w, http.StatusServiceUnavailable, "arbitrage_unavailable", "arbitrage service is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "configuration",
		"rates":  s.arbitrage.FeeRates(),
	})
}

func (s *Server) arbitrageChartInstruments(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.arbitrage == nil {
		writeJSON(w, http.StatusOK, []arbitragemodel.ChartInstrument{})
		return
	}
	writeJSON(w, http.StatusOK, s.arbitrage.ChartInstruments())
}

func (s *Server) arbitrageChartStream(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.arbitrage == nil {
		writeError(w, http.StatusServiceUnavailable, "arbitrage_unavailable", "arbitrage service is unavailable")
		return
	}
	instrumentID := strings.TrimSpace(r.URL.Query().Get("instrument"))
	interval := strings.TrimSpace(r.URL.Query().Get("interval"))
	events, unsubscribe, err := s.arbitrage.SubscribeChart(instrumentID, interval)
	if err != nil {
		status := http.StatusBadRequest
		code := "invalid_chart_request"
		if errors.Is(err, arbitrage.ErrChartInstrumentNotFound) {
			status = http.StatusNotFound
			code = "instrument_not_found"
		}
		writeError(w, status, code, err.Error())
		return
	}
	defer unsubscribe()

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1024)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	snapshot, err := s.arbitrage.ChartSnapshot(ctx, instrumentID, interval, 300)
	if err != nil {
		_ = writeChartMessage(ctx, conn, arbitragemodel.ChartEvent{Type: "error", Error: err.Error()})
		return
	}
	if err := writeChartMessage(ctx, conn, snapshot); err != nil {
		return
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			if err := writeChartMessage(ctx, conn, event); err != nil {
				return
			}
		case <-ping.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

func writeChartMessage(ctx context.Context, conn *websocket.Conn, value any) error {
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, conn, value)
}

func (s *Server) latestArbitrageRun(ctx context.Context) (arbitragemodel.Run, error) {
	if s.arbitrage != nil {
		run := s.arbitrage.Latest()
		if !run.ObservedAt.IsZero() {
			return run, nil
		}
	}
	return s.store.LatestArbitrageRun(ctx)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   s.now().UTC(),
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	ip := clientIP(r)
	if !s.loginLimiter.Allow(ip, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	ok, err := s.store.CheckPassword(r.Context(), req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}
	user, err := s.store.UserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	token, err := s.signToken(user.ID, user.Email, s.now().Add(30*24*time.Hour))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  s.now().Add(30 * 24 * time.Hour),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "email": user.Email})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, user store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "email": user.Email})
}

func (s *Server) top(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	rows, err := s.store.LatestSnapshotRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	limit := queryInt(r, "limit", len(rows))
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(formatTopText(rows)))
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "missing_symbol", "symbol is required")
		return
	}
	points, err := s.store.History(r.Context(), symbol, queryInt(r, "days", 30))
	if err != nil {
		writeError(w, http.StatusBadRequest, "history_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	from, _ := parseQueryTime(r.URL.Query().Get("from"))
	to, _ := parseQueryTime(r.URL.Query().Get("to"))
	items, err := s.store.SnapshotSummaries(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	stats, err := s.store.Stats(r.Context(), queryInt(r, "days", 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) propList(w http.ResponseWriter, r *http.Request, _ store.User) {
	switch r.Method {
	case http.MethodGet:
		props, err := s.store.PropList(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, props)
	case http.MethodPut:
		var props []store.PropInstrument
		if err := readJSON(r, &props); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", err.Error())
			return
		}
		if err := s.store.ReplacePropList(r.Context(), props); err != nil {
			writeError(w, http.StatusBadRequest, "prop_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, _ store.User) {
	switch r.Method {
	case http.MethodGet:
		overrides, effective, err := s.currentSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"defaults":  s.defaults,
			"overrides": overrides,
			"effective": effective,
		})
	case http.MethodPut:
		var req struct {
			Overrides map[string]string `json:"overrides"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", err.Error())
			return
		}
		effective, err := s.store.ReplaceSettingsOverrides(r.Context(), s.defaults, req.Overrides)
		if err != nil {
			writeError(w, http.StatusBadRequest, "settings_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"effective": effective, "overrides": req.Overrides})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) refreshHandler(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.refresh == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "scheduled": false})
		return
	}
	if err := s.refresh(r.Context()); err != nil {
		writeError(w, http.StatusConflict, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing session")
			return
		}
		claims, err := s.parseToken(cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid session")
			return
		}
		user, err := s.store.UserByID(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unknown user")
			return
		}
		next(w, r, user)
	}
}

func (s *Server) currentSettings(ctx context.Context) (map[string]string, domain.Settings, error) {
	overrides, err := s.store.SettingsOverrides(ctx)
	if err != nil {
		return nil, domain.Settings{}, err
	}
	effective, err := config.Effective(s.defaults, overrides)
	if err != nil {
		return nil, domain.Settings{}, err
	}
	return overrides, effective, nil
}

type tokenClaims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Exp    int64  `json:"exp"`
}

func (s *Server) signToken(userID int64, email string, exp time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(tokenClaims{UserID: userID, Email: email, Exp: exp.Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.jwtSecret)
	if _, err := mac.Write([]byte(unsigned)); err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) parseToken(token string) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenClaims{}, errors.New("malformed token")
	}
	unsigned := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return tokenClaims{}, err
	}
	mac := hmac.New(sha256.New, s.jwtSecret)
	_, _ = mac.Write([]byte(unsigned))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return tokenClaims{}, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, err
	}
	var claims tokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return tokenClaims{}, err
	}
	if s.now().Unix() > claims.Exp {
		return tokenClaims{}, errors.New("expired token")
	}
	return claims, nil
}

func LoadOrCreateJWTSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("jwt secret path is required")
	}
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 32 {
		return raw, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func readJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func queryInt(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func parseQueryTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", raw)
}

func formatTopText(rows []domain.MetricsRow) string {
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "#%d %s score=%.4f change=%.2f%% turnover=%.0f\n",
			row.Rank, displayInstrument(row.Instrument), row.Score, row.ChangePct24h, row.TurnoverUSD)
	}
	return b.String()
}

func displayInstrument(instrument domain.Instrument) string {
	if instrument.Multiplier == 1 {
		return instrument.Base + "USDT"
	}
	return fmt.Sprintf("%d%sUSDT", instrument.Multiplier, instrument.Base)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type loginLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (l *loginLimiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	var kept []time.Time
	for _, hit := range l.hits[ip] {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	if len(kept) >= l.limit {
		l.hits[ip] = kept
		return false
	}
	kept = append(kept, now)
	l.hits[ip] = kept
	return true
}
