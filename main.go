// botapi-relay — зеркало Telegram Bot API.
//
// HTTP-сервис, который пересылает запросы на api.telegram.org.
// Разворачивается в локации, откуда Bot API доступен, и используется
// клиентами из сетей, где он недоступен: клиенту достаточно заменить
// базовый адрес Bot API (apiRoot) на адрес зеркала.
//
// Схема URL: клиент ходит на /s/<RELAY_SECRET>/bot<token>/<method>,
// зеркало проверяет секрет, срезает префикс и пересылает
// /bot<token>/<method> дальше. Секрет в пути — чтобы зеркалом не мог
// пользоваться кто попало и чтобы клиентам не требовались кастомные
// заголовки.
//
// Тела запросов и URL не логируются (в пути лежит токен бота).
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	secret := os.Getenv("RELAY_SECRET")
	if secret == "" {
		log.Fatal("RELAY_SECRET не задан")
	}
	prefix := "/s/" + secret

	target, err := url.Parse("https://api.telegram.org")
	if err != nil {
		log.Fatal(err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = target.Host
		// Не тащим наружу ничего лишнего от платформы/балансировщика.
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
		if !strings.HasPrefix(r.URL.Path, prefix+"/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
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
	log.Printf("relay: слушаю :%s", port)
	log.Fatal(srv.ListenAndServe())
}
