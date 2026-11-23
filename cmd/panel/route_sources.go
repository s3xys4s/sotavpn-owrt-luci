package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type sourceList struct {
	URL  string `json:"url"`
	Text string `json:"text"`
}

type routeDocument struct {
	DefaultsInstalled bool             `json:"defaults_installed,omitempty"`
	Exceptions        storedExceptions `json:"exceptions,omitempty"`
	Manual            string           `json:"manual"`
	Lists             []sourceList     `json:"lists,omitempty"`
	Files             []storedRuleFile `json:"files,omitempty"`
	RefreshMinutes    int              `json:"refresh_minutes,omitempty"`
}

const (
	maxSourceBytes        = 2 << 20
	maxSourceTotal        = 8 << 20
	maxURLTotal           = 10 << 20
	defaultRefreshMinutes = 360
)

func validSourceURL(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// Check the actual resolved destination, including after redirects. LAN file servers are supported.
var listDialer = net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !ap.Addr().Unmap().IsGlobalUnicast() || ap.Addr().Unmap().IsLoopback() {
		return errors.New("недоступный адрес источника")
	}
	return nil
}}
var listClient = &http.Client{
	Timeout:   12 * time.Second,
	Transport: &http.Transport{DialContext: listDialer.DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 7 * time.Second, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 << 10},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !validSourceURL(r.URL) {
			return errors.New("недопустимое перенаправление")
		}
		return nil
	},
}

func downloadList(ctx context.Context, client *http.Client, address string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil || len(address) > 8192 || !validSourceURL(req.URL) {
		return "", errors.New("укажите ссылку HTTP или HTTPS без данных входа")
	}
	req.Header.Set("Accept", "text/plain, */*;q=0.1")
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("не удалось загрузить файл")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("сервер вернул HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxSourceBytes+1))
	if err != nil {
		return "", errors.New("не удалось прочитать ответ")
	}
	if len(b) > maxSourceBytes {
		return "", errors.New("файл больше 2 МиБ")
	}
	if !utf8.Valid(b) {
		return "", errors.New("ожидается текст в UTF-8")
	}
	body := string(b)
	list, err := parseEntries(body)
	if err != nil {
		return "", err
	}
	if _, builtin := presetSeeds[address]; builtin && len(list.ips)+len(list.domains) == 0 {
		return "", errors.New("Источник вернул пустой список")
	}
	return body, nil
}

func loadLists(ctx context.Context, client *http.Client, addresses []string, cached []sourceList) ([]sourceList, error) {
	if len(addresses) > 42 {
		return nil, errors.New("Можно добавить до 42 ссылок")
	}
	ctx, cancel := context.WithTimeout(ctx, 22*time.Second)
	defer cancel()
	var lists []sourceList
	saved := make(map[string]string, len(cached))
	for _, list := range cached {
		saved[list.URL] = list.Text
	}
	total := 0
	seen := map[string]bool{}
	for i, raw := range addresses {
		address := strings.TrimSpace(raw)
		if seen[address] {
			continue
		}
		body, found := saved[address]
		if seed, ok := presetSeeds[address]; !found && ok {
			body, found = seed.Text, true
		}
		if !found {
			var err error
			body, err = downloadList(ctx, client, address)
			if err != nil {
				return nil, fmt.Errorf("Список %d: %w", i+1, err)
			}
		}
		total += len(body)
		if total > maxURLTotal {
			return nil, errors.New("Общий размер загруженных списков превышает 10 МиБ")
		}
		seen[address] = true
		lists = append(lists, sourceList{URL: address, Text: body})
	}
	return lists, nil
}

// SmartDNS uses one rule per name. Combine apex-only and subdomain-only matches
// before emitting the rules so one source cannot overwrite another's match.
func (doc routeDocument) combined() (routeList, error) {
	ips := map[string]struct{}{}
	names := map[string]uint8{}
	add := func(list routeList) {
		for _, ip := range list.ips {
			ips[ip] = struct{}{}
		}
		for _, d := range list.domains {
			if name, ok := strings.CutPrefix(d, "-."); ok {
				names[name] |= 1
			} else if name, ok := strings.CutPrefix(d, "*."); ok {
				names[name] |= 2
			} else {
				names[d] = 3
			}
		}
	}
	manual, err := parseList(doc.Manual)
	if err != nil {
		return routeList{}, err
	}
	add(manual)
	for i, source := range doc.Lists {
		list, err := parseEntries(source.Text)
		if err != nil {
			return routeList{}, fmt.Errorf("Список %d: %w", i+1, err)
		}
		add(list)
	}
	for _, file := range doc.Files {
		list, err := parseEntries(file.Text)
		if err != nil {
			return routeList{}, fmt.Errorf("%s: %w", file.Name, err)
		}
		add(list)
	}
	var out routeList
	for ip := range ips {
		out.ips = append(out.ips, ip)
	}
	for name, mask := range names {
		switch mask {
		case 1:
			name = "-." + name
		case 2:
			name = "*." + name
		}
		out.domains = append(out.domains, name)
	}
	sort.Strings(out.ips)
	sort.Strings(out.domains)
	return out, nil
}
