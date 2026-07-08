package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"scalp-assist/internal/config"
	"scalp-assist/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	writer *sql.DB
	reader *sql.DB
}

type PropInstrument struct {
	Instrument domain.Instrument
	Note       string
}

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := ensureDir(dir); err != nil {
				return nil, err
			}
		}
	}
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dsn = "file:" + filepath.ToSlash(path)
	}
	if !strings.Contains(dsn, "?") {
		dsn += "?"
	} else {
		dsn += "&"
	}
	dsn += "_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"

	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}

	store := &Store{writer: writer, reader: reader}
	if err := store.configure(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	var err error
	if s.reader != nil {
		err = errors.Join(err, s.reader.Close())
	}
	if s.writer != nil {
		err = errors.Join(err, s.writer.Close())
	}
	return err
}

func (s *Store) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, db := range []*sql.DB{s.writer, s.reader} {
		for _, pragma := range pragmas {
			if _, err := db.ExecContext(ctx, pragma); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		applied, err := s.migrationApplied(ctx, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.writer.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveSnapshot(ctx context.Context, createdAt time.Time, rows []domain.MetricsRow) (int64, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `INSERT INTO snapshots(created_at) VALUES (?)`, formatTime(createdAt))
	if err != nil {
		return 0, err
	}
	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		instrumentID, err := upsertInstrumentTx(ctx, tx, row.Instrument)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO snapshot_rows (
  snapshot_id, instrument_id, rank, price, turnover_usd, day_range_pct, change_pct_24h,
  funding_rate, relative_tick_pct, btc_corr_7d, high_corr, is_inplay, in_prop_list, bybit_only, score
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshotID, instrumentID, row.Rank, row.Price, row.TurnoverUSD, row.DayRangePct, row.ChangePct24h,
			row.FundingRate, row.RelativeTickPct, nullableFloat(row.BTCCorr7d), boolInt(row.HighCorr),
			boolInt(row.IsInplay), boolInt(row.InPropList), boolInt(row.BybitOnly), row.Score,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return snapshotID, nil
}

func (s *Store) LatestSnapshotRows(ctx context.Context) ([]domain.MetricsRow, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT id FROM snapshots ORDER BY created_at DESC, id DESC LIMIT 1`)
	var snapshotID int64
	if err := row.Scan(&snapshotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.SnapshotRows(ctx, snapshotID)
}

func (s *Store) SnapshotRows(ctx context.Context, snapshotID int64) ([]domain.MetricsRow, error) {
	rows, err := s.reader.QueryContext(ctx, `
SELECT i.base, i.multiplier, i.binance_symbol, i.bybit_symbol, i.on_binance, i.on_bybit,
       sr.rank, sr.price, sr.turnover_usd, sr.day_range_pct, sr.change_pct_24h, sr.funding_rate,
       sr.relative_tick_pct, sr.btc_corr_7d, sr.high_corr, sr.is_inplay, sr.in_prop_list, sr.bybit_only, sr.score
FROM snapshot_rows sr
JOIN instruments i ON i.id = sr.instrument_id
WHERE sr.snapshot_id = ?
ORDER BY sr.rank ASC`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.MetricsRow
	for rows.Next() {
		var item domain.MetricsRow
		var corr sql.NullFloat64
		var onBinance, onBybit, highCorr, isInplay, inPropList, bybitOnly int
		if err := rows.Scan(
			&item.Instrument.Base, &item.Instrument.Multiplier, &item.Instrument.BinanceSymbol, &item.Instrument.BybitSymbol,
			&onBinance, &onBybit, &item.Rank, &item.Price, &item.TurnoverUSD, &item.DayRangePct, &item.ChangePct24h,
			&item.FundingRate, &item.RelativeTickPct, &corr, &highCorr, &isInplay, &inPropList, &bybitOnly, &item.Score,
		); err != nil {
			return nil, err
		}
		item.Instrument.OnBinance = onBinance != 0
		item.Instrument.OnBybit = onBybit != 0
		if corr.Valid {
			item.BTCCorr7d = &corr.Float64
		}
		item.HighCorr = highCorr != 0
		item.IsInplay = isInplay != 0
		item.InPropList = inPropList != 0
		item.BybitOnly = bybitOnly != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ReplacePropList(ctx context.Context, props []PropInstrument) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM prop_instruments`); err != nil {
		return err
	}
	for _, prop := range props {
		id, err := upsertInstrumentTx(ctx, tx, prop.Instrument)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO prop_instruments(instrument_id, note)
VALUES (?, ?)
ON CONFLICT(instrument_id) DO UPDATE SET note = excluded.note`, id, prop.Note); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) PropList(ctx context.Context) ([]PropInstrument, error) {
	rows, err := s.reader.QueryContext(ctx, `
SELECT i.base, i.multiplier, i.binance_symbol, i.bybit_symbol, i.on_binance, i.on_bybit, p.note
FROM prop_instruments p
JOIN instruments i ON i.id = p.instrument_id
ORDER BY i.base, i.multiplier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PropInstrument
	for rows.Next() {
		var prop PropInstrument
		var onBinance, onBybit int
		if err := rows.Scan(&prop.Instrument.Base, &prop.Instrument.Multiplier, &prop.Instrument.BinanceSymbol,
			&prop.Instrument.BybitSymbol, &onBinance, &onBybit, &prop.Note); err != nil {
			return nil, err
		}
		prop.Instrument.OnBinance = onBinance != 0
		prop.Instrument.OnBybit = onBybit != 0
		out = append(out, prop)
	}
	return out, rows.Err()
}

func (s *Store) SettingsOverrides(ctx context.Context) (map[string]string, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) ReplaceSettingsOverrides(ctx context.Context, defaults domain.Settings, overrides map[string]string) (domain.Settings, error) {
	effective, err := config.Effective(defaults, overrides)
	if err != nil {
		return domain.Settings{}, err
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return domain.Settings{}, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings`); err != nil {
		return domain.Settings{}, err
	}
	now := formatTime(time.Now().UTC())
	for key, value := range overrides {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings(key, value, updated_at)
VALUES (?, ?, ?)`, key, value, now); err != nil {
			return domain.Settings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Settings{}, err
	}
	tx = nil
	return effective, nil
}

func (s *Store) CorrCache(ctx context.Context) (map[string]*float64, error) {
	rows, err := s.reader.QueryContext(ctx, `
SELECT i.base, i.multiplier, c.corr
FROM corr_cache c
JOIN instruments i ON i.id = c.instrument_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*float64{}
	for rows.Next() {
		var base string
		var multiplier int
		var corr sql.NullFloat64
		if err := rows.Scan(&base, &multiplier, &corr); err != nil {
			return nil, err
		}
		key := instrumentKey(domain.Instrument{Base: base, Multiplier: multiplier})
		if corr.Valid {
			value := corr.Float64
			out[key] = &value
		} else {
			out[key] = nil
		}
	}
	return out, rows.Err()
}

func (s *Store) UpsertCorr(ctx context.Context, instrument domain.Instrument, corr *float64, updatedAt time.Time) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	id, err := upsertInstrumentTx(ctx, tx, instrument)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO corr_cache(instrument_id, corr, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(instrument_id) DO UPDATE SET corr = excluded.corr, updated_at = excluded.updated_at`,
		id, nullableFloat(corr), formatTime(updatedAt)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) BootstrapUser(ctx context.Context, email, password string) (bool, error) {
	if email == "" || password == "" {
		return false, errors.New("bootstrap email and password are required")
	}
	var count int
	if err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	_, err = s.writer.ExecContext(ctx, `INSERT INTO users(email, password_hash) VALUES (?, ?)`, email, string(hash))
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	var created string
	err := s.reader.QueryRowContext(ctx, `SELECT id, email, password_hash, created_at FROM users WHERE email = ?`, email).
		Scan(&user.ID, &user.Email, &user.PasswordHash, &created)
	if err != nil {
		return User{}, err
	}
	user.CreatedAt, _ = parseTime(created)
	return user, nil
}

func (s *Store) CheckPassword(ctx context.Context, email, password string) (bool, error) {
	user, err := s.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *Store) ResetPassword(ctx context.Context, email, password string) error {
	if email == "" || password == "" {
		return errors.New("email and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	result, err := s.writer.ExecContext(ctx, `UPDATE users SET email = ?, password_hash = ? WHERE id = (SELECT id FROM users ORDER BY id LIMIT 1)`, email, string(hash))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		_, err = s.writer.ExecContext(ctx, `INSERT INTO users(email, password_hash) VALUES (?, ?)`, email, string(hash))
	}
	return err
}

func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
	_, _ = s.writer.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`)
	var count int
	err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count)
	return count > 0, err
}

func upsertInstrumentTx(ctx context.Context, tx *sql.Tx, instrument domain.Instrument) (int64, error) {
	if instrument.Base == "" || instrument.Multiplier <= 0 {
		return 0, fmt.Errorf("invalid instrument: %+v", instrument)
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO instruments(base, multiplier, binance_symbol, bybit_symbol, on_binance, on_bybit, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(base, multiplier) DO UPDATE SET
  binance_symbol = CASE WHEN excluded.binance_symbol <> '' THEN excluded.binance_symbol ELSE instruments.binance_symbol END,
  bybit_symbol = CASE WHEN excluded.bybit_symbol <> '' THEN excluded.bybit_symbol ELSE instruments.bybit_symbol END,
  on_binance = CASE WHEN excluded.on_binance <> 0 THEN excluded.on_binance ELSE instruments.on_binance END,
  on_bybit = CASE WHEN excluded.on_bybit <> 0 THEN excluded.on_bybit ELSE instruments.on_bybit END,
  updated_at = excluded.updated_at`,
		instrument.Base, instrument.Multiplier, instrument.BinanceSymbol, instrument.BybitSymbol,
		boolInt(instrument.OnBinance), boolInt(instrument.OnBybit), formatTime(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM instruments WHERE base = ? AND multiplier = ?`, instrument.Base, instrument.Multiplier).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func migrationVersion(name string) (int, error) {
	part := strings.SplitN(name, "_", 2)[0]
	return strconv.Atoi(part)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func instrumentKey(instrument domain.Instrument) string {
	return fmt.Sprintf("%s:%d", instrument.Base, instrument.Multiplier)
}
