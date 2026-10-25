// botapi-relay — зеркало Telegram Bot API.
//
// HTTP-сервис, который пересылает запросы на api.telegram.org.
// Разворачивается в локации, откуда Bot API доступен, и используется
// клиентами из сетей, где он недоступен: клиенту достаточно заменить
// базовый адрес Bot API (apiRoot) на адрес зеркала и добавить заголовок
// X-Relay-Secret.
//
// Авторизация: секрет в заголовке (не в URL — пути попадают в логи
// балансировщиков). Мультибот без БД: RELAY_BOTS=botId=secret,botId2=secret2 —
// секрет привязан к конкретному боту (botId — число до «:» в токене,
// зеркало достаёт его из пути /bot<token>/... и сверяет).
//
// Тела запросов и URL не логируются (в пути лежит токен бота).
package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// parseBots разбирает RELAY_BOTS: "123456=secretA, 789=secretB".
func parseBots(raw string) map[string]string {
	bots := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		id, secret, ok := strings.Cut(pair, "=")
		id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
		if !ok || id == "" || secret == "" {
			log.Fatalf("RELAY_BOTS: ожидаю пары botId=secret, получил %q", pair)
		}
		bots[id] = secret
	}
	return bots
}

// botIDFromPath достаёт ID бота из пути Bot API:
// /bot<id>:<hash>/<method> и /file/bot<id>:<hash>/<path> (скачивание файлов).
func botIDFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/bot")
	if !ok {
		rest, ok = strings.CutPrefix(path, "/file/bot")
		if !ok {
			return ""
		}
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}
	return id
}

func main() {
	bots := parseBots(os.Getenv("RELAY_BOTS"))
	if len(bots) == 0 {
		log.Fatal("RELAY_BOTS не задан (формат: botId=secret,botId2=secret2)")
	}

	target, err := url.Parse("https://api.telegram.org")
	if err != nil {
		log.Fatal(err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = target.Host
		// Не тащим наружу ни секрет, ни служебное от платформы/балансировщика.
		r.Header.Del("X-Relay-Secret")
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Real-Ip")
	}
	// Логи ошибок — только факт, без URL.
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("upstream error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		expected := bots[botIDFromPath(r.URL.Path)]
		got := r.Header.Get("X-Relay-Secret")
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// Long polling (getUpdates) держит соединение до ~60 секунд —
		// таймауты записи должны быть заметно больше.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       180 * time.Second,
	}
	log.Printf("relay: слушаю :%s, ботов: %d", port, len(bots))
	log.Fatal(srv.ListenAndServe())
}
