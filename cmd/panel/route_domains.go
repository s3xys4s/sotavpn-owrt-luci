package main

import (
	"fmt"
	"strings"
)

// Each group is loaded into its own SmartDNS stage. SmartDNS uses one nftset
// target per name and otherwise overwrites an apex rule with a wildcard rule.
func domainSetConfig(domains []string, set string) []byte {
	var out strings.Builder
	for _, domain := range domains {
		fmt.Fprintf(&out, "nftset /%s/#4:inet#sota_guard#%s\n", domain, set)
	}
	return []byte(out.String())
}
