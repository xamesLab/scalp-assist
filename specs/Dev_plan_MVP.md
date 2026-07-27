# План разработки Scalp Assist

**Версия:** 2.0
**Дата:** 27.07.2026

Документ отражает фактическое состояние реализации. Исходный MVP скринера
сохранён как этапы 0–10. Арбитраж добавлен отдельной фазой, но использует общую
инфраструктуру монолита.

## Архитектурная карта

```text
exchange adapters + marketdata cache + normalize
                 /                 \
          screener contour     arbitrage contour
                 \                 /
              store + API + auth
                      |
            dashboard + contour pages
```

## Фаза A: скринер MVP

### Этап 0 — Каркас проекта

**Статус:** готово.

Go module, YAML config, JSON logging, domain types, Makefile и структура пакетов.

### Этап 1 — Клиенты Binance и Bybit

**Статус:** готово.

Публичные REST-клиенты, retry/backoff, rate-limit metadata, fixtures и
integration smoke.

### Этап 2 — Нормализация

**Статус:** готово.

Base/multiplier identity, USDT/USDC, blacklist, stable filtering и overrides.

### Этап 3 — Метрики и фильтры

**Статус:** готово.

Оборот, диапазон, изменение, funding, relative tick, inplay и жёсткие фильтры.

### Этап 4 — Скоринг

**Статус:** готово.

Перцентильный score, top-N, штраф корреляции и исключение для inplay.

### Этап 5 — Корреляция BTC

**Статус:** готово.

1h candles, выравнивание рядов, Pearson returns и кэш.

### Этап 6 — SQLite

**Статус:** готово.

WAL, встроенные миграции, снапшоты, prop list, settings, users и corr cache.

### Этап 7 — REST API и авторизация

**Статус:** готово.

Top/history/stats/settings/refresh, login/logout/me, JWT cookie, bcrypt и
rate-limit.

### Этап 8 — Scheduler

**Статус:** готово.

Fast/slow cycles, refresh mutex, graceful shutdown и degraded exchange mode.

### Этап 9 — Frontend

**Статус:** готово.

Публичный landing, auth, screener top/history/settings, темы и адаптив.
Prop list поддерживает TXT import и свёрнут по умолчанию.

### Этап 10 — Сборка и деплой

**Статус:** реализовано в коде, production-приёмка выполняется владельцем.

Frontend встраивается в бинарник; HTTPS/autocert, systemd и backup описаны в
`Deploy_VPS_guide.md`.

## Фаза B: общий dashboard и арбитраж v1

### Этап 11 — Общий слой market data

**Статус:** готово.

- Добавлены нейтральные `InstrumentKey`, `VenueQuote`, `FundingInfo`.
- Нормализация вынесена в повторно используемый resolver.
- Добавлены TTL cache и keyed cache с coalescing конкурентных fetch.
- Binance/Bybit клиенты переиспользуются между контурами.

Проверка: конкурентный тест подтверждает один fetch на один cache key.

### Этап 12 — OKX adapter

**Статус:** готово.

Public instruments/tickers/funding-rate, mapping linear SWAP, contract value,
BBO и fixtures.

Проверка: unit fixtures + live запрос через системный `cmd.exe`.

### Этап 13 — Движок арбитража

**Статус:** готово.

- Exact identity: base, quote, multiplier, kind.
- Все направленные комбинации трёх бирж.
- Gross/net bps, комиссии, top-level capacity.
- Stale/skew/status/sanity/capacity filters.
- Funding edge per hour.

Проверка: табличные тесты направления, комиссий, capacity, stale quote,
временного skew и несовпадающего multiplier.

### Этап 14 — Сервис и история

**Статус:** готово.

- Параллельный fetch бирж.
- Работа при любых двух источниках.
- Targeted funding enrichment верхних кандидатов.
- Immediate + interval scan, refresh mutex.
- Minute-level persistence и retention.
- Миграция `002_arbitrage.sql`.

Проверка: service/store tests, повторное применение миграций, restart-safe DB.

### Этап 15 — API и frontend контуров

**Статус:** готово.

- `/api/dashboard/summary`.
- `/api/arbitrage/opportunities`, `/stats`, `/refresh`.
- Главная приватная `/app`.
- Отдельные `/app/screener/*` и `/app/arbitrage`.
- Legacy screener routes сохранены.

Проверка: API tests, `npm run build`, auth/live API smoke.

## Фаза C: следующие улучшения

### Этап 16 — WebSocket BBO

**Статус:** запланировано.

Добавить один multiplexed public stream на биржу, хранить последнюю котировку в
общем in-memory hub и оставить REST как bootstrap/fallback. Нужны reconnect,
heartbeat, sequence validation и метрика возраста данных.

### Этап 17 — Глубина и slippage

**Статус:** запланировано.

Для выбранных кандидатов догружать несколько уровней order book и считать
исполняемую ёмкость для заданных размеров позиции. Не скачивать полную глубину
для всего рынка.

### Этап 18 — Аналитика истории

**Статус:** запланировано.

График spread, длительность непрерывной возможности, медиана/квантили,
доступность маршрута и экспорт CSV.

### Этап 19 — Настройки и уведомления

**Статус:** запланировано.

UI для fee assumptions и порогов арбитража, затем уведомления. Автоматическое
исполнение сделок остаётся вне скоупа.

## Обязательные проверки релиза

```powershell
go test ./...
go build ./cmd/screener
Set-Location web
npm.cmd run build
```

Live-smoke выполнять через `C:\Windows\System32\cmd.exe` по правилам
`Coder_agent_instructions.md`. После деплоя обязательны 48 часов наблюдения,
проверка деградации источников, стабильности памяти и роста SQLite.
