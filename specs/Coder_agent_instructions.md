# Инструкция для кодового агента

## Контекст проекта

Проект `scalp-assist` — персональный веб-сервис-скринер фьючерсов Binance и Bybit для отбора топ-20 инструментов под скальпинг. Основные требования лежат в:

- `specs/PRD_trading_assistant.md`
- `specs/Dev_plan_MVP.md`
- `specs/Deploy_VPS_guide.md`
- `specs/Summary_описание_проекта.md`

При конфликте документов приоритет такой: PRD и Dev plan выше HTML-мокапа. HTML-мокап используется как эталон визуального стиля, но местами старее актуального MVP.

## Важное правило для живых запросов к биржам

В локальной среде пользователя настроен VPN, и запросы к Binance/Bybit проходят через системный `cmd.exe`.

Для live-smoke проверок API использовать именно:

```powershell
C:\Windows\System32\cmd.exe /c curl -s -i https://fapi.binance.com/fapi/v1/time
C:\Windows\System32\cmd.exe /c curl -s -i https://api.bybit.com/v5/market/time
```

Проверено 08.07.2026: оба запроса через `cmd.exe` возвращают `HTTP/1.1 200 OK`.

Ожидаемые признаки успешного ответа:

- Binance: JSON с `serverTime`
- Bybit: JSON с `retCode: 0`

Если запускать сетевые проверки обычным PowerShell/Go-процессом, запросы могут не использовать VPN-маршрут и получать геоблок:

- Binance: `451 Service unavailable from a restricted location`
- Bybit: `403 CloudFront distribution is configured to block access from your country`

Поэтому для ручной диагностики и live API smoke-команд в этой машине предпочитать `cmd.exe /c curl ...`.

## Git и этапы

Работать небольшими этапами и коммитить после зелёных тестов. Уже принятый стиль коммитов:

- `Implement exchange clients`
- `Implement symbol normalization`
- `Implement metrics filters`
- `Implement scoring top`

Перед финальным ответом проверять:

```powershell
$env:GO111MODULE='on'; go test ./...
$env:GO111MODULE='on'; go build -o $env:TEMP\screener-check.exe ./cmd/screener
git status --short
```

## Текущие технические решения

- Go module: `scalp-assist`
- Минимальная версия в `go.mod`: `go 1.22`
- Конфиг: YAML через `gopkg.in/yaml.v3`
- Логи: `log/slog`, JSON
- Обычные тесты не должны требовать сеть
- Live API tests держать под build tag `integration`
- Для Binance/Bybit используются только публичные market endpoints, без API-ключей

## Frontend routing decision

Перед реализацией фронтенда зафиксировано новое требование:

- `/` — публичный лендинг без авторизации, главная страница сервиса.
- `/auth` — отдельная страница авторизации/регистрации. В MVP backend поддерживает login существующей bootstrap-учётки; registration UI допускается только как задел, без вызова несуществующего API.
- `/app/top`, `/app/history`, `/app/settings` — приватные страницы, доступны только авторизованным пользователям.
- После успешного login редиректить на `/app/top`.
- При 401 в приватной зоне редиректить на `/auth`.
- После logout принудительно редиректить на публичный лендинг `/`.
