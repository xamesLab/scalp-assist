# PRD: Scalp Assist

**Версия:** 1.1

**Дата:** 29.07.2026
**Статус:** скринер MVP, prop-screen и арбитраж v1 реализованы

Связанные документы: `Dev_plan_MVP.md`, `Deploy_VPS_guide.md`,
`Summary_описание_проекта.md`, `Coder_agent_instructions.md`,
`Screener_contour_agent_guide.md`, `Prop_screen_contour_agent_guide.md`,
`Arbitrage_contour_agent_guide.md`.

## 1. Продукт

Scalp Assist — персональный self-hosted веб-сервис для исследования рынка
фьючерсов. Под одной авторизацией он объединяет независимые информационные
контуры и общую краткую сводку.

### 1.1. Реализованные контуры

**Скринер:** сканирует Binance USDⓈ-M и Bybit linear, применяет правила
методички RDNSCALO, формирует top-N и сохраняет историю.

**Prop screen:** соединяет общую prop list с активными Binance/Bybit
perpetual-контрактами и показывает Binance-first изменение и оборот за 24 часа.

**Арбитраж:** сравнивает best bid/ask одинаковых USDT perpetual-контрактов
Binance, Bybit и OKX, оценивает spread после комиссий, ёмкость первого уровня и
funding. Контур предоставляет только статистику.

**Charts:** показывает до десяти свечных графиков Binance/Bybit в сетке из двух
столбцов. Раскладка, монеты и таймфреймы являются локальной настройкой браузера.

### 1.2. Пользователь и доступ

В первой версии один владелец. `/` и `/auth` публичны. Все данные и рабочие
экраны под `/app/*` защищены JWT-сессией. После login открывается `/app`, после
logout — `/`.

### 1.3. Вне скоупа

- автоматическая торговля и хранение exchange API keys;
- обещание исполнимости или прибыльности найденного spread;
- spot/perpetual и межсетевой арбитраж;
- самостоятельная регистрация и мультипользовательский data scope;
- Telegram/Hermes;
- realtime WebSocket и полный order book в арбитраже v1.

## 2. Цели

| Цель | Критерий |
|---|---|
| Полезный скринер | Актуальный top-N, история, prop flags и runtime-настройки |
| Наблюдение prop list | Все доступные prop-монеты и актуальные 24h change/turnover |
| Наблюдение арбитража | Направления между тремя биржами с gross/net/capacity/funding |
| Экономные запросы | Общие клиенты и TTL-кэши, без дублирования payload между контурами |
| Отказоустойчивость | Скринер переживает отказ одной биржи; арбитраж работает при любых двух |
| Безопасность | Только публичные market endpoints, HTTPS и защищённый API |

## 3. Архитектура

```text
           Binance        Bybit         OKX
              \             |            /
       exchange adapters + HTTP/retry + TTL cache
                         |
              normalize/domain models
              /            |             \
  screener scheduler  prop-screen     arbitrage service
              \            |             /
                    SQLite store
                      |
             REST API + React SPA
```

Один экземпляр Binance и Bybit клиентов инжектируется во все контуры. Каталоги
контрактов кэшируются на часы, all-market ticker/BBO payload — на секунды или
минуты. Одновременные обращения к одному cache key объединяются.

Контуры разделены:

- `internal/metrics`, `scoring`, `corr`, `scheduler` — скринер;
- `internal/propscreen` — prop-screen;
- `internal/arbitrage` — арбитраж;
- `internal/domain`, `exchange`, `normalize`, `marketdata`, `store`, `api` —
  общая инфраструктура с нейтральными контрактами.

Технологии: Go 1.22+, `net/http`, `slog`, SQLite `modernc.org/sqlite`, React +
Vite, один production-бинарник с `go:embed`.

## 4. Скринер

### 4.1. Сбор и нормализация

- Быстрый цикл по умолчанию 15 минут: Binance `exchangeInfo`, `ticker/24hr`,
  `premiumIndex`; Bybit cursor-paginated `instruments-info` и `tickers`.
- Медленный цикл 2 часа: 1h klines за 7 дней и корреляция с BTC.
- Identity инструмента учитывает base и multiplier. Неизвестные случаи
  исправляются `normalization_overrides`.
- Стейблы и blacklist исключаются.

### 4.2. Метрики и правила

