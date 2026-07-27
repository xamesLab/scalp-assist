# Контур «Арбитраж»: техническое описание для кодового агента

**Версия:** 1.1

**Дата:** 27.07.2026

## 1. Назначение и границы

Контур наблюдает расхождения best bid/ask одинаковых линейных бессрочных
USDT-контрактов Binance, Bybit и OKX. Он показывает статистику направления,
spread после предполагаемых комиссий, доступную ёмкость первого уровня и
funding.

Это мониторинг, а не execution engine. В контуре запрещены:

- API keys торговых аккаунтов;
- создание, изменение и отмена ордеров;
- формулировки «гарантированная прибыль»;
- предположение, что REST BBO можно исполнить одновременно на двух биржах;
- включение funding в net spread без модели времени удержания.

Текущая v1 сравнивает только perpetual-perpetual USDT. Spot, dated futures,
межсетевые переводы, borrow и transfer latency вне скоупа.

## 2. Карта кода

| Зона | Пакет/файл | Ответственность |
|---|---|---|
| Запуск | `cmd/screener/main.go` | Общие exchange clients и запуск сервиса |
| Модели | `internal/arbitrage/model` | Settings, Opportunity, Run и Stats |
| Чистый расчёт | `internal/arbitrage/engine.go` | Grouping, направления, filters и ranking |
| Оркестрация | `internal/arbitrage/service.go` | Fetch, degraded mode, funding enrichment и persistence |
| Графики | `internal/arbitrage/charts.go` | Каталог общих инструментов, history bootstrap и общий WebSocket stream |
| Общие quote | `internal/domain/types.go` | `InstrumentKey`, `VenueQuote`, `FundingInfo` |
| Кэш | `internal/marketdata` | TTL cache, keyed cache и request coalescing |
| Нормализация | `internal/normalize.Resolver` | ContractSpec → identity |
| Binance | `internal/exchange/binance` | Catalog, BBO и funding |
| Bybit | `internal/exchange/bybit` | Catalog, BBO и funding из tickers |
| OKX | `internal/exchange/okx` | Catalog, BBO и точечный funding |
| Хранилище | `internal/store/arbitrage.go` | Runs, opportunities, stats и retention |
| Миграция | `internal/store/migrations/002_arbitrage.sql` | Таблицы контура |
| Миграция | `internal/store/migrations/003_arbitrage_maker_taker_net.sql` | Дополнительный Net maker+taker |
| API | `internal/api/api.go` | Current run, stats и refresh |
| UI | `web/src/main.jsx` | Вкладки List, Settings и Wiki в `/app/arbitrage/*` |

`engine.Scan` должен оставаться чистой функцией без HTTP и SQLite. Сетевые
решения принадлежат service/client, persistence — store.

## 3. Общий поток

```text
Binance Quotes ─┐
Bybit Quotes ───┼─ fetch concurrently
OKX Quotes ─────┘
        │
        v
normalize.Resolver
        │
        v
group by InstrumentKey
        │
        v
all directed exchange pairs
        │
        v
freshness/skew/spread/fees/capacity filters
        │
        v
top-N by net spread
        │
        ├─ targeted funding enrichment
        ├─ first_seen tracking
        ├─ current run in memory
        └─ sampled SQLite history
```

`Service.Start` делает первый scan сразу, затем запускает ticker с
`cycles.arbitrage_interval`.

## 4. Контракт QuoteClient

Каждая биржа реализует:

```go
type QuoteClient interface {
    Exchange() domain.Exchange
    Quotes(context.Context) ([]domain.VenueQuote, error)
}
```

Опционально клиент может реализовать точечную догрузку:

```go
type symbolFundingClient interface {
    SymbolFunding(context.Context, string) (domain.FundingInfo, error)
}
```

`VenueQuote` должен содержать:

- биржу и исходный символ;
- `ContractSpec` со status/type/base/quote;
- bid/ask price и quantity;
- `QuantityMultiplier`;
- timestamps;
- funding rate/interval/next time, если доступны без отдельного запроса.

Правило timestamp: `UpdatedAt` должен описывать свежесть рыночной котировки, а
не время загрузки каталога контрактов.

## 5. Биржи, payload и TTL

### Binance

- contracts: `/fapi/v1/exchangeInfo`, TTL 6 часов;
- 24h stats: `/fapi/v1/ticker/24hr`, TTL 1 минута;
- BBO: `/fapi/v1/ticker/bookTicker`, TTL 2 секунды;
- funding/mark: `/fapi/v1/premiumIndex`, TTL 1 минута.

`Quotes` объединяет catalog, BBO и funding. Funding interval сейчас принимается
равным 8 часам, потому что используемый payload не несёт индивидуальный
интервал.

### Bybit

