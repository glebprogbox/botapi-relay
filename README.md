# botapi-relay

Зеркало Telegram Bot API: HTTP-сервис на Go (~80 строк, без зависимостей), который пересылает запросы на `api.telegram.org`. Разворачивается там, где Bot API доступен, и используется клиентами из сетей, где он недоступен.

## Как работает

```
клиент ──▶ https://<relay>/s/<RELAY_SECRET>/bot<token>/<method> ──▶ https://api.telegram.org/bot<token>/<method>
```

- `httputil.ReverseProxy` на стандартной библиотеке; один бинарник, ~15 МБ памяти.
- Секрет — сегмент пути `/s/<RELAY_SECRET>/…`: запрос с другим префиксом получает 403. Клиенту не нужны кастомные заголовки — достаточно заменить базовый адрес Bot API (`apiRoot` в grammY, `base_url` в aiogram и т. п.) на `https://<relay>/s/<RELAY_SECRET>`.
- Работают все методы, включая long polling `getUpdates` (таймауты сервера рассчитаны на удержание соединения) и загрузку файлов.
- Тела запросов и URL не логируются: в пути лежит токен бота. Ошибки апстрима пишутся без URL.
- `GET /health` → `ok` — без секрета, для проверок живости.

## Переменные окружения

| Переменная | Обязательна | Описание |
|---|---|---|
| `RELAY_SECRET` | да | длинная случайная строка (например, `openssl rand -hex 24`); сервис не стартует без неё |
| `PORT` | нет | порт HTTP-сервера, по умолчанию `8080` |

## Деплой на Timeweb App Platform (без Docker)

Приложение соответствует нативному Go-флоу платформы: `go.mod` и главный пакет в корне, сборка `go build -o main .`, запуск `./main`, слушает `0.0.0.0`.

1. Backend app → тип **Go** (любой пункт из Go-списка — Gin/Beego/…, команды сборки одинаковые), версия Go 1.22+.
2. Команды сборки/запуска — по умолчанию.
3. Переменная окружения `RELAY_SECRET`; порт — `8080` (или задайте `PORT`).
4. Проверка: `curl https://<app>.twc1.net/health` → `ok`.

## Запуск

```sh
docker build -t botapi-relay .
docker run -d -p 8080:8080 -e RELAY_SECRET=$(openssl rand -hex 24) botapi-relay
```

Проверка:

```sh
curl http://localhost:8080/health                       # ok
curl http://localhost:8080/s/wrong/bot123/getMe         # 403
curl http://localhost:8080/s/<RELAY_SECRET>/bot<token>/getMe   # ответ Telegram
```

Без Docker: `go build -o relay . && RELAY_SECRET=... ./relay`.
