package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"sota-gateway/internal/core"
)

const rpcSocket = "/var/run/sota/rpc.sock"
const policyTool = "/usr/libexec/sota-policy"

type event struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type routerControl struct {
	uploads  map[string]*routeUpload
	schedule chan time.Duration
	core     core.Adapter
}

func (r *app) record(level, message string) {
	r.events = append(r.events, event{time.Now().UTC().Format(time.RFC3339), level, message})
	if len(r.events) > 100 {
		r.events = append([]event(nil), r.events[len(r.events)-100:]...)
	}
	log.Printf("%s %s", level, message)
}

func (a *app) serveRouter() error {
	a.router = &routerControl{core: core.Adapter{Endpoint: "/var/run/sota/core.sock", Interface: "sota0"}, schedule: make(chan time.Duration, 1)}
	if err := os.Remove(rpcSocket); err != nil && !os.IsNotExist(err) {
		return err
	}
	l, err := net.Listen("unix", rpcSocket)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chmod(rpcSocket, 0600); err != nil {
		return err
	}
	a.record("info", "Служба управления запущена")
	if a.cfg.WantTunnel == nil {
		// Older releases recorded a live subscription reservation but no intent.
		if err := a.setWanted(a.cfg.ProviderSession && a.cfg.Selected != nil); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.runListRefresh(ctx)
	go a.maintainTunnel(ctx)
	if os.Getenv("SOTA_LOCAL_RPC") == "1" {
		local, err := net.Listen("tcp4", "127.0.0.1:18082")
		if err != nil {
			return err
		}
		defer local.Close()
		go (&http.Server{Handler: a.handler(true, false), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 12 * time.Second, WriteTimeout: 70 * time.Second}).Serve(local)
	}
	return (&http.Server{Handler: a.handler(true, false), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 70 * time.Second}).Serve(l)
}

func routerCall(method string) error {
	params, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(params)) == 0 {
		params = []byte("{}")
	}
	var args map[string]json.RawMessage
	if len(params) > 1<<20 || json.Unmarshal(params, &args) != nil || args == nil {
		return errors.New("invalid parameters")
	}
	// rpcd attaches the authenticated LuCI session to HTTP-originated calls.
	delete(args, "ubus_rpc_session")
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": args})
	if err != nil {
		return errors.New("invalid parameters")
	}
	client := http.Client{Timeout: 65 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", rpcSocket)
	}}}
	resp, err := client.Post("http://localhost/rpc", "application/json", bytes.NewReader(req))
	if err != nil {
		return errors.New("router service unavailable")
	}
	defer resp.Body.Close()
	var v struct {
		Result json.RawMessage
		Error  *struct{ Message string }
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&v) != nil {
		return errors.New("invalid RPC reply")
	}
	if v.Error != nil {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": v.Error.Message})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]json.RawMessage{"data": v.Result})
}

func (r *routerControl) status() (json.RawMessage, error) {
	s, err := r.core.Status()
	return json.Marshal(map[string]any{"controller_alive": true, "core_responding": err == nil, "core_state": s.State, "policy_enabled": policy("check") == nil})
}
func policy(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, policyTool, action).Run()
}

// An idle upstream core can retain the TUN until its process exits.
// procd restarts only the core while the forwarding guard stays in place.
func (r *routerControl) releaseTun() error {
	restarted := false
	for attempt := 0; attempt < 48; attempt++ {
		state, err := r.core.Status()
		if err == nil && state.State != nil && *state.State == 0 {
			if _, err := net.InterfaceByName("sota0"); err != nil {
				return nil
			}
			if attempt >= 4 && !restarted {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err = exec.CommandContext(ctx, "ubus", "call", "service", "signal", `{"name":"sota","instance":"core","signal":15}`).Run()
				cancel()
				if err != nil {
					break
				}
				restarted = true
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errors.New("Ядро ещё освобождает TUN")
}

func (r *routerControl) control(op string, p json.RawMessage) (json.RawMessage, error) {
	switch op {
	case "status":
		return r.status()
	case "disconnect":
		if err := r.core.Stop(); err != nil {
			return nil, errors.New("Ядро не подтвердило остановку")
		}
		if err := r.releaseTun(); err != nil {
			return nil, err
		}
		return r.status()
	case "connect":
		if policy("check") != nil {
			return nil, errors.New("Включите правила маршрутизации и примените настройки")
		}
		if err := r.releaseTun(); err != nil {
			return nil, err
		}
		if err := r.core.Start(p); err != nil {
			r.core.Stop()
			return nil, errors.New("Ядро отклонило подключение")
		}
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) {
			s, err := r.core.Status()
			if err != nil || s.State == nil || *s.State == 4 {
				break
			}
			if *s.State == 2 && policy("route") == nil {
				return r.status()
			}
			time.Sleep(150 * time.Millisecond)
		}
		r.core.Stop()
		return nil, errors.New("Не удалось запустить TUN и маршрут")
	case "ip":
		wan := routerProbe(0)
		tun := map[string]any{"ip": nil, "status": "disconnected"}
		if _, err := net.InterfaceByName("sota0"); err == nil {
			tun = routerProbe(0x534f)
		}
		return json.Marshal(map[string]any{"wan": wan, "tunnel": tun, "same_ip": wan["ip"] != nil && wan["ip"] == tun["ip"], "time": time.Now().UTC()})
	}
	return nil, errors.New("Неизвестная операция")
}

func routerProbe(mark int) map[string]any {
	d := net.Dialer{Timeout: 5 * time.Second, Control: routeMark(mark)}
	client := http.Client{Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true}, Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	v := map[string]any{"ip": nil, "status": "unavailable"}
	resp, err := client.Get("https://1.1.1.1/cdn-cgi/trace")
	if err != nil {
		return v
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil || resp.StatusCode != 200 {
		return v
	}
	for _, line := range strings.Split(string(body), "\n") {
		if addr, ok := strings.CutPrefix(line, "ip="); ok {
			ip := net.ParseIP(strings.TrimSpace(addr))
			if ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return map[string]any{"ip": ip.String(), "status": "ok"}
			}
		}
	}
	return v
}

func (a *app) logAction(method string, err error) {
	labels := map[string]string{"network.set": "Применение правил", "account.setKey": "Проверка ключа", "server.list": "Список серверов", "profile.import": "Импорт профиля", "tunnel.connect": "Подключение", "tunnel.disconnect": "Отключение", "diagnostics.ip": "Проверка IP"}
	if label, ok := labels[method]; ok {
		if err != nil {
			a.record("error", fmt.Sprintf("%s: %s", label, err))
		} else {
			a.record("info", label+" · успешно")
		}
	}
}
