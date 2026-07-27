# Контур «Скринер»: техническое описание для кодового агента

**Версия:** 1.0

**Дата:** 27.07.2026

## 1. Назначение и границы

Скринер автоматически отбирает линейные фьючерсные инструменты Binance и Bybit
для последующего ручного анализа скальпером. Он объединяет данные двух бирж,
применяет жёсткие фильтры, рассчитывает метрики и score, формирует top-N и
сохраняет историю.

Контур информационный. Он не открывает графики, не оценивает уровни и новости,
не формирует торговые команды и не использует приватные API бирж.

OKX сейчас используется только арбитражным контуром. Не добавлять данные OKX в
скринер без отдельного продуктового решения, потому что это меняет оборот,
медиану рынка, перцентили score и историческую сопоставимость.

## 2. Карта кода

| Зона | Пакет/файл | Ответственность |
|---|---|---|
| Запуск | `cmd/screener/main.go` | Создание клиентов, store, scheduler и API |
| Биржевые данные | `internal/exchange/binance`, `internal/exchange/bybit` | REST, retry, decode, rate-limit metadata и кэши |
| Общие модели | `internal/domain/types.go` | `Instrument`, `Ticker`, `MarketSnapshot`, `MetricsRow`, `Settings` |
| Нормализация | `internal/normalize` | Сведение биржевых символов к base+multiplier |
| Метрики | `internal/metrics` | Приоритет источника, фильтры, turnover, range, tick и inplay |
| Скоринг | `internal/scoring` | Перцентили, score, сортировка и rank |
| Корреляция | `internal/corr` | Klines, выравнивание рядов, доходности и Pearson |
| Оркестрация | `internal/scheduler` | Быстрый и медленный циклы, degraded mode и mutex |
| Хранилище | `internal/store` | Снапшоты, prop list, settings, corr cache и статистика |
| API | `internal/api/api.go` | Screener endpoints и общая авторизация |
| UI | `web/src/main.jsx` | Top, history, settings и prop-list import |

Скринер не должен импортировать пакеты `internal/arbitrage/*`. Общими между
контурами остаются только нейтральные domain-модели, exchange-клиенты,
нормализация, HTTP helper и market-data cache.

## 3. Основной поток данных

```text
Binance Snapshot ─┐
                  ├─ normalize.FromSnapshots
Bybit Snapshot ───┘
          │
          v
metrics.BuildRows
          │
          ├─ corr cache
          ├─ prop list flags
          v
scoring.Top
          │
          v
store.SaveSnapshot
```

### 3.1. Быстрый цикл

`scheduler.Refresh` запускается сразу при старте и затем через
`cycles.fast_interval`, по умолчанию каждые 15 минут.

Порядок строго такой:

1. Прочитать runtime-overrides из таблицы `settings`.
2. Построить эффективные настройки через `config.Effective`.
3. Параллельно запросить `Snapshot` Binance и Bybit.
4. Если обе биржи недоступны, завершить цикл ошибкой без нового снапшота.
5. Нормализовать contracts/tickers.
6. Построить строки метрик и применить жёсткие фильтры.
7. Подмешать последнее значение корреляции из `corr_cache`.
8. Проставить `in_prop_list`.
9. Рассчитать score, отсортировать и ограничить top-N.
10. Атомарно сохранить снапшот.

Отказ одной биржи допустим: цикл строит результат по второй и отражает
деградацию в `scheduler.Status`.

`POST /api/screener/refresh` вызывает тот же `Refresh`. `TryLock` запрещает два
одновременных прогона; конкурентный запрос получает conflict.

### 3.2. Медленный цикл

`scheduler.RecomputeCorr` запускается сразу и затем через
`cycles.slow_interval`, по умолчанию каждые 2 часа.

Он читает строки последнего сохранённого top-снапшота, загружает 168 часовых
свечей инструмента и BTC с той же биржи, рассчитывает корреляцию и обновляет
`corr_cache`. Binance приоритетен; Bybit используется для Bybit-only.

