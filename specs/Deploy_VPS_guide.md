# Инструкция: ручной деплой MVP на VPS

**Версия:** 2.1 · **Дата:** 27.07.2026
**Для кого:** владелец сервиса, деплой выполняется вручную.
**Что деплоим:** один статический Go-бинарник `screener` (фронтенд зашит внутрь через go:embed) + `config.yaml`. Внутри работают скринер и информационный контур арбитража Binance/Bybit/OKX. База SQLite создаётся сама при первом запуске. Доступ — из открытого интернета по HTTPS с логином (email+пароль); VPN не нужен.

**Изменения v2.1:** добавлены проверка доступности OKX, параметры арбитража и smoke-check общей приватной страницы и обоих контуров.

Инструкция написана под **Ubuntu 22.04/24.04** (на Debian всё то же самое). Предполагается, что у тебя есть SSH-доступ с правами root (или sudo).

---

## 0. Что понадобится

- VPS: 1 vCPU, 512 МБ–1 ГБ RAM, 10 ГБ диска — достаточно (сервис ест < 100 МБ RAM).
- **Регион VPS — важно.** Биржи гео-блокируют API по IP: из **США** Binance Futures отдаёт 451 (не подходит категорически), **Великобритания** рискованна из-за ограничений Bybit, на **РФ** лучше не завязываться. Надёжный выбор: Нидерланды, Германия, Финляндия или Сингапур. Задержка не важна (циклы раз в 15 мин) — регион выбирается только по гео-блокам. Проверка кандидата с самого VPS:

  ```bash
  curl -s https://fapi.binance.com/fapi/v1/time     # ожидаем JSON с serverTime
  curl -s https://api.bybit.com/v5/market/time      # ожидаем retCode: 0
  curl -s https://www.okx.com/api/v5/public/time    # ожидаем code: "0"
  ```

  Если вместо JSON приходит 451/403 или заглушка CloudFront — регион не годится.
