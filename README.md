# botapi-relay

Зеркало Telegram Bot API: HTTP-сервис на Go (~200 строк, без зависимостей), который пересылает запросы на `api.telegram.org`. Разворачивается там, где Bot API доступен, и используется клиентами из сетей, где он недоступен. Поддерживает несколько ботов с разными секретами — без БД, одной переменной окружения.

## Как работает

```
клиент ──[X-Relay-Secret: <secret>]──▶ https://<relay>/bot<token>/<method> ──▶ https://api.telegram.org/bot<token>/<method>
```

- `httputil.ReverseProxy` на стандартной библиотеке; один бинарник, ~15 МБ памяти.
- **Авторизация — заголовок `X-Relay-Secret`**, не URL: пути попадают в логи балансировщиков, заголовки — нет. Перед пересылкой в Telegram заголовок удаляется.
- **IP-фильтр** (`IP_ALLOW`): второй рубеж после секрета — вызовы принимаются только с перечисленных адресов (например, IP вашего сервера). `/health` отвечает с любого адреса.
- **Мультибот**: `RELAY_BOTS=botId=secret,botId2=secret2`. ID бота — число до `:` в токене; зеркало достаёт его из пути запроса и сверяет секрет (constant-time). Запрос к боту не из списка или с чужим секретом → 403. Утечка одного секрета не открывает остальных ботов.
- Работают все методы, включая long polling `getUpdates` (таймауты сервера рассчитаны на удержание соединения) и скачивание файлов (`/file/bot<token>/...`).
- Тела запросов и URL не логируются: в пути лежит токен бота. Ошибки апстрима пишутся без URL.
- `GET /health` → `ok` — без секрета, для проверок живости.

## Переменные окружения

| Переменная | Обязательна | Описание |
|---|---|---|
| `RELAY_BOTS` | да | пары `botId=secret` через запятую, например `123456=a1b2c3,789012=d4e5f6`; секреты — длинные случайные строки (`openssl rand -hex 24`) |
| `IP_ALLOW` | нет | IP и/или CIDR через запятую (`1.2.3.4, 10.0.0.0/8`) — только с этих адресов принимаются вызовы, остальным 403. Пусто — проверка выключена. Клиентский IP берётся из последнего элемента `X-Forwarded-For` (его ставит балансировщик платформы, подделать нельзя) |
| `PORT` | нет | порт HTTP-сервера, по умолчанию `8080` |

## Настройка клиента

Базовый адрес Bot API → адрес зеркала, плюс заголовок. Пример для grammY (кастомный `fetch`):

```ts
const relayFetch: typeof fetch = (input, init) => {
  const headers = new Headers(init?.headers);
  headers.set("x-relay-secret", process.env.RELAY_SECRET!);
  return fetch(input, { ...init, headers });
};
const bot = new Bot(token, {
  client: { apiRoot: "https://<relay>", fetch: relayFetch },
});
```

Для aiogram — свой `AiohttpSession` с `headers={"X-Relay-Secret": ...}` и `api=TelegramAPIServer.from_base("https://<relay>")`.

## Деплой на Timeweb App Platform (без Docker)

Приложение соответствует нативному Go-флоу платформы: `go.mod` и главный пакет в корне, сборка `go build -o main .`, запуск `./main`, слушает `0.0.0.0`.

1. Backend app → тип **Go** (любой пункт из Go-списка — Gin/Beego/…, команды сборки одинаковые), версия Go 1.22+.
2. Команды сборки/запуска — по умолчанию.
3. Переменная окружения `RELAY_BOTS`; порт — `8080` (или задайте `PORT`).
4. Проверка: `curl https://<app>.twc1.net/health` → `ok`.

## Запуск

```sh
docker build -t botapi-relay .
docker run -d -p 8080:8080 -e RELAY_BOTS="<botId>=$(openssl rand -hex 24)" botapi-relay
```

Проверка:

```sh
curl http://localhost:8080/health                                      # ok
curl http://localhost:8080/bot<token>/getMe                            # 403 (без секрета)
curl -H "X-Relay-Secret: <secret>" http://localhost:8080/bot<token>/getMe   # ответ Telegram
```

Без Docker: `go build -o relay . && RELAY_BOTS=... ./relay`.
