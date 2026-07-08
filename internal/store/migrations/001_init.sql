CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS instruments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  base TEXT NOT NULL,
  multiplier INTEGER NOT NULL,
  binance_symbol TEXT NOT NULL DEFAULT '',
  bybit_symbol TEXT NOT NULL DEFAULT '',
  on_binance INTEGER NOT NULL DEFAULT 0,
  on_bybit INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE(base, multiplier)
);

CREATE TABLE IF NOT EXISTS prop_instruments (
  instrument_id INTEGER PRIMARY KEY,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS snapshot_rows (
  snapshot_id INTEGER NOT NULL,
  instrument_id INTEGER NOT NULL,
  rank INTEGER NOT NULL,
  price REAL NOT NULL,
  turnover_usd REAL NOT NULL,
  day_range_pct REAL NOT NULL,
  change_pct_24h REAL NOT NULL,
  funding_rate REAL NOT NULL,
  relative_tick_pct REAL NOT NULL,
  btc_corr_7d REAL,
  high_corr INTEGER NOT NULL,
  is_inplay INTEGER NOT NULL,
  in_prop_list INTEGER NOT NULL,
  bybit_only INTEGER NOT NULL,
  score REAL NOT NULL,
  PRIMARY KEY (snapshot_id, instrument_id),
  FOREIGN KEY (snapshot_id) REFERENCES snapshots(id) ON DELETE CASCADE,
  FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS corr_cache (
  instrument_id INTEGER PRIMARY KEY,
  corr REAL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
