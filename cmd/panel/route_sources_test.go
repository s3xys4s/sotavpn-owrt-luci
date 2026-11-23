package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRemoteAndManualRulesCombine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cidr":
			fmt.Fprint(w, "\ufeff1.1.1.1/32\r\n192.168.1.1/24")
		case "/domains":
			fmt.Fprint(w, "*.both.example\nonly.example\n\\*.wild.example")
		case "/bad":
			fmt.Fprint(w, "<html>not a list</html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	sources := []string{server.URL + "/cidr", server.URL + "/domains", server.URL + "/cidr"}
	cached, err := loadLists(context.Background(), server.Client(), sources, nil)
	if err != nil || len(cached) != 2 {
		t.Fatalf("load: %d, %v", len(cached), err)
	}
	doc := routeDocument{Manual: "both.example\n1.1.1.1\ngoogle.com", Lists: cached}
	rules, err := doc.combined()
	if err != nil || !reflect.DeepEqual(rules.ips, []string{"1.1.1.1", "192.168.1.0/24"}) || !reflect.DeepEqual(rules.domains, []string{"*.wild.example", "-.google.com", "-.only.example", "both.example"}) {
		t.Fatalf("union %+v %v", rules, err)
	}
	doc.Lists = doc.Lists[:1]
	rules, err = doc.combined()
	if err != nil || !reflect.DeepEqual(rules.domains, []string{"-.both.example", "-.google.com"}) {
		t.Fatalf("removal %+v %v", rules, err)
	}
	for _, path := range []string{"/bad", "/missing"} {
		if _, err := loadLists(context.Background(), server.Client(), []string{sources[0], server.URL + path}, nil); err == nil || !strings.Contains(err.Error(), "Список 2") {
			t.Fatalf("invalid list at %s: %v", path, err)
		}
	}
}
func TestLegacyBareDomainMigration(t *testing.T) {
	old := "payload:\r\n - 'legacy.example' # memo\r\n1.2.3.4\n*.kept.example\nDOMAIN,api.example"
	newText := migrateManual(old)
	if !strings.Contains(newText, "'+.legacy.example' # memo") || strings.Contains(newText, "+.1.2.3.4") {
		t.Fatal(newText)
	}
	rules, err := (routeDocument{Manual: newText}).combined()
	if err != nil || !reflect.DeepEqual(rules.domains, []string{"*.kept.example", "-.api.example", "legacy.example"}) {
		t.Fatalf("%+v %v", rules, err)
	}
}

func TestRefreshKeepsFailedSourceAndLocalRows(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/good" {
			fmt.Fprint(w, "new.example")
		} else {
			fmt.Fprint(w, "invalid domain")
		}
	}))
	defer server.Close()
	old := []sourceList{{URL: server.URL + "/good", Text: "old.example"}, {URL: server.URL + "/bad", Text: "keep.example"}}
	files := []storedRuleFile{{Name: "local.txt", Text: "*.keep.example\n192.0.2.1"}}
	n := networkSettings{document: routeDocument{Manual: "manual.example", Lists: old, Files: files}, storedHash: "before"}
	n.populateSources()
	revision := n.revision()
	next, failures := refreshSources(context.Background(), server.Client(), old)
	if len(failures) != 1 || requests != 2 || next[0].Text != "new.example" || next[1] != old[1] || old[0].Text != "old.example" {
		t.Fatalf("refresh: %v, %v, %d requests", next, failures, requests)
	}
	n.document.Lists = next
	n.storedHash = "after"
	n.populateSources()
	if n.revision() != revision {
		t.Fatal("background refresh invalidated the editor")
	}
	got, err := n.document.combined()
	if err != nil || !slices.Equal(got.domains, []string{"-.manual.example", "-.new.example", "keep.example"}) || !slices.Equal(got.ips, []string{"192.0.2.1"}) {
		t.Fatalf("combined: %+v %v", got, err)
	}
	if _, err = loadLists(context.Background(), server.Client(), n.Sources, next); err != nil || requests != 2 {
		t.Fatal("existing sources were downloaded during an edit")
	}
}

func TestStoredFileRows(t *testing.T) {
	first := storedRuleFile{Name: "rules.txt", Text: "example.org"}
	second := storedRuleFile{Name: first.Name, Text: "*.example.org"}
	saved := []storedRuleFile{first, second}
	for _, tc := range []struct {
		input []ruleFileInput
		want  []storedRuleFile
	}{
		{nil, saved}, {[]ruleFileInput{}, nil}, {[]ruleFileInput{second.reference()}, []storedRuleFile{second}},
		{[]ruleFileInput{{Name: first.Name, Text: &first.Text}, second.reference()}, saved},
	} {
		files, err := resolveRuleFiles(tc.input, saved)
		if err != nil || !slices.Equal(files, tc.want) {
			t.Fatalf("file rows: %v %v", files, err)
		}
	}
	bad := "bogus"
	for _, row := range []ruleFileInput{{Name: "rules.txt", Hash: "unknown"}, {Name: "x", Text: &bad}, {Name: "../rules", Text: &first.Text}} {
		if _, err := resolveRuleFiles([]ruleFileInput{row}, saved); err == nil {
			t.Fatalf("accepted invalid file %q", row.Name)
		}
	}
}
