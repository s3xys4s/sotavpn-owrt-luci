package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderSession(t *testing.T) {
	const secret = "synthetic-integration-key"
	a := &app{file: filepath.Join(t.TempDir(), "config")}
	if e := a.load(); e != nil {
		t.Fatal(e)
	}
	initial := a.cfg.Device
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Access-Key") != secret || r.Header.Get("X-HWID") != initial || len(initial) != 64 {
			http.Error(w, "denied", 403)
			return
		}
		switch r.URL.Path {
		case "/subscription/profile":
			w.Write([]byte(`{"code":200,"response":{"active":true}}`))
		case "/connection/list":
			w.Write([]byte(`{"code":200,"response":[{"id":3,"name":"Helsinki","shortname":"FI"}]}`))
		case "/connection/connect":
			if r.URL.Query().Get("gate_id") != "3" {
				t.Error("gate id")
			}
			w.Write([]byte(`{"response":{"configuration":"{\"outbounds\":[{\"type\":\"trojan\",\"tag\":\"proxy\"}]}"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer p.Close()
	a.origin = p.URL
	q := rpcRequest{Method: "account.setKey"}
	q.Params.Key = secret
	if _, e := a.call(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	profile, e := a.profile(context.Background(), 3, "auto")
	if e != nil || !json.Valid(profile) {
		t.Fatal(e)
	}
	q.Params.Key = "rejected-key"
	if _, e = a.call(context.Background(), q); e == nil || strings.Contains(e.Error(), q.Params.Key) {
		t.Fatal("credential failure handling")
	}
	a.cfg = settings{}
	if e = a.load(); e != nil || a.cfg.Key != secret || a.cfg.Device != initial {
		t.Fatal("persistence")
	}
}

func TestHTTPBoundary(t *testing.T) {
	a := &app{}
	for _, c := range []struct {
		web          bool
		path, origin string
		want         int
	}{
		{true, "/", "", 200}, {false, "/", "", 404},
		{true, "/rpc", "http://panel.test", 200}, {false, "/rpc", "", 200},
		{true, "/rpc", "http://other.invalid", 403},
	} {
		method := "GET"
		if c.path == "/rpc" {
			method = "POST"
		}
		r := httptest.NewRequest(method, "http://panel.test"+c.path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"unknown"}`))
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.handler(true, c.web).ServeHTTP(w, r)
		if !standaloneAvailable && c.path == "/" {
			c.want = http.StatusNotFound
		}
		if w.Code != c.want {
			t.Errorf("%s got %d want %d", c.path, w.Code, c.want)
		}
		if w.Header().Get("WWW-Authenticate") != "" {
			t.Error("unexpected login challenge")
		}
	}
}

func TestPreviouslyRegisteredHWID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	stored := strings.Repeat("af", 32)
	body := `{"device":"` + stored + `"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	a := app{file: path}
	if err := a.load(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || a.cfg.Device != stored || string(b) != body {
		t.Fatal("loading changed existing registration")
	}
}
