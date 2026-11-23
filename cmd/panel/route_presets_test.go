package main

import (
	"slices"
	"strings"
	"testing"
)

func TestBundledListsUsePlainRules(t *testing.T) {
	if len(servicePresets) != 10 {
		t.Fatal("missing service lists")
	}
	for _, p := range servicePresets {
		seed := presetSeeds[p.URL]
		list, err := parseEntries(seed.Text)
		if err != nil || len(list.domains)+len(list.ips) == 0 {
			t.Fatalf("%s %s: %v", p.Name, p.Kind, err)
		}
		if strings.ContainsAny(seed.Text, ",") || !strings.Contains(p.URL, "format=text") {
			t.Fatal(p.URL)
		}
	}
	list, err := parseEntries(russianRules)
	if err != nil || len(list.ips) != 8580 || !slices.Contains(list.domains, "*.ru") || !slices.Contains(list.domains, "-.yandex-pogoda.static-storage.net") || strings.Contains(russianRules, "+.") {
		t.Fatalf("RU snapshot: %d networks, %v", len(list.ips), err)
	}
}

func TestDomainExceptionTargets(t *testing.T) {
	route := string(domainSetConfig([]string{"*.mixed.example"}, "resolved"))
	excluded := string(domainSetConfig([]string{"-.mixed.example"}, "bypass_resolved"))
	if route != "nftset /*.mixed.example/#4:inet#sota_guard#resolved\n" || excluded != "nftset /-.mixed.example/#4:inet#sota_guard#bypass_resolved\n" {
		t.Fatal(route, excluded)
	}
}
