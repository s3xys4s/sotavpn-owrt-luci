package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sota-gateway/internal/profile"
)

type app struct {
	mu                           sync.Mutex
	cfg                          settings
	file, envKey, origin, socket string
	servers                      []location
	router                       *routerControl
	events                       []event
}
type rpcRequest struct {
	Version string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Offset   int              `json:"offset"`
		Transfer string           `json:"transfer"`
		Revision string           `json:"revision"`
		Chunk    string           `json:"chunk"`
		Final    bool             `json:"final"`
		Key      string           `json:"key"`
		ID       *int64           `json:"id"`
		Protocol string           `json:"protocol"`
		Profile  json.RawMessage  `json:"profile"`
		Network  *networkSettings `json:"network"`
	} `json:"params"`
}

func (a *app) closeProvider(ctx context.Context) error {
	if !a.cfg.ProviderSession {
		return nil
	}
	if _, err := a.provider(ctx, "/connection/disconnect", a.key()); err != nil {
		return err
	}
	next := a.cfg
	next.ProviderSession = false
	return a.save(next)
}

func (a *app) failedConnect(ctx context.Context, cause error) error {
	if err := a.closeProvider(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("%w. Не удалось закрыть сессию Sota: %v", cause, err)
	}
	return cause
}

func coreIdle(s json.RawMessage) bool {
	var st struct {
		State *int `json:"core_state"`
	}
	return json.Unmarshal(s, &st) == nil && st.State != nil && *st.State == 0
}

func (a *app) control(op string, p json.RawMessage) (json.RawMessage, error) {
	if a.router != nil {
		return a.router.control(op, p)
	}
	c, e := net.DialTimeout("unix", a.socket, time.Second)
	if e != nil {
		return nil, errors.New("Служба сетевого стенда недоступна")
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(32 * time.Second))
	if json.NewEncoder(c).Encode(map[string]any{"op": op, "profile": p}) != nil {
		return nil, errors.New("Не удалось отправить команду")
	}
	var v struct {
		Result json.RawMessage
		Error  string
	}
	if json.NewDecoder(io.LimitReader(c, 8192)).Decode(&v) != nil {
		return nil, errors.New("Служба сетевого стенда вернула неполный ответ")
	}
	switch v.Error {
	case "tun_busy":
		return nil, errors.New("Ядро ещё освобождает предыдущий TUN. Повторите подключение")
	case "core_start":
		return nil, errors.New("Ядро не завершило запуск туннеля")
	case "client_route":
		return nil, errors.New("Не удалось направить тестовый клиент через TUN")
	}
	if v.Error != "" || len(v.Result) == 0 {
		return nil, errors.New("Сетевой стенд не выполнил команду")
	}
	return v.Result, nil
}
func (a *app) call(ctx context.Context, q rpcRequest) (result any, callErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.callLocked(ctx, q)
}

