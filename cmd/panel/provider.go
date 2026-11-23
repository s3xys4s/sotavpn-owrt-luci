package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sota-gateway/internal/profile"
)

const providerOrigin = "https://meowcon.net/api/v1/public"

var transport = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

type location struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Country string `json:"shortname"`
}
type settings struct {
	Key             string `json:"key,omitempty"`
	Device          string `json:"device"`
	Selected        *int64 `json:"selected,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	ProviderSession bool   `json:"provider_session,omitempty"`
	WantTunnel      *bool  `json:"want_tunnel,omitempty"`
}

func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (a *app) save(next settings) error {
	data, e := json.Marshal(next)
	if e != nil || atomicFile(a.file, data) != nil {
		return errors.New("Не удалось сохранить настройки")
	}
	a.cfg = next
	return nil
}
func (a *app) load() error {
	b, e := os.ReadFile(a.file)
	if os.IsNotExist(e) {
		hwid, err := newDeviceHWID()
		if err != nil {
			return err
		}
		return a.save(settings{Device: hwid})
	}
	if e != nil || json.Unmarshal(b, &a.cfg) != nil {
		return errors.New("Не удалось прочитать настройки")
	}
	v, e := hex.DecodeString(a.cfg.Device)
	if e != nil || len(v) != 32 {
		return errors.New("Повреждён идентификатор устройства")
	}
	return nil
}
func (a *app) key() string {
	if a.envKey != "" {
		return a.envKey
	}
	return a.cfg.Key
}

func (a *app) provider(ctx context.Context, path, key string) (json.RawMessage, error) {
	if key == "" {
		return nil, errors.New("Введите ключ подписки Sota")
	}
	q, e := http.NewRequestWithContext(ctx, http.MethodGet, a.origin+path, nil)
	if e != nil {
		return nil, errors.New("Ошибка запроса")
	}
	q.Header.Set("X-Access-Key", key)
	q.Header.Set("X-HWID", a.cfg.Device)
	q.Header.Set("Accept", "application/json")
	q.Header.Set("User-Agent", "Sota-Gateway-Lab/1.0 (Linux arm64)")
	r, e := transport.Do(q)
	if e != nil {
		return nil, errors.New("API Sota недоступен по HTTPS")
	}
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return nil, errors.New("Некорректный ответ API Sota")
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, fmt.Errorf("Sota отклонила запрос (HTTP %d)", r.StatusCode)
	}
	var wrapper struct {
		Code     int             `json:"code"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(b, &wrapper) == nil && len(wrapper.Response) > 0 {
		if wrapper.Code >= 400 {
			return nil, fmt.Errorf("Ошибка API Sota (%d)", wrapper.Code)
		}
		b = wrapper.Response
	}
	return b, nil
}
func (a *app) locations(ctx context.Context, key string) ([]location, error) {
	b, e := a.provider(ctx, "/connection/list", key)
	if e != nil {
		return nil, e
	}
	var list []location
	if json.Unmarshal(b, &list) != nil || len(list) == 0 {
		return nil, errors.New("Sota вернула неизвестный формат списка серверов")
	}
	seen := map[int64]bool{}
	for _, v := range list {
		if v.ID < 0 || v.Name == "" || seen[v.ID] {
			return nil, errors.New("Некорректный список серверов")
		}
		seen[v.ID] = true
	}
	return list, nil
}
func tunnelProfile(b json.RawMessage, mode string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(b, &object) == nil && object["configuration"] != nil {
		b = object["configuration"]
	}
	var asString string
	if json.Unmarshal(b, &asString) == nil {
		b = []byte(asString)
	}
	var p struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if json.Unmarshal(b, &p) != nil {
		return nil, errors.New("Не удалось прочитать профиль Sota")
	}
	return profile.Full(p.Outbounds, mode)
}
func (a *app) profile(ctx context.Context, id int64, mode string) (json.RawMessage, error) {
	b, e := a.provider(ctx, "/connection/connect?gate_id="+strconv.FormatInt(id, 10), a.key())
	if e != nil {
		return nil, e
	}
	return tunnelProfile(b, mode)
}
func validSecret(s string) bool {
	return len(s) > 0 && len(s) <= 512 && strings.IndexFunc(s, func(r rune) bool { return r < 33 || r > 126 }) < 0
}
func readSecret(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	return string(bytes.TrimSpace(b)), nil
}
