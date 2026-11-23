package main

import (
	"reflect"
	"testing"
)

func TestRouteListImport(t *testing.T) {
	got, err := parseList("\ufeffpayload:\r\n - 'IP-CIDR,192.0.2.17/24,no-resolve' # note\n - DOMAIN,API.EXAMPLE.ORG\n - DOMAIN-SUFFIX,example.net\n||example.net^\n*.only.example\n")
	if err != nil || !reflect.DeepEqual(got.ips, []string{"192.0.2.0/24"}) || !reflect.DeepEqual(got.domains, []string{"*.only.example", "-.api.example.org", "example.net"}) {
		t.Fatalf("unexpected rules %+v, %v", got, err)
	}
	for _, s := range []string{"DOMAIN-KEYWORD,vpn", "192.0.2.500", "2001:db8::1", "*.a*.com", "example.org; drop", "DOMAIN-SUFFIX,x.org,DIRECT", "IP-CIDR,example.com"} {
		if _, e := parseList(s); e == nil {
			t.Errorf("unexpected acceptance of %q", s)
		}
	}
}