func (a *app) callLocked(ctx context.Context, q rpcRequest) (result any, callErr error) {
	defer func() { a.logAction(q.Method, callErr) }()
	switch q.Method {
	case "network.readpart", "network.writepart":
		if a.router == nil {
			return nil, errors.New("Настройки сети доступны в OpenWrt")
		}
		if q.Method == "network.readpart" {
			return a.readRoutePart(q)
		}
		return a.writeRoutePart(ctx, q)
	case "network.get", "network.set":
		if a.router == nil {
			return nil, errors.New("Настройки сети доступны в OpenWrt")
		}
		if q.Method == "network.get" {
			return getNetwork()
		}
		if q.Params.Network == nil {
			return nil, errors.New("Отсутствуют настройки сети")
		}
		return a.setNetwork(ctx, *q.Params.Network)
	case "logs":
		return a.events, nil
	case "status":
		s, e := a.control("status", nil)
		if e != nil {
			return nil, e
		}
		_, err := os.Stat(a.file + ".profile")
		mode := a.cfg.Protocol
		if mode == "" {
			mode = profile.Auto
		}
		return map[string]any{"core": s, "hwid": a.cfg.Device, "has_key": a.key() != "", "key_source": map[bool]string{true: "env", false: "file"}[a.envKey != ""], "selected": a.cfg.Selected, "protocol": mode, "imported": err == nil, "provider_session": a.cfg.ProviderSession, "want_tunnel": a.wantsTunnel(), "servers": a.servers}, nil
	case "account.setKey", "server.list":
		key := a.key()
		if q.Method == "account.setKey" {
			if a.envKey != "" {
				return nil, errors.New("Ключ задан окружением службы")
			}
			if a.cfg.ProviderSession {
				return nil, errors.New("Сначала отключите текущую сессию")
			}
			key = strings.TrimSpace(q.Params.Key)
			if !validSecret(key) {
				return nil, errors.New("Проверьте формат ключа")
			}
			if _, e := a.provider(ctx, "/subscription/profile", key); e != nil {
				return nil, e
			}
		}
		list, e := a.locations(ctx, key)
		if e != nil {
			return nil, e
		}
		if q.Method == "account.setKey" {
			next := a.cfg
			next.Key = key
			next.Selected = nil
			if e = a.save(next); e != nil {
				return nil, e
			}
		}
		a.servers = list
		return list, nil
	case "profile.import":
		if a.cfg.ProviderSession {
			return nil, errors.New("Сначала отключите текущую сессию")
		}
		p, e := tunnelProfile(q.Params.Profile, profile.Auto)
		if e != nil {
			return nil, e
		}
		if atomicFile(a.file+".profile", p) != nil {
			return nil, errors.New("Не удалось сохранить профиль")
		}
		return true, nil
	case "tunnel.restart":
		if a.router != nil {
			if serviceStopping() {
				return nil, errors.New("Служба останавливается")
			}
			if err := a.stopLocal(); err != nil {
				return nil, err
			}
		} else if _, err := a.control("disconnect", nil); err != nil {
			return nil, err
		}
		q.Method = "tunnel.connect"
		return a.callLocked(ctx, q)
	case "tunnel.connect":
		if a.router != nil && serviceStopping() {
			return nil, errors.New("Служба останавливается")
		}
		if a.router != nil && q.Params.ID != nil && *q.Params.ID == 0 {
			q.Params.ID, q.Params.Protocol = a.cfg.Selected, a.cfg.Protocol
		}
		s, e := a.control("status", nil)
		if e != nil {
			return nil, e
		}
		if !coreIdle(s) {
			if a.router != nil && a.wantsTunnel() && q.Params.ID != nil && a.cfg.Selected != nil && *q.Params.ID == *a.cfg.Selected && q.Params.Protocol == a.cfg.Protocol {
				return s, nil
			}
			return nil, errors.New("Для нового подключения сначала отключите текущую сессию")
		}
		mode := q.Params.Protocol
		if mode == "" {
			mode = profile.Auto
		}
		if mode != profile.Auto && !profile.Transport(mode) {
			return nil, errors.New("Неизвестный протокол")
		}
		// An older release or a failed network request can leave a remote
		// reservation with an idle local core. Close it before starting again.
		if e = a.closeProvider(ctx); e != nil {
			return nil, fmt.Errorf("Не удалось закрыть предыдущую сессию Sota: %w", e)
		}
		var p json.RawMessage
		if q.Params.ID != nil {
			list, e := a.locations(ctx, a.key())
			if e != nil {
				return nil, e
			}
			found := false
			for _, item := range list {
				if item.ID == *q.Params.ID {
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("Выбранный сервер отсутствует в списке Sota")
			}
			a.servers = list
			next := a.cfg
			next.Selected = q.Params.ID
			next.Protocol = mode
			next.ProviderSession = true
			if e = a.save(next); e != nil {
				return nil, e
			}
			p, e = a.profile(ctx, *q.Params.ID, mode)
			if e != nil {
				return nil, a.failedConnect(ctx, e)
			}
		} else {
			b, err := os.ReadFile(a.file + ".profile")
			if err != nil {
				return nil, errors.New("Выберите сервер или импортируйте профиль")
			}
			p, e = tunnelProfile(b, mode)
			if e != nil {
				return nil, e
			}
			next := a.cfg
			next.Protocol, next.Selected = mode, nil
			if e = a.save(next); e != nil {
				return nil, e
			}
		}
		armed := false
		if a.router != nil {
			armed, e = setRouterPolicy(true)
			if e != nil {
				return nil, a.failedConnect(ctx, e)
			}
		}
		result, e := a.control("connect", p)
		if e == nil && a.router != nil {
			if e = a.setWanted(true); e != nil {
				_, stopErr := a.control("disconnect", nil)
				e = errors.Join(e, stopErr)
			}
		}
		if e != nil && armed {
			_, rollback := setRouterPolicy(false)
			e = errors.Join(e, rollback)
		}
		if e != nil && a.cfg.ProviderSession {
			if latest, err := a.control("status", nil); err == nil && coreIdle(latest) {
				return nil, a.failedConnect(ctx, e)
			}
		}
		return result, e
	case "tunnel.disconnect":
		if a.router != nil {
			return a.stopRouter(ctx, false)
		}
		s, e := a.control("disconnect", nil)
		if e != nil {
			return nil, e
		}
		if e = a.closeProvider(ctx); e != nil {
			return nil, fmt.Errorf("Локальный туннель остановлен. %w", e)
		}
		return s, nil
	case "service.suspend":
		if a.router == nil || !serviceStopping() {
			return nil, errors.New("Служба не останавливается")
		}
		return a.stopRouter(ctx, true)
	case "diagnostics.ip":
		return a.control("ip", nil)
	default:
		return nil, errors.New("Неизвестный метод")
	}
}
func (a *app) handler(api, web bool) http.Handler {
	web = web && standaloneAvailable
	files, _ := fs.Sub(assets, "web")
	static := http.FileServer(http.FS(files))
	bridge := web && api && os.Getenv("SOTA_OPENWRT_BRIDGE") == "1"
	target, _ := url.Parse("http://127.0.0.1:18082")
	proxy := httputil.NewSingleHostReverseProxy(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if web && r.URL.Path == "/backends" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"openwrt": bridge})
			return
		}
		if bridge && r.URL.Path == "/router-rpc" {
			r.URL.Path = "/rpc"
			proxy.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/rpc" {
			if !web || (r.Method != "GET" && r.Method != "HEAD") || (r.URL.Path != "/" && r.URL.Path != "/app.js" && r.URL.Path != "/routing.js" && r.URL.Path != "/i18n.js" && r.URL.Path != "/messages.json" && r.URL.Path != "/app.css") {
				http.NotFound(w, r)
				return
			}
			static.ServeHTTP(w, r)
			return
		}
		if !api {
			http.NotFound(w, r)
			return
		}
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			http.Error(w, "POST required", 405)
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			http.Error(w, "application/json required", 415)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "origin mismatch", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var q rpcRequest
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(&struct{}{}) != io.EOF || q.Version != "2.0" {
			http.Error(w, "invalid JSON-RPC request", 400)
			return
		}
		result, e := a.call(r.Context(), q)
		reply := map[string]any{"jsonrpc": "2.0", "id": q.ID}
		if e != nil {
			reply["error"] = map[string]any{"code": -32000, "message": e.Error()}
		} else {
			reply["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	})
}
func enabled(name string) (bool, error) {
	switch os.Getenv(name) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s", name)
	}
}
func start() error {
	dir := flag.String("state", "/var/lib/sota-panel", "private settings directory")
	router := flag.Bool("router", false, "serve the OpenWrt RPC adapter on a private Unix socket")
	method := flag.String("call", "", "invoke one OpenWrt RPC method with JSON parameters on stdin")
	installDefaults := flag.Bool("install-route-defaults", false, "initialize the built-in route lists once")
	compile := flag.Bool("compile-routes", false, "compile validated routing lists")
	flag.Parse()
	if *installDefaults {
		return installRouteDefaults()
	}
	if *compile {
		return compileRoutes()
	}
	if *method != "" {
		return routerCall(*method)
	}
	if !standaloneAvailable && !*router {
		return errors.New("this OpenWrt build requires -router")
	}
	api, e := enabled("SOTA_API_ENABLED")
	if e != nil {
		return e
	}
	web, e := enabled("SOTA_WEB_ENABLED")
	if e != nil {
		return e
	}
	if web && !api {
		return errors.New("SOTA_WEB_ENABLED requires SOTA_API_ENABLED")
	}
	if !api && !*router {
		return nil
	}
	a := &app{file: filepath.Join(*dir, "settings.json"), origin: providerOrigin, socket: "/run/sota-lab/control.sock"}
	key, k := os.LookupEnv("SOTA_ACCESS_KEY")
	file, f := os.LookupEnv("SOTA_ACCESS_KEY_FILE")
	if k && f {
		return errors.New("conflicting key sources")
	}
	if f {
		key, e = readSecret(file)
		if e != nil {
			return errors.New("key file unavailable")
		}
	}
	if (k || f) && !validSecret(key) {
		return errors.New("invalid key source")
	}
	a.envKey = key
	if e = a.load(); e != nil {
		return e
	}
	if *router {
		return a.serveRouter()
	}
	addr := os.Getenv("SOTA_HTTP_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	s := &http.Server{Addr: addr, Handler: a.handler(api, web), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 12 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 16 << 10}
	return s.ListenAndServe()
}
func main() {
	if e := start(); e != nil {
		log.Fatal(e)
	}
}
