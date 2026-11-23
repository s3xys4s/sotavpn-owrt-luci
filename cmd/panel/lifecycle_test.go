package main

import (
	"path/filepath"
	"testing"
)

func TestPersistentTunnelIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{file: path}
	if err := a.load(); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false} {
		if err := a.setWanted(on); err != nil {
			t.Fatal(err)
		}
		fresh := &app{file: path}
		if err := fresh.load(); err != nil {
			t.Fatal(err)
		}
		if fresh.wantsTunnel() != on {
			t.Fatal("lost tunnel intent on restart")
		}
	}
}

func TestDoHEndpoint(t *testing.T) {
	for _, s := range []string{"", "https://dns.google/dns-query", "https://resolver.example:8443/dns-query?profile=family", "https://1.1.1.1/dns-query"} {
		if !validDoH(s) {
			t.Fatalf("valid DoH URL rejected: %q", s)
		}
	}
	for _, s := range []string{"http://dns.google/dns-query", "https://user@host/path", "https://host:/dns-query", "https://host:65536/path", "https://host/path\nserver 192.0.2.1", "https://host", "https://host/path#local", "https://host/'"} {
		if validDoH(s) {
			t.Fatalf("invalid DoH URL accepted: %q", s)
		}
	}
}
