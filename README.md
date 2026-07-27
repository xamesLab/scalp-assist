# Scalp Assist

Self-hosted information service for futures trading research. It has two private
contours behind one account:

- **Screener** ranks Binance and Bybit USDT/USDC perpetuals using volume,
  volatility, market-relative movement, BTC correlation and a prop-company list.
- **Arbitrage** monitors cross-exchange USDT perpetual spreads between Binance,
  Bybit and OKX. It shows statistics only and never sends orders.

The public `/` route is a landing page. After login, `/app` opens a shared
dashboard with links and summary data for both contours.

## Local development

Requirements: Go 1.22+, Node.js/npm and Bash (Git Bash works on Windows).

```bash
./dev.sh
```

The script creates `config.yaml` from `config.example.yaml` when needed, installs
frontend dependencies and starts:

- frontend: `http://127.0.0.1:5173`
- API: `http://127.0.0.1:8080`

Environment overrides:

```bash
CONFIG=config.example.yaml BACKEND_ADDR=127.0.0.1:18080 FRONTEND_PORT=5174 ./dev.sh
```

PowerShell equivalents:

```powershell
go run ./cmd/screener -config config.example.yaml
cd web
npm install
$env:VITE_API_TARGET = "http://127.0.0.1:8080"
npm run dev
```

## Checks

```bash
go test ./...
go build ./cmd/screener
cd web && npm run build
```

Live exchange checks on the owner's Windows machine must run through
`C:\Windows\System32\cmd.exe` so they use the configured VPN. See
`specs/Coder_agent_instructions.md`.

## Routes

Public: `/`, `/auth`.

Private: `/app`, `/app/screener`, `/app/screener/history`,
`/app/screener/settings`, `/app/screener/wiki`, `/app/arbitrage`,
`/app/arbitrage/settings`, `/app/arbitrage/wiki`.

Legacy screener routes under `/app/top`, `/app/history` and `/app/settings`
remain available for compatibility.

Production uses the configuration in `config.yaml`; local runtime files and
build outputs are ignored by Git. Deployment is described in
`specs/Deploy_VPS_guide.md`.

Detailed implementation guides for coding agents:

- `specs/Screener_contour_agent_guide.md`
- `specs/Arbitrage_contour_agent_guide.md`