Важное следствие: новое значение corr влияет на score не в уже сохранённом
снапшоте, а в следующем быстром цикле. Корреляция рассчитывается только для
инструментов последнего top, а не для всей предварительной выборки.

## 4. Биржевые источники и переиспользование запросов

### Binance USDⓈ-M

- `/fapi/v1/exchangeInfo` — contracts/status/tick size, TTL 6 часов;
- `/fapi/v1/ticker/24hr` — цена, high/low, change, quote volume, TTL 1 минута;
- `/fapi/v1/premiumIndex` — funding, TTL 1 минута;
- `/fapi/v1/klines` — корреляция.

### Bybit v5 linear

- `/v5/market/instruments-info` — cursor-пагинация, TTL каталога 6 часов;
- `/v5/market/tickers` — market payload, TTL 2 секунды;
- `/v5/market/kline` — корреляция; ответ сортируется по времени ascending.

Клиенты Binance и Bybit создаются один раз в `main` и одновременно передаются
скринеру и арбитражу. Не создавать второй client instance внутри scheduler:
это обойдёт общий TTL-кэш и удвоит запросы.

## 5. Нормализация

Идентичность скринера:

```text
Instrument = base + multiplier
```

Примеры:

| Биржевой символ | Результат |
|---|---|
| `BTCUSDT` | `BTC x1` |
| `1000PEPEUSDT` | `PEPE x1000` |
| `SHIB1000USDT` | `SHIB x1000` |

Поддерживаемые quotes: USDT и USDC. Quote не входит в ключ `Instrument`
скринера, поэтому при расширении на новые quotes нужно сначала решить, как
избежать коллизий и двойного учёта.

Автоматически распознаются множители `10`, `100`, `1000`, `10000`, `1000000`
как prefix или suffix base. Ручной override:

```yaml
normalization_overrides:
  1000PEPEUSDT: "PEPE:1000"
```

Stable bases и `blacklist` исключаются. Неизвестный символ пропускается с
warning, а не останавливает весь цикл.

Критический инвариант: `PEPE x1` и `PEPE x1000` — разные инструменты. Нельзя
сливать их только по очищенному base.

## 6. Метрики и фильтры

`metrics.BuildRows` сначала выбирает источник:

- Binance ticker, если инструмент есть на Binance;
- иначе Bybit ticker.

Оборот является исключением и суммируется:

```text
turnover_usd = binance_turnover_usd + bybit_turnover_usd
day_range_pct = (high_24h - low_24h) / low_24h * 100
relative_tick_pct = tick_size / price * 100
bybit_only = on_bybit && !on_binance
```

Строка отбрасывается, если:

- base stable/blacklisted;
- нет ticker;
- статус не Trading;
- `low_24h <= 0` или `price <= 0`;
- turnover ниже `min_volume_usd`;
- включён change filter и `abs(change_pct_24h) < min_change_pct`.

### Inplay

Медиана рассчитывается по изменениям всех нормализованных валидных Trading
инструментов до volume/change-фильтров, но после stable/blacklist-фильтра.

```text
market_active = median_change >= market_threshold_pct
is_inplay = market_active &&
            (asset_change >= median_change * multiplier || asset_change < 0)
```

При отрицательной или слабой медиане рынка inplay всегда false.

## 7. Корреляция с BTC

Параметры по умолчанию:

- interval: `1h`;
- limit: 168 свечей;
- минимум: 100 выровненных доходностей.

Алгоритм:

1. Сопоставить свечи инструмента и BTC по `OpenTime`.
2. Удалить точки без пары и цены `<= 0`.
3. Рассчитать доходности `close[i]/close[i-1]-1`.
4. Рассчитать Pearson.
5. Вернуть `nil`, если точек мало или дисперсия нулевая.

`nil` означает «данных недостаточно», а не нулевую корреляцию.

## 8. Скоринг

Волатильность и turnover преобразуются в перцентили текущей отфильтрованной
выборки `[0..1]`. Одинаковые значения получают средний rank.

