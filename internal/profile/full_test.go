package profile

import (
	"encoding/json"
	"testing"
)

func TestURLTestAndFixedProtocols(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"type":"vless","tag":"tcp","flow":"test"}`),
		json.RawMessage(`{"type":"urltest","tag":"proxy","outbounds":["quic","tcp"],"interval":"3m"}`),
		json.RawMessage(`{"type":"hysteria2","tag":"quic","server_port":12345}`),
		json.RawMessage(`{"type":"direct","tag":"bypass"}`),
	}
	for _, mode := range []string{Auto, "vless", "hysteria2"} {
		b, err := Full(raw, mode)
		if err != nil {
			t.Fatal(err)
		}
		var p struct{ Outbounds []outbound }
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		root := p.Outbounds[0]
		if root.Tag != "proxy" {
			t.Fatalf("lost primary tag in %s", mode)
		}
		if mode == Auto {
			if len(p.Outbounds) != 3 || root.Type != "urltest" || root.Members[0] != "quic" || p.Outbounds[1].Tag != "quic" {
				t.Fatal("invalid automatic group")
			}
		} else if len(p.Outbounds) != 1 || root.Type != mode {
			t.Fatalf("unexpected fallback for %s", mode)
		}
	}
	if _, err := Full(raw, "trojan"); err == nil {
		t.Fatal("accepted absent protocol")
	}
	for _, members := range []string{`["tcp","bypass"]`, `["unknown","tcp"]`, `["quic","quic"]`, `["proxy","tcp"]`} {
		raw[1] = json.RawMessage(`{"type":"urltest","tag":"proxy","outbounds":` + members + `}`)
		if _, err := Full(raw, Auto); err == nil {
			t.Fatal("accepted unsafe group", members)
		}
	}
}
