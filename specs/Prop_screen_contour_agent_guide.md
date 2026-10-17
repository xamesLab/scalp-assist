# Контур «Prop screen»: техническое описание для кодового агента

**Версия:** 1.0

**Дата:** 29.07.2026

## 1. Назначение

Prop screen показывает монеты из общей prop list, для которых найден активный
линейный perpetual-контракт на Binance или Bybit. Для каждой строки выводятся
изменение цены и оборот за 24 часа.

Контур информационный. Он использует только публичные market endpoints, не
хранит API keys и не отправляет ордера.

## 2. Карта кода

| Зона | Пакет/файл | Ответственность |
|---|---|---|
| Модели и сервис | `internal/propscreen` | Join prop list с рыночными данными, fallback и current run |
| Binance | `internal/exchange/binance` | `MarketSnapshot`: каталог и 24h tickers |
| Bybit | `internal/exchange/bybit` | `MarketSnapshot`: каталог и 24h tickers |
| Нормализация | `internal/normalize` | Base, multiplier, blacklist и overrides |
| Хранилище списка | `internal/store` | Существующая таблица `prop_instruments` |
| API | `internal/api/api.go` | Current run и ручной refresh |
| UI | `web/src/main.jsx` | `/app/prop-screen` |

Новая таблица БД не нужна. Последний run хранится в памяти и восстанавливается
немедленным refresh после старта.

## 3. Поток данных

```text
prop_instruments ─────────────────────────┐
                                         ├─ exact/base matching ── rows
Binance catalog + 24h tickers ─ normalize┤
Bybit catalog + 24h tickers ─── normalize┘
```

Сервис запускается сразу и далее через `cycles.prop_screen_interval`, по
умолчанию раз в минуту. `Refresh` защищён `TryLock`. Корреляция пересчитывается
отдельным медленным циклом с интервалом `cycles.slow_interval`; при обычном
refresh значения читаются из общей таблицы `corr_cache`.

## 4. Выбор контракта и источника

В расчёт входят только:

- status `TRADING`;
- contract type, содержащий `PERPETUAL`;
- quote USDT или USDC;
- контракты, прошедшие общий resolver.

Внутри одной биржи USDT имеет приоритет над USDC. Сначала prop entry
сопоставляется по точному `base + multiplier`.

Для обычной записи `PEPE x1`, если точного контракта нет, допускается fallback
на `1000PEPE`, только когда для base найден ровно один multiplier и этот
контракт не заявлен отдельной prop entry. При нескольких multiplier контур не
угадывает соответствие и пропускает строку.

Источник метрик:

1. Binance, если найден валидный Binance-контракт;
2. иначе Bybit.

При временной ошибке Binance валидная строка Bybit используется как degraded
fallback. Ответ содержит состояния обеих бирж и явно указывает
`source_exchange`.

## 5. Поля строки

- `instrument` — исходная prop entry и фактическая доступность бирж;
- `source_exchange` — источник метрик;
- `source_symbol` — исходный биржевой символ;
- `contract_multiplier` — множитель выбранного контракта;
- `change_pct_24h` — изменение за 24 часа;
- `turnover_usd` — quote turnover за 24 часа;
- `btc_corr_7d` — Pearson-корреляция часовых доходностей с BTC за 7 дней;
- `updated_at` — timestamp ticker payload.

Строки без валидного контракта на обеих биржах не показываются.

## 6. API и UI

Оба endpoint защищены общей JWT-сессией:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/prop-screen` | Последний run; до первого run выполняет refresh |
| POST | `/api/prop-screen/refresh` | Синхронное внеплановое обновление |

UI содержит вкладки `/app/prop-screen` (List) и `/app/prop-screen/wiki` (Wiki).
List показывает статусы бирж, размер prop list, число найденных строк, источники
Binance/Bybit, поиск, нижнюю границу модуля 24h change, нижнюю границу turnover
и сортируемую таблицу со столбцом `BTC corr 7d`. Wiki объясняет workflow,
столбцы, диапазоны корреляции, `n/a` и ограничения интерпретации.

`/api/dashboard/summary` и Overview содержат краткую карточку: число найденных
монет, размер prop list, состояния бирж и распределение источников метрик.

Редактирование prop list остаётся на `/app/screener/settings`. После полного PUT
списка API запускает фоновое обновление prop-screen.

## 7. Правила изменений и тесты

Binance и Bybit clients создаются только в `main` и переиспользуются всеми
контурами. `MarketSnapshot` не должен загружать funding или BBO.

Минимальные проверки:

```powershell
go test ./internal/propscreen
go test ./internal/exchange/binance ./internal/exchange/bybit
go test ./internal/api ./internal/config
go test ./...
Set-Location web; npm.cmd run build
```

Обязательные сценарии: Binance priority, Bybit fallback, USDT priority,
multiplier fallback и ambiguity, degraded exchange, refresh mutex, auth и
обновление после изменения prop list, чтение corr cache и медленный пересчёт.
