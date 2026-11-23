// Package profile prepares the provider outbounds used by the full-tunnel policy.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
)

const Auto = "auto"

func Transport(name string) bool {
	switch name {
	case "vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic":
		return true
	}
	return false
}

type outbound struct {
	Type    string   `json:"type"`
	Tag     string   `json:"tag"`
	Detour  string   `json:"detour"`
	Members []string `json:"outbounds"`
	raw     json.RawMessage
}

// Full keeps the original URLTest policy in auto mode. A fixed protocol filters
// its members before passing them to kittend. Only encrypted leaves may be used.
func Full(raw []json.RawMessage, mode string) (json.RawMessage, error) {
	if mode != Auto && !Transport(mode) {
		return nil, errors.New("Неизвестный протокол")
	}
	var group *outbound
	var leaves []outbound
	tags := make(map[string]bool)
	for _, item := range raw {
		var o outbound
		if json.Unmarshal(item, &o) != nil {
			return nil, errors.New("Повреждён outbound")
		}
		o.raw = item
		if o.Tag != "" && tags[o.Tag] {
			return nil, errors.New("В профиле повторяется тег выхода")
		}
		tags[o.Tag] = true
		switch o.Type {
		case "direct", "dns", "block":
			continue
		case "urltest":
			if group != nil || o.Tag == "" || o.Detour != "" || len(o.Members) == 0 {
				return nil, errors.New("Некорректная группа urltest")
			}
			group = &o
		default:
			if !Transport(o.Type) || o.Detour != "" {
				return nil, errors.New("Профиль содержит неподдерживаемый выход или цепочку")
			}
			leaves = append(leaves, o)
		}
	}
	if group == nil && len(leaves) != 1 {
		return nil, errors.New("В профиле требуется один VPN-выход или группа urltest")
	}
	if group != nil {
		byTag := make(map[string]outbound, len(leaves))
		for _, o := range leaves {
			byTag[o.Tag] = o
		}
		leaves = nil
		for _, tag := range group.Members {
			o, ok := byTag[tag]
			if !ok || tag == "" {
				return nil, errors.New("Группа urltest ссылается на недоступный или повторный выход")
			}
			leaves = append(leaves, o)
			delete(byTag, tag)
		}
		if len(byTag) != 0 {
			return nil, errors.New("Профиль содержит VPN-выход вне группы urltest")
		}
	}
	var selected []json.RawMessage
	var names []string
	for _, o := range leaves {
		if mode == Auto || o.Type == mode {
			selected = append(selected, o.raw)
			names = append(names, o.Tag)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("В профиле этого сервера отсутствует протокол %s", mode)
	}
	if group != nil {
		g := group.raw
		if mode != Auto {
			if len(selected) == 1 {
				// Keep the primary outbound tag used by the provider.
				selected[0] = replaceField(selected[0], "tag", group.Tag)
				return pack(selected)
			}
			g = replaceField(g, "outbounds", names)
		}
		selected = append([]json.RawMessage{g}, selected...)
	}
	return pack(selected)
}

func replaceField(raw json.RawMessage, field string, value any) json.RawMessage {
	// raw was already decoded as an outbound; values are strings or []string.
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	fields[field], _ = json.Marshal(value)
	result, _ := json.Marshal(fields)
	return result
}

func pack(outbounds []json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}{outbounds})
}