- **Домен** (обязательно — Let's Encrypt не выдаёт сертификаты на голый IP). Подойдёт любой дешёвый домен (~$2–10/год) или поддомен уже имеющегося, например `screener.mydomain.com`.
- SSH-доступ к VPS.
- На локальной машине: собранный релиз (`make release` — см. шаг 1).

Плейсхолдеры в командах — подставь свои значения:

| Плейсхолдер | Что это | Пример |
|---|---|---|
| `VPS_IP` | публичный IP VPS | `203.0.113.10` |
| `DOMAIN` | домен сервиса | `screener.mydomain.com` |

---

## 1. Сборка релиза (на локальной машине)

В корне репозитория:

```powershell
# Windows (PowerShell):
cd web; npm ci; npm run build; cd ..
$env:CGO_ENABLED = "0"; $env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o screener ./cmd/screener
```

```bash
# или Linux/macOS/Git Bash:
make release
```

Проверка: файл `screener` появился, размер порядка 15–30 МБ. Это единственный артефакт — фронтенд уже внутри.

Подготовь боевой конфиг: скопируй `config.example.yaml` в `config.yaml` и заполни:

```yaml
domain: "DOMAIN"                 # например screener.mydomain.com
listen_https: ":443"
listen_http: ":80"               # редирект на HTTPS + проверка Let's Encrypt; не отключать
db_path: "./data/screener.db"

auth:
  bootstrap_email: "твой@email"
  bootstrap_password: "длинный-случайный-пароль"   # менеджер паролей в помощь

# пороги/веса можно не трогать — они редактируются из UI после запуска

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

Про `bootstrap_*`: эти поля применяются **один раз** — при первом старте с пустой базой создаётся твоя учётка (в базу попадает только bcrypt-хэш). После первого успешного входа строку `bootstrap_password` можно удалить из конфига — она нужна снова только для сброса пароля (раздел 9). JWT-секрет сервис генерирует сам при первом старте и хранит в data-каталоге.

---

## 2. Подготовка VPS

Подключись и обнови систему:

```bash
ssh root@VPS_IP
apt update && apt upgrade -y
apt install -y sqlite3 ufw          # sqlite3 нужен только для бэкапов
```

Создай отдельного системного пользователя и директории (сервис не должен работать от root):

```bash
useradd --system --home /opt/screener --shell /usr/sbin/nologin screener
mkdir -p /opt/screener/data
chown -R screener:screener /opt/screener
```

Раскладка:

```
/opt/screener/
├── screener           # бинарник
├── screener.prev      # предыдущая версия (для отката, появится после первого обновления)
├── config.yaml
├── backups/           # бэкапы базы (создаст скрипт)
└── data/
    ├── screener.db    # база (создастся сама)
    ├── acme/          # кэш TLS-сертификатов (создастся сам)
    └── jwt.secret     # секрет подписи JWT (создастся сам)
```

Файрвол — SSH и веб, всё остальное закрыто:

```bash
ufw allow OpenSSH
ufw allow 80/tcp       # ACME-challenge + редирект на HTTPS
ufw allow 443/tcp      # HTTPS
ufw enable             # ответить y
ufw status             # убедиться: 22, 80, 443 — и ничего больше
```

---

## 3. Домен и DNS

У регистратора домена (или в панели DNS) создай **A-запись**:

```
screener.mydomain.com  →  VPS_IP        (TTL можно 300–600)
```

Дождись, пока запись разойдётся, и проверь с любой машины:

```bash
nslookup DOMAIN        # должен вернуть VPS_IP
```

**Не запускай сервис, пока DNS не резолвится в правильный IP** — иначе выпуск сертификата будет падать, а у Let's Encrypt есть лимиты на неудачные попытки (см. раздел 10).

---

## 4. Копирование файлов

С локальной машины:

```bash
scp screener config.yaml root@VPS_IP:/tmp/
```

На VPS:

```bash
mv /tmp/screener /opt/screener/screener
mv /tmp/config.yaml /opt/screener/config.yaml
chmod +x /opt/screener/screener
chmod 600 /opt/screener/config.yaml        # в конфиге bootstrap-пароль
chown -R screener:screener /opt/screener
```

---

## 5. Systemd-юнит

Создай файл `/etc/systemd/system/screener.service`:

```ini
[Unit]
Description=Futures screener (scalp-assist)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=screener
Group=screener
WorkingDirectory=/opt/screener
ExecStart=/opt/screener/screener -config /opt/screener/config.yaml
Restart=always
RestartSec=5

# Порты 80/443 ниже 1024 — обычному пользователю нужна capability:
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Hardening — сервису можно писать только в свою data-директорию
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/screener/data

[Install]
WantedBy=multi-user.target
```

Включи и запусти:

```bash
systemctl daemon-reload
systemctl enable --now screener
systemctl status screener        # ожидаем: active (running)
journalctl -u screener -f        # живые логи; Ctrl+C — выйти
```

В логах первого старта должны появиться: загрузка конфига → миграции → создание учётки из bootstrap → старт HTTP/HTTPS → первый быстрый цикл. Сертификат выпускается при первом HTTPS-обращении к домену и занимает несколько секунд.

---

## 6. Проверка

С любой машины (не с VPS — проверяем настоящий путь через интернет):

```bash
# 1. HTTPS жив, сертификат валиден, health отвечает (он без авторизации):
curl -s https://DOMAIN/api/health | jq .

# 2. HTTP редиректит на HTTPS:
curl -sI http://DOMAIN/ | grep -i location     # ожидаем https://DOMAIN/

# 3. Данные закрыты авторизацией:
curl -s -o /dev/null -w "%{http_code}\n" https://DOMAIN/api/dashboard/summary  # ожидаем 401

# 4. Логин работает (cookie сохраняется в файл):
curl -s -c /tmp/sc.txt -H "Content-Type: application/json" \
  -d '{"email":"твой@email","password":"твой-пароль"}' https://DOMAIN/api/auth/login
curl -s -b /tmp/sc.txt "https://DOMAIN/api/screener/top?limit=20" | jq '.[0]'
curl -s -b /tmp/sc.txt "https://DOMAIN/api/arbitrage/opportunities" | jq .
```

В браузере: открой `https://DOMAIN` на ноутбуке и телефоне. Без входа должен быть лендинг; после login открывается `/app` со сводкой. Проверь переходы в «Скринер» и «Арбитраж», тему, refresh обоих контуров и сохранение настроек скринера. Закрой и открой браузер — сессия должна сохраниться (токен живёт 30 дней).

Ребут-тест:

```bash
reboot
# через пару минут:
systemctl status screener                      # active (running) без ручных действий
curl -s https://DOMAIN/api/health | jq .       # сертификат на месте (кэш в data/acme)
```

Залогиненная сессия после ребута тоже должна остаться живой (JWT-секрет хранится в `data/jwt.secret`).

---

## 7. Бэкап базы

Раз в сутки, храним 14 копий. В базе всё: история, проп-лист, настройки, учётка. Создай `/opt/screener/backup.sh`:

```bash
#!/bin/sh
set -eu
BACKUP_DIR=/opt/screener/backups
mkdir -p "$BACKUP_DIR"
sqlite3 /opt/screener/data/screener.db ".backup '$BACKUP_DIR/screener-$(date +%F).db'"
find "$BACKUP_DIR" -name 'screener-*.db' -mtime +14 -delete
```

```bash
chmod +x /opt/screener/backup.sh
chown screener:screener /opt/screener/backup.sh
crontab -u screener -e
# добавить строку:
# 30 4 * * * /opt/screener/backup.sh
```

`sqlite3 .backup` корректно работает при живом сервисе (WAL), останавливать ничего не нужно. Восстановление: остановить сервис, подменить `data/screener.db` файлом бэкапа, запустить.

Периодически (раз в месяц) стоит утаскивать свежий бэкап с VPS к себе: `scp root@VPS_IP:/opt/screener/backups/screener-*.db .`

---

## 8. Обновление версии

```bash
# локально: собрать (шаг 1), затем
scp screener root@VPS_IP:/tmp/screener.new

# на VPS:
systemctl stop screener
mv /opt/screener/screener /opt/screener/screener.prev   # прошлая версия — для отката
mv /tmp/screener.new /opt/screener/screener
chmod +x /opt/screener/screener && chown screener:screener /opt/screener/screener
systemctl start screener
journalctl -u screener -n 30 --no-pager                  # миграции применились, циклы пошли
```

**Откат**, если что-то пошло не так:

```bash
systemctl stop screener
mv /opt/screener/screener.prev /opt/screener/screener
systemctl start screener
```

База обратно совместима в пределах MVP (миграции только добавляют). Если новая версия успела применить миграцию, а откатываешься на старую — сначала восстанови базу из бэкапа за прошлые сутки.

Конфиг меняется без пересборки: правишь `/opt/screener/config.yaml` → `systemctl restart screener`. Пороги и веса при этом проще менять из UI — рестарт не нужен.

---

## 9. Сброс пароля

Если пароль забыт:

```bash
# 1. Впиши новый пароль в /opt/screener/config.yaml → auth.bootstrap_password
# 2. Затем:
systemctl stop screener
sudo -u screener /opt/screener/screener -config /opt/screener/config.yaml -reset-password
systemctl start screener
# 3. Проверь вход в браузере и удали bootstrap_password из конфига
```

Смена email — так же, через `bootstrap_email` + `-reset-password`.

---

## 10. Диагностика

| Симптом | Что смотреть |
|---|---|
| Сервис не стартует | `journalctl -u screener -n 50 --no-pager` — типично: опечатка в config.yaml (сервис пишет, какое поле), нет прав на `data/` (`chown -R screener:screener /opt/screener`) |
| `permission denied` на порту 80/443 | В юните отсутствует `AmbientCapabilities=CAP_NET_BIND_SERVICE` (раздел 5) |
| Сертификат не выпускается | 1) `nslookup DOMAIN` → точно VPS_IP? 2) порты 80 и 443 открыты в ufw? 3) логи: `journalctl -u screener \| grep -i acme`. **Важно:** у Let's Encrypt лимит ~5 неудачных попыток в час на домен — не рестарти сервис в цикле, сначала почини DNS/порты |
| Браузер показывает «небезопасно» | Сертификат ещё не выпущен (первые секунды после старта) или ходишь по IP вместо домена — autocert работает только для DOMAIN из конфига |
| 401 при валидном логине | Куки: браузер должен ходить именно на `https://DOMAIN` (не IP); в curl не забыт `-b /tmp/sc.txt` |
| Все разлогинились после манипуляций с data/ | Удалён/пересоздан `data/jwt.secret` — старые токены невалидны. Не страшно: просто войти заново |
| «Слишком много попыток» на логине | Rate-limit (5/мин с IP) — подожди минуту. Если это не ты — см. логи, кто ломится |
| Страница не открывается | `curl https://DOMAIN/api/health` с другой машины; `systemctl status screener`; `ufw status` |
| В health биржа в статусе degraded | Сеть/лимиты биржи. Не критично: снапшоты идут по живой бирже. Смотри логи на `429`/`retCode 10006` |
| Снапшоты перестали обновляться | `journalctl -u screener --since "1 hour ago" \| grep -i error`; возраст снапшота есть в `/api/health` |
| Память растёт | `systemctl status screener` показывает RSS; норма < 100 МБ. Если растёт неделями — рестарт и issue в бэклог |

