CREATE TABLE IF NOT EXISTS arbitrage_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  observed_at TEXT NOT NULL,
  exchange_state_json TEXT NOT NULL,
  instruments INTEGER NOT NULL,
  opportunities INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS arbitrage_opportunities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL,
  base TEXT NOT NULL,
  quote TEXT NOT NULL,
  multiplier INTEGER NOT NULL,
  kind TEXT NOT NULL,
  buy_exchange TEXT NOT NULL,
  sell_exchange TEXT NOT NULL,
  buy_symbol TEXT NOT NULL,
  sell_symbol TEXT NOT NULL,
  buy_ask REAL NOT NULL,
  sell_bid REAL NOT NULL,
  gross_spread_bps REAL NOT NULL,
  net_spread_bps REAL NOT NULL,
  capacity_usd REAL NOT NULL,
  buy_funding_rate REAL,
  sell_funding_rate REAL,
  buy_funding_interval_hours REAL NOT NULL DEFAULT 0,
  sell_funding_interval_hours REAL NOT NULL DEFAULT 0,
  buy_next_funding_time TEXT NOT NULL DEFAULT '',
  sell_next_funding_time TEXT NOT NULL DEFAULT '',
  funding_edge_bps_hour REAL,
  buy_quote_at TEXT NOT NULL,
  sell_quote_at TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES arbitrage_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_arbitrage_runs_observed_at
  ON arbitrage_runs(observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_arbitrage_opportunities_run
  ON arbitrage_opportunities(run_id, net_spread_bps DESC);

CREATE INDEX IF NOT EXISTS idx_arbitrage_opportunities_observed
  ON arbitrage_opportunities(observed_at DESC);