- contracts: `/v5/market/instruments-info?category=linear`, cursor pagination,
  TTL 6 часов;
- tickers: `/v5/market/tickers?category=linear`, TTL 2 секунды.

Один ticker payload содержит BBO, sizes, mark, funding interval и next funding
time. Не делать второй запрос ради тех же полей.

### OKX

- contracts: `/api/v5/public/instruments?instType=SWAP`, TTL 6 часов;
- BBO: `/api/v5/market/tickers?instType=SWAP`, TTL 2 секунды;
- funding: `/api/v5/public/funding-rate?instId=...`, keyed TTL 1 минута.

OKX funding endpoint вызывается только для верхних кандидатов, а не для всех
сотен контрактов.

### Общий принцип

Binance и Bybit client instances общие со скринером. Создание нового экземпляра
в арбитражном service запрещено без причины: это отключает переиспользование
кэша. Одновременные cache miss одного ключа должны объединяться в один HTTP
fetch.

### WebSocket-графики

Графики используют публичные kline/candlestick streams без API keys:

- Binance: routed USDⓈ-M stream `<symbol>@kline_1m|5m`;
- Bybit: linear topic `kline.{1|5}.{symbol}`;
- OKX: business WebSocket channel `candle1m|candle5m`.

Начальная история загружается REST-методами klines, затем текущая свеча
обновляется через WebSocket. Значение линии — `close` биржевой свечи, а не BBO.
Один shared stream переиспользуется всеми браузерными подписчиками одинакового
`(instrument, interval)`. После обрыва upstream соединение восстанавливается с
backoff.

## 6. Идентичность контракта

Ключ арбитража строже скринера:

```text
InstrumentKey = base + quote + multiplier + kind
```

Текущий `kind`: `linear_perpetual`.

Примеры:

- `BTC/USDT x1 perpetual` совпадает между биржами;
- `PEPE x1` не совпадает с `PEPE x1000`;
- USDC не совпадает с USDT;
- perpetual не совпадает с dated future.

В scan допускается только identity с quote `USDT`. Resolver умеет USDC для
общей инфраструктуры, но арбитраж v1 намеренно отбрасывает его.

Stable bases и blacklist исключаются resolver. Override имеет формат:

```yaml
normalization_overrides:
  EXCHANGE_SYMBOL: "BASE:MULTIPLIER"
```

Критический инвариант: нельзя сопоставлять контракты только по видимой строке
base. Ошибка multiplier создаёт ложный spread на порядки величины.

## 7. Валидация исходной котировки

`validQuote` допускает quote только если:

- `bid_price > 0`;
- `ask_price > 0`;
- `ask_price >= bid_price`;
- status равен `TRADING` без учёта регистра;
- contract type содержит `PERPETUAL`;
- `UpdatedAt` не zero;
- возраст не больше `max_quote_age`;
- timestamp не находится в будущем дальше `max_quote_skew`.

После группировки инструмент должен присутствовать минимум на двух уникальных
биржах.

## 8. Направления и формулы

Для N бирж движок проверяет каждую упорядоченную пару:

```text
buy on A at ask
sell on B at bid
A != B
```

Для трёх доступных бирж максимум шесть направлений одного инструмента.

```text
gross_spread_bps =
    (sell_bid - buy_ask) / buy_ask * 10000

net_spread_bps =
    gross_spread_bps
  - fee_bps[buy_exchange]
  - fee_bps[sell_exchange]

net_maker_taker_bps =
    gross_spread_bps
  - maker_fee_bps[buy_exchange]
  - fee_bps[sell_exchange]
```

`net_spread_bps` предполагает taker+taker и остаётся основой фильтрации,
сортировки и статистики. `net_maker_taker_bps` — дополнительный сценарий:
покупающая биржа считается maker, продающая — taker. Исполнение maker-ордера не
гарантируется. Комиссии задаются в bps. Если карта не содержит биржу, helper
возвращает ноль. Дефолт содержит все три биржи, но validation проверяет только
переданные ключи и не отклоняет неполную карту. Поэтому не удалять
Binance/Bybit/OKX из `fees_bps` и `maker_fees_bps`.

### Capacity

```text
buy_capacity_usd =
    ask_quantity * buy_quantity_multiplier * buy_ask

sell_capacity_usd =
    bid_quantity * sell_quantity_multiplier * sell_bid

capacity_usd = min(buy_capacity_usd, sell_capacity_usd)
```

`QuantityMultiplier` приводит размер BBO к количеству базового актива. Если он
не положительный, engine использует 1. Для OKX значение строится из `ctVal`.

Capacity относится только к первому уровню и не учитывает slippage, latency,
minimum order size и округление по quantity step.

## 9. Фильтры возможности

После проверки исходных quote применяются:

1. `abs(buy.updated_at - sell.updated_at) <= max_quote_skew`;
2. `gross >= min_gross_spread_bps`;
3. `gross <= max_gross_spread_bps`;
4. `net >= min_net_spread_bps`;
5. `capacity >= min_capacity_usd`.

Верхний gross ceiling является sanity-фильтром против ошибочного сопоставления
символов и повреждённых данных. Не удалять его только потому, что очень большой
spread кажется интересным: сначала нужно доказать корректность contract mapping.

Результаты сортируются по `net_spread_bps desc`, затем по gross desc и
обрезаются до `top_n`.

## 10. Funding

Если funding обеих сторон и интервалы известны:

```text
funding_edge_bps_hour =
    (sell_funding_rate / sell_interval_hours
     - buy_funding_rate / buy_interval_hours) * 10000
```

Знак исходит из модели long на buy exchange и short на sell exchange:

- long платит положительный funding;
- short получает положительный funding;
- положительный edge выгоден выбранному направлению.

Funding показывается отдельно и не входит в `net_spread_bps`. Чтобы включить
его в ожидаемый PnL, нужна явная модель holding period и проверка, какие funding
timestamps реально пересекает позиция.

`Service.enrichFunding` рассматривает первые `funding_candidates` opportunities,
дедуплицирует `(exchange, symbol)` и выполняет максимум 5 запросов параллельно.
После enrichment hourly edge пересчитывается.

## 11. Сервис, деградация и first_seen

`Service.Refresh` защищён `TryLock`. Повторный refresh во время активного scan
возвращает `ErrRefreshInProgress`.

Все клиенты опрашиваются параллельно. Для каждой биржи создаётся
`ExchangeState` с `ok`, ошибкой, временем и количеством quote.

- 3 успешных биржи: полный режим;
- 2 успешных: degraded mode, scan продолжается;
- 0 или 1: run сохраняется в памяти со статусами, метод возвращает ошибку.

`FirstSeenAt` сохраняется, пока то же направление присутствует в
последовательных scan. Разрыв больше трёх `arbitrage_interval` начинает новую
серию. Ключ серии включает base, quote, multiplier, buy и sell exchange.

Current run хранится в памяти и клонируется при чтении, чтобы API не мог изменить
внутреннее состояние service.

## 12. История и retention

Не каждый 10-секундный scan пишется в SQLite. Persistence происходит:

- при первом успешном run;
- затем не чаще `history_interval`, по умолчанию 1 минута.

Таблицы `002_arbitrage.sql`:

- `arbitrage_runs` — observed time, JSON-состояния бирж, число общих
  инструментов и opportunities;
- `arbitrage_opportunities` — рассчитанные направления run.

Миграция `003_arbitrage_maker_taker_net.sql` добавляет nullable-колонку
`net_maker_taker_bps`. Для старых записей чтение использует `net_spread_bps` как
консервативный fallback.

Удаление выполняется по `arbitrage_runs.observed_at < cutoff`; дочерние строки
удаляются через `ON DELETE CASCADE`. Retention по умолчанию 30 дней.

API сначала возвращает in-memory run. Если сервис ещё не сделал scan, читается
последний сохранённый run из БД.

## 13. Конфигурация

```yaml
cycles:
  arbitrage_interval: 10s

arbitrage:
  min_gross_spread_bps: 1
  max_gross_spread_bps: 1000
  min_net_spread_bps: -20
  min_capacity_usd: 1000
  max_quote_age: 30s
  max_quote_skew: 10s
  top_n: 30
  funding_candidates: 20
  history_interval: 1m
  retention_days: 30
  fees_bps:
    binance: 5
    bybit: 5.5
    okx: 5
  maker_fees_bps:
    binance: 2
    bybit: 2
    okx: 2
```

Арбитражные настройки пока читаются только из YAML и требуют рестарта.

Валидация:

- min gross неотрицателен;
- max gross строго больше min gross;
- capacity неотрицательна;
- age/skew/history interval положительны;
- top-N `1..200`;
- funding candidates `0..top_n`;
- retention `1..3650`;
- известные taker- и maker-комиссии неотрицательны.

Значение `min_net_spread_bps = -20` позволяет наблюдать близкие к безубыточности
маршруты. Это не утверждение об их прибыльности.

## 14. API и UI

Все endpoints требуют авторизацию:

| Метод | Путь | Поведение |
|---|---|---|
| GET | `/api/arbitrage/opportunities?limit=` | Полный `Run`, limit обрезает opportunities |
| GET | `/api/arbitrage/stats?days=7` | Runs, observations, avg/max net, profitable count и routes |
| POST | `/api/arbitrage/refresh` | Синхронный внеплановый scan |
| GET | `/api/arbitrage/fees` | Текущие maker/taker-комиссии из конфигурации сервиса |
| GET | `/api/arbitrage/charts/instruments` | Контракты, одновременно доступные на всех трёх биржах |
| WS | `/api/arbitrage/charts/stream?instrument=&interval=` | History snapshot с сохранённым Gross и live candle/status/gross events |
| GET | `/api/dashboard/summary` | Краткие числа и до пяти top-строк каждого контура |