---

## 11. Чек-лист приёмки (из PRD, раздел 9)

- [ ] 48 часов непрерывной работы: снапшоты каждые 15 мин без пропусков (видно на странице «История»), corr-кэш обновляется, RAM стабильна.
- [ ] `https://DOMAIN` открывается с ноутбука и телефона из обычной сети: сертификат валиден, HTTP редиректит на HTTPS.
- [ ] Без логина доступен лендинг; защищённый API без cookie → 401; после логина открывается `/app`, тема работает.
- [ ] Скринер обновляется и показывает top; арбитраж получает Binance/Bybit/OKX, отображает комиссии, capacity и состояние деградации.
- [ ] Сессия переживает рестарт сервиса и ребут VPS; 6-я попытка логина за минуту блокируется.
- [ ] Санити методички: в топе нет монет ниже порога объёма; SOL/DOGE/XRP-класс помечен «ходит за BTC».
- [ ] Проп-лист редактируется из UI, флаги меняются со следующего снапшота.
- [ ] Правила отбора редактируются из UI и переживают рестарт; «Сбросить к дефолтам» работает.
- [ ] `reboot` VPS — сервис и сертификат поднялись сами, история цела.
- [ ] `curl -b cookie "https://DOMAIN/api/screener/top?format=text"` возвращает готовый текст (задел под Telegram-агента).
- [ ] Сброс пароля через `-reset-password` проверен; `bootstrap_password` удалён из конфига.
- [ ] Бэкап-скрипт отработал хотя бы раз (`ls /opt/screener/backups`).