- Оборот суммируется между биржами.
- При наличии Binance его price/change/range/funding/tick size приоритетны.
- `day_range = (high-low)/low*100`.
- `relative_tick = tick_size/price*100`.
- `btc_corr_7d` — Пирсон по выровненным часовым доходностям; меньше 100 точек
  даёт `null`.
- `is_inplay` учитывает медиану движения рынка.

Жёсткие фильтры: Trading, не stable/blacklist, минимальный оборот и опциональный
минимум абсолютного движения. Скоринг использует перцентили volatility,
turnover, inplay и штраф корреляции. Результат ограничивается `top_n`.

### 4.3. Prop list

Prop list редактируется вручную или импортируется из `.txt`. Разделители файла:
запятая и перенос строки; регистр игнорируется; `BTC` и `BTCUSDT` эквивалентны;
дубликаты не добавляются, существующие notes сохраняются. Блок свёрнут по
умолчанию.

## 5. Prop screen

Контур раз в `cycles.prop_screen_interval` читает существующую prop list и
параллельно получает каталоги и 24h tickers Binance/Bybit через общие client
instances. Funding и BBO для этого запроса не загружаются.

Допускаются Trading linear perpetual-контракты USDT и USDC; USDT приоритетен.
Сначала используется точное соответствие `base + multiplier`. Для записи x1
разрешён fallback на единственный доступный multiplier того же base, если он не
представлен отдельной prop entry.

Binance является приоритетным источником change/turnover. Bybit используется,
если валидной Binance-строки нет. Последний run хранится в памяти; отдельная
история не сохраняется. Корреляция часовых доходностей с BTC за 7 дней
пересчитывается в медленном цикле и переиспользует общий `corr_cache`.

## 6. Арбитраж

### 6.1. Рыночные данные

Источники v1:

- Binance: exchange info, book ticker, premium index;
- Bybit: instruments info и tickers;
- OKX: public instruments, tickers и точечный funding-rate.

В расчёт входят только Trading linear USDT perpetuals с совпадающей identity
`base + quote + multiplier + kind`.

### 6.2. Расчёт

Для каждой пары бирж проверяются оба направления:

```text
gross_bps = (sell_bid / buy_ask - 1) * 10000
net_bps   = gross_bps - fee_bps[buy] - fee_bps[sell]
capacity_usd = min(buy_ask * buy_ask_qty, sell_bid * sell_bid_qty)
```

Если размер контракта выражен не в одной базовой монете, количество приводится
через спецификацию контракта. Funding нормализуется к часу:

```text
funding_edge_per_hour =
    sell_funding_rate / sell_interval_hours
  - buy_funding_rate / buy_interval_hours
```

Funding не включён в `net_bps`, потому что фактический результат зависит от
времени удержания и попадания под funding timestamp. Он показывается отдельно.

### 6.3. Валидация результата

Возможность отбрасывается, если:

- BBO отсутствует или цена/размер не положительны;
- quote старше `max_quote_age`;
- перекос времён двух quote выше `max_quote_skew`;
- gross ниже минимума или выше sanity ceiling;
- net ниже порога;
- ёмкость первого уровня ниже `min_capacity_usd`.

Комиссии задаются в YAML отдельно для каждой биржи. Детальный funding
догружается только для верхних `funding_candidates`, с ограниченной
конкурентностью.

### 6.4. Цикл и история

Арбитражный scan запускается сразу и далее через `cycles.arbitrage_interval`
(дефолт 10 секунд). Текущий run хранится в памяти. История сохраняется не чаще
`history_interval` (дефолт 1 минута), retention — 30 дней. При одной доступной
бирже run считается невозможным; при двух из трёх сервис работает в degraded
режиме.

Ограничение v1: REST BBO и только первый уровень. Данные подходят для поиска и
статистики, но не моделируют задержку ордера, slippage и глубокую ликвидность.

## 7. Хранилище

Скринер использует `instruments`, `prop_instruments`, `snapshots`,
`snapshot_rows`, `corr_cache`, `settings`, `users`.

Арбитраж использует:

- `arbitrage_runs` — время scan и состояния бирж;
- `arbitrage_opportunities` — направления и рассчитанные метрики run.

Миграции встроены и применяются автоматически. SQLite работает в WAL-режиме.
Очистка арбитражной истории выполняется по retention.

