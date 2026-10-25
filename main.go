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
// Опционально IP_ALLOW — список IP/CIDR, откуда принимаются вызовы;
// всем остальным — 403.
//
// Тела запросов и URL не логируются (в пути лежит токен бота).
package main

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
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

// parseAllow разбирает IP_ALLOW: "1.2.3.4, 10.0.0.0/8". Пусто — проверка выключена.
func parseAllow(raw string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p)
			continue
		}
		if a, err := netip.ParseAddr(item); err == nil {
			a = a.Unmap()
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		log.Fatalf("IP_ALLOW: не разберу %q (ожидаю IP или CIDR через запятую)", item)
	}
	return out
}

// clientIP: последний элемент X-Forwarded-For (его дописывает балансировщик
// платформы, в отличие от первого его нельзя подделать), иначе RemoteAddr.
func clientIP(r *http.Request) netip.Addr {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
			return a.Unmap()
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return a.Unmap()
		}
	}
	return netip.Addr{}
}

func ipAllowed(allow []netip.Prefix, ip netip.Addr) bool {
	if len(allow) == 0 {
		return true
	}
	if !ip.IsValid() {
		return false
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return true
		}
	}
	return false
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

func health(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func main() {
	bots := parseBots(os.Getenv("RELAY_BOTS"))
	if len(bots) == 0 {
		log.Fatal("RELAY_BOTS не задан (формат: botId=secret,botId2=secret2)")
	}
	allow := parseAllow(os.Getenv("IP_ALLOW"))

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
		health(w)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !ipAllowed(allow, ip) {
			log.Printf("denied: ip=%s не в IP_ALLOW", ip)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		expected := bots[botIDFromPath(r.URL.Path)]
		got := r.Header.Get("X-Relay-Secret")
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			log.Printf("denied: ip=%s неверный секрет или неизвестный бот", ip)
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
	log.Printf("relay: слушаю :%s, ботов: %d, IP-фильтр: %d правил", port, len(bots), len(allow))
	log.Fatal(srv.ListenAndServe())
}
