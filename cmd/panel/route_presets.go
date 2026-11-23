package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"slices"
)

//go:embed presets/*.txt
var bundledRules embed.FS

type servicePreset struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

var servicePresets, presetSeeds = buildPresetCatalog()

const russianName = "Российские сайты (Sota Connect).txt"

//go:embed presets/sota-ru.txt
var russianRules string

var russianFile = storedRuleFile{Name: russianName, Text: russianRules}

func buildPresetCatalog() ([]servicePreset, map[string]sourceList) {
	var catalog []servicePreset
	seeds := make(map[string]sourceList)
	for _, p := range []struct{ id, name, query string }{
		{"youtube", "YouTube", "site=youtube.com"},
		{"discord", "Discord", "group=discord"},
		{"meta", "Meta", "site=facebook.com&site=messenger.com&site=whatsapp.com"},
		{"instagram", "Instagram", "site=instagram.com"},
		{"twitter", "Twitter / X", "site=x.com"},
	} {
		for _, kind := range []string{"domains", "cidr4"} {
			item := servicePreset{Name: p.name, Kind: kind, URL: "https://iplist.opencck.org/?format=text&data=" + kind + "&" + p.query}
			data, err := bundledRules.ReadFile("presets/" + p.id + "-" + kind + ".txt")
			if err != nil {
				panic(err)
			}
			seeds[item.URL] = sourceList{URL: item.URL, Text: string(data)}
			catalog = append(catalog, item)
		}
	}
	return catalog, seeds
}

type exceptionSettings struct {
	Rules   string          `json:"rules"`
	Sources []string        `json:"sources"`
	Files   []ruleFileInput `json:"files"`
}

type storedExceptions struct {
	Manual string           `json:"manual,omitempty"`
	Lists  []sourceList     `json:"lists,omitempty"`
	Files  []storedRuleFile `json:"files,omitempty"`
}

func (s storedExceptions) public() *exceptionSettings {
	e := &exceptionSettings{Rules: s.Manual}
	for _, src := range s.Lists {
		e.Sources = append(e.Sources, src.URL)
	}
	for _, file := range s.Files {
		e.Files = append(e.Files, file.reference())
	}
	return e
}

func (s storedExceptions) combined() (routeList, error) {
	return (routeDocument{Manual: s.Manual, Lists: s.Lists, Files: s.Files}).combined()
}

func prepareExceptions(ctx context.Context, input *exceptionSettings, old storedExceptions) (storedExceptions, error) {
	if input == nil {
		return old, nil
	}
	if _, err := parseList(input.Rules); err != nil {
		return old, fmt.Errorf("Исключения: %w", err)
	}
	saved := old.Files
	if input.Files != nil {
		saved = append(slices.Clone(old.Files), russianFile)
	}
	files, err := resolveRuleFiles(input.Files, saved)
	if err != nil {
		return old, fmt.Errorf("Исключения: %w", err)
	}
	addresses := input.Sources
	if addresses == nil {
		addresses = old.public().Sources
	}
	lists, err := loadLists(ctx, listClient, addresses, old.Lists)
	if err != nil {
		return old, fmt.Errorf("Исключения: %w", err)
	}
	return storedExceptions{Manual: input.Rules, Lists: lists, Files: files}, nil
}

func installRouteDefaults() error {
	n, err := getNetwork()
	if err != nil || n.document.DefaultsInstalled {
		return err
	}
	oldHash := n.storedHash
	for _, preset := range servicePresets {
		if !slices.Contains(n.Sources, preset.URL) {
			n.document.Lists = append(n.document.Lists, presetSeeds[preset.URL])
		}
	}
	if !slices.Contains(n.document.Exceptions.Files, russianFile) {
		n.document.Exceptions.Files = append(n.document.Exceptions.Files, russianFile)
	}
	n.document.DefaultsInstalled = true
	if err := persistNetwork(&n); err != nil {
		return err
	}
	if oldHash != "" && oldHash != n.storedHash {
		os.Remove(routeListFile + "." + oldHash)
	}
	return nil
}