```text
score =
    w_volatility * percentile(day_range_pct)
  + w_turnover   * percentile(turnover_usd)
  + w_inplay     * bool(is_inplay)
  - w_corr       * btc_corr_7d
```

Corr penalty применяется только когда corr известна и `is_inplay == false`.
В текущей реализации `high_corr` также выставляется только в этой ветке. При
inplay corr penalty отменяется и `high_corr` остаётся false.

Сортировка:

1. score descending;
2. turnover descending;
3. base ascending;
4. multiplier ascending.

После ограничения `top_n` rank начинается с 1.

## 9. Конфигурация

YAML defaults:

```yaml
cycles:
  fast_interval: 15m
  slow_interval: 2h

settings:
  min_volume_usd: 100000000
  min_change_pct: 20
  enable_change_filter: true
  corr_threshold: 0.8
  inplay:
    market_threshold_pct: 3
    multiplier: 2
  weights:
    volatility: 0.4
    turnover: 0.3
    inplay: 0.2
    corr: 0.1
  top_n: 20
```

Через UI/API можно менять только runtime `settings`. Интервалы, blacklist и
normalization overrides требуют изменения YAML и рестарта.

`config.Effective` применяет overrides к копии defaults и валидирует итог
целиком. При ошибке не должно быть частично применённого набора.

## 10. Хранилище

Основные таблицы миграции `001_init.sql`:

- `instruments` — base/multiplier и символы Binance/Bybit;
- `snapshots` — время быстрого цикла;
- `snapshot_rows` — top и все показанные метрики;
- `corr_cache` — последняя корреляция инструмента;
- `prop_instruments` — prop membership и note;
- `settings` — строковые runtime-overrides;
- `users` — общая авторизация.

Историческая строка должна быть самодостаточной. Нельзя при чтении старого
снапшота пересчитывать его score по текущим настройкам.

## 11. API и UI

Защищённые endpoints:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/screener/top?limit=` | Последний top |
| GET | `/api/screener/top?format=text` | Текстовое представление |
| GET | `/api/screener/history?symbol=&days=` | История инструмента |
| GET | `/api/screener/snapshots?from=&to=` | Список снапшотов |
| GET | `/api/screener/stats?days=` | Агрегаты истории |
| GET/PUT | `/api/screener/prop-list` | Полная замена prop list |
| GET/PUT | `/api/screener/settings` | Runtime-настройки |
| POST | `/api/screener/refresh` | Внеплановый быстрый цикл |

Legacy endpoints без `/screener` пока сохранены для совместимости.

UI-маршруты: `/app/screener`, `/app/screener/history`,
`/app/screener/settings`, `/app/screener/wiki`. Wiki содержит русскоязычное
описание workflow, сводных показателей, колонок, раскрытой строки, score и
связанных страниц. При изменении метрик или UI одновременно обновлять
`ScreenerWikiPage`.

TXT import prop list реализован только на frontend: он парсит файл, объединяет
элементы с текущим списком и отправляет полный PUT.

## 12. Ошибки, тесты и правила изменений

Минимальные проверки при изменении контура:

```powershell
go test ./internal/exchange/... ./internal/normalize ./internal/metrics
go test ./internal/scoring ./internal/corr ./internal/scheduler
go test ./internal/store ./internal/api
go test ./...
```

Обязательные fixture-сценарии:

- изменение формата ответа биржи;
- multiplier/override/stable/blacklist;
- Binance priority и Bybit-only fallback;
- volume/change/invalid price filters;
- inplay на растущем рынке;
- corr `1`, `-1`, недостаток точек и пропуски времени;
- score ties и top-N;
- отказ одной и обеих бирж;
- concurrent refresh;
- round-trip снапшота и settings.

При добавлении метрики агент должен синхронно изменить domain model, расчёт,
storage migration/read-write, API JSON, UI и тест round-trip. При изменении
скоринга нужно явно учитывать, нарушает ли оно сравнимость исторических score.