## 8. REST API

Публичны только auth login и health. Остальные endpoint требуют сессию.

Общие:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/dashboard/summary` | Сводка всех контуров и короткие top-выборки |
| GET | `/api/health` | Состояние сервиса |
| POST | `/api/auth/login` | Вход |
| POST | `/api/auth/logout` | Выход |
| GET | `/api/auth/me` | Текущая сессия |

Скринер: `/api/screener/top`, `/history`, `/snapshots`, `/stats`,
`/prop-list`, `/settings`, `/refresh`. Старые endpoint без namespace оставлены
для совместимости.

Арбитраж:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/arbitrage/opportunities` | Текущий или последний сохранённый run |
| GET | `/api/arbitrage/stats` | Агрегаты и маршруты за период |
| POST | `/api/arbitrage/refresh` | Внеплановый scan с mutex |

Prop screen:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/prop-screen` | Текущий список prop-монет и состояния бирж |
| POST | `/api/prop-screen/refresh` | Внеплановый refresh с mutex |

Charts:

| Метод | Путь | Назначение |
|---|---|---|
| GET | `/api/charts/instruments` | Binance-first/Bybit-fallback каталог |
| WS | `/api/charts/stream` | Multiplexed snapshot и live OHLC по `slot_id` |

## 9. Web UI

- `/` — публичный лендинг Scalp Assist;
- `/auth` — вход;
- `/app` — общая приватная сводка;
- `/app/screener` — top;
- `/app/screener/history` — история;
- `/app/screener/settings` — настройки и prop list;
- `/app/screener/wiki` — русскоязычная справка по работе со скринером,
  сводным показателям, колонкам, score, History и Settings;
- `/app/prop-screen` — отдельный список prop-монет с поиском, сортировкой,
  источником, 24h-метриками и корреляцией с BTC за 7 дней;
- `/app/prop-screen/wiki` — русскоязычная справка по workflow, фильтрам,
  столбцам и практической интерпретации корреляции;
- `/app/charts` — два столбца свечных графиков `1h/5m`, BTC/ETH по умолчанию,
  заглушка добавления и ручное обновление раскладки из `localStorage`;
- `/app/arbitrage` — вкладка List: возможности, статусы бирж, фильтр и статистика;
- `/app/arbitrage/settings` — пустая вкладка Settings, зарезервирована под UI
  параметров арбитража;
- `/app/arbitrage/wiki` — русскоязычная Wiki с порядком работы, расшифровкой
  сводных показателей и столбцов, формулами и предупреждением об ограничениях
  REST BBO.

Все fetch выполняются с credentials. Ответ 401 переводит на `/auth`. Светлая и
тёмная темы и мобильный layout обязательны.

Overview показывает для prop-screen число найденных монет, размер prop list,
состояния Binance/Bybit и распределение источников метрик.

## 10. Конфигурация

YAML содержит интервалы контуров, включая `prop_screen_interval`, настройки
скринера и секцию `arbitrage`.
Runtime-overrides из таблицы `settings` пока относятся к скринеру. Настройки
арбитража применяются из YAML после рестарта.

Диапазоны арбитража валидируются целиком: spread, capacity, quote age/skew,
top-N, history interval, retention и неотрицательные комиссии.

## 11. Приёмка

1. `go test ./...`, `go build ./cmd/screener`, `npm test`, `npm run build`
   проходят.
2. После login открывается `/app`; все четыре контура доступны из общей
   навигации.
3. `/app/prop-screen` показывает только доступные prop-монеты, выбирает Binance
   перед Bybit и корректно переживает отказ одного источника.
4. Live scan получает непустые quote Binance/Bybit/OKX и показывает только
   правдоподобные spread после sanity/capacity filters.
5. Отключение одной биржи не останавливает арбитраж; отключение двух отражается
   понятной ошибкой.
6. История скринера и арбитража переживает рестарт и миграции.
7. Без cookie защищённые endpoint отвечают 401; logout возвращает на `/`.
8. В production 48 часов нет пропусков циклов, неконтролируемого роста памяти и
   ошибок SQLite.
9. Charts сохраняет порядок и таймфреймы после reload, не допускает больше
   десяти слотов и синхронизируется с `Set charts` на обеих таблицах.