`POST /refresh` возвращает conflict при активном refresh или ошибке получения
минимум двух бирж.

UI использует общий для контуров компонент вкладок:

- `/app/arbitrage` — List с текущим рабочим содержимым;
- `/app/arbitrage/charts` — три линии close Binance/Bybit/OKX и нижняя панель Gross с периодом 1m или 5m;
- `/app/arbitrage/info` — настроенные maker/taker-комиссии и суммы M/T, T/T;
- `/app/arbitrage/settings` — пустая зарезервированная область;
- `/app/arbitrage/wiki` — русскоязычная пользовательская справка по List,
  столбцам, формулам и ограничениям.

Вкладка List показывает:

- статусы Binance/Bybit/OKX;
- время run и число общих инструментов;
- поиск;
- route и исходные symbols;
- buy ask, sell bid, gross/net bps;
- capacity;
- funding edge;
- длительность с `first_seen_at`.

Карточка Arbitrage на Overview показывает не более пяти строк с положительным
`net_spread_bps`: ticker, route, Net T/T и Active. API дополнительно возвращает
`positive_total`, чтобы UI мог указать количество остальных положительных строк.
Карточка Screener показывает не более пяти инструментов с максимальным Score и
число остальных строк последнего snapshot. Кнопка `Refresh cards` повторно
читает summary, но не запускает внеплановые рыночные scan.

Frontend не должен самостоятельно пересчитывать исполнимый spread: backend
является единственным источником формул и фильтров. Исключение — явно
обозначенный визуальный `Gross proxy` по close-ценам.

Charts не заменяет List: линии отражают last-trade close свечей, тогда как
возможности List рассчитываются по синхронизированным best ask/bid.
Нижняя панель всегда показывает лучший сохранённый в минутной корзине Gross,
рассчитанный из `sell_bid / buy_ask`. Опциональная линия Gross proxy считается
на frontend из максимального и минимального close трёх бирж; её можно скрыть
чекбоксом `Approximate Gross`. Разрывы сохранённого Gross ожидаемы: в таблицу
попадают только прошедшие фильтры opportunities с частотой `history_interval`.

При изменении колонок, формул или трактовки funding одновременно обновлять
`ArbitrageWikiPage`, чтобы пользовательская справка не расходилась с backend.

## 15. Наблюдаемость и известные ограничения

В логах должны быть видны ошибки отдельной биржи, неудачная funding-догрузка,
ошибка persistence и cleanup. Ошибка funding enrichment не должна удалять
ценовую opportunity.

Известные ограничения v1:

- REST polling вместо WebSocket;
- timestamps бирж имеют разную точность и семантику;
- только top-level BBO;
- нет slippage и order constraints;
- Binance funding interval предполагается 8h;
- комиссии статические и не учитывают VIP tier;
- нет transfer/withdrawal constraints;
- stats агрегируют сохранённые sampled runs, а не каждый in-memory scan.

Эти ограничения должны быть видны в продуктовой документации при любых
изменениях, связанных с трактовкой «прибыли».

## 16. Тестирование и правила расширения

Минимальный прогон:

```powershell
go test ./internal/marketdata
go test ./internal/exchange/binance ./internal/exchange/bybit ./internal/exchange/okx
go test ./internal/normalize ./internal/arbitrage ./internal/store ./internal/api
go test ./...
```

Обязательные тесты:

- fixture parsing всех бирж;
- Bybit pagination и OKX retCode/code errors;
- cache TTL и concurrent fetch coalescing;
- два направления пары;
- fee subtraction;
- quantity multiplier и capacity;
- stale/future quote и interexchange skew;
- status/type/quote/multiplier mismatch;
- min/max gross, min net и capacity filters;
- funding edge с разными интервалами;
- работа с двумя биржами и отказ при одной;
- refresh mutex;
- first_seen continuity;
- history interval, migration, round-trip и retention;
- API auth, empty DB, limit и stats.
- chart catalog intersection, REST history и WebSocket auth;
- parsing kline WebSocket payload Binance/Bybit/OKX;
- shared subscription, update текущей свечи и reconnect status.

Live-smoke на машине владельца выполнять через:

```powershell
C:\Windows\System32\cmd.exe /c curl ...
```

Перед добавлением WebSocket не удалять REST-клиенты: они нужны для bootstrap и
fallback. Рекомендуемая следующая архитектура — один multiplexed stream на
биржу, общий in-memory quote hub и тот же чистый `engine.Scan`.
