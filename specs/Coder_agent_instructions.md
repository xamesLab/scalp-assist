# Инструкция для кодового агента

**Версия:** 2.0
**Дата:** 27.07.2026

## Контекст проекта

`scalp-assist` — один self-hosted веб-сервис с общей авторизацией и независимыми
информационными контурами:

- скринер Binance/Bybit;
- статистика межбиржевого арбитража Binance/Bybit/OKX.

Главные документы:

- `specs/PRD_trading_assistant.md`
- `specs/Dev_plan_MVP.md`
- `specs/Deploy_VPS_guide.md`
- `specs/Summary_описание_проекта.md`
- `specs/Screener_contour_agent_guide.md` — подробные инварианты скринера
- `specs/Arbitrage_contour_agent_guide.md` — подробные инварианты арбитража

При конфликте PRD и Dev plan выше старого HTML-мокапа. Мокап задаёт визуальный
язык скринера, но не актуальную информационную архитектуру всего приложения.

## Архитектурные правила

- Сохранять один Go-процесс и одну SQLite БД, пока нагрузка не требует
  обоснованного разделения.
- Контуры разделять по доменным пакетам, сервисам, API namespace и таблицам.
- Общими оставлять exchange adapters, HTTP/retry, rate-limit metadata,
  нормализацию контрактов и market-data cache.
- Один экземпляр клиента Binance/Bybit должен инжектироваться во все контуры.
  Не создавать отдельные клиенты внутри scheduler/service.
- Перед добавлением биржевого запроса проверять, нельзя ли переиспользовать уже
  полученный payload. Каталоги имеют длинный TTL, all-market tickers/BBO —
  короткий TTL; конкурентные cache miss должны coalesce в один запрос.
- Скринер не должен зависеть от типов арбитража и наоборот. Общими могут быть
  только нейтральные типы `domain`/`marketdata`.
- Арбитраж остаётся read-only: никаких API-ключей, ордеров и формулировок,
  обещающих исполнимую прибыль.

## Живые запросы к биржам

На Windows-машине владельца VPN применяется к запросам через системный
`cmd.exe`. Все ручные live-smoke команды выполнять именно так:

```powershell
C:\Windows\System32\cmd.exe /c curl -s -i https://fapi.binance.com/fapi/v1/time
C:\Windows\System32\cmd.exe /c curl -s -i https://api.bybit.com/v5/market/time
C:\Windows\System32\cmd.exe /c curl -s -i "https://www.okx.com/api/v5/public/time"
```

Успех: HTTP 200; Binance содержит `serverTime`, Bybit — `retCode: 0`, OKX —
`code: "0"`. Обычный PowerShell/Go-процесс может обойти VPN-маршрут и получить
451/403, поэтому его результат не использовать для вывода о доступности API.

Обычные тесты не должны требовать сеть. Live-тесты держать под build tag
`integration`, а ответы контрактов проверять через fixtures/`httptest`.

## Frontend routing

- `/` — публичный лендинг.
- `/auth` — вход.
- `/app` — главная приватная сводка по контурам.
- `/app/screener`, `/app/screener/history`, `/app/screener/settings` — рабочие
  страницы скринера.
- `/app/screener/wiki` — русскоязычная справка; обновлять вместе с колонками,
  формулой score и трактовкой флагов.
- `/app/arbitrage` — List арбитража.
- `/app/arbitrage/settings` — зарезервированная пустая вкладка Settings.
- `/app/arbitrage/wiki` — реализованная русскоязычная справка; синхронизировать
  её с таблицей и формулами при изменении расчётов.
- После login переходить на `/app`.
- При 401 переходить на `/auth`.
- После logout переходить на `/`.

Старые `/app/top`, `/app/history`, `/app/settings` пока сохраняются для
совместимости, но новые ссылки должны использовать namespace `/app/screener/*`.

## Проверки перед завершением

```powershell
$env:GO111MODULE='on'; go test ./...
$env:GO111MODULE='on'; go build -o $env:TEMP\screener-check.exe ./cmd/screener
Set-Location web; npm.cmd run build; Set-Location ..
git diff --check
git status --short
```

Для арбитража отдельно проверить fixtures всех трёх бирж, деградацию при отказе
одной биржи, stale/skew/multiplier filters, комиссии, capacity и миграцию БД.
Не коммитить runtime БД, JWT secret, бинарники и пользовательские списки.
