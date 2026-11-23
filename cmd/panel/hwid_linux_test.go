package main

import (
	"testing"
	"testing/fstest"
)

// Captured X-HWID values from the unmodified Sota Connect 1.8.11+1892 Linux
// client sending its subscription/profile request in an offline fixture.
func TestLinuxHWIDMatchesSotaConnect(t *testing.T) {
	const release = "PRETTY_NAME=\"OpenWrt Fixture Pretty\"\nVERSION=\"Fixture Version\"\nVERSION_ID=fixture-version-id\nID=openwrt\n"
	for _, tc := range []struct{ name, machine, release, lsb, want string }{
		{"machine-id", "0123456789abcdef0123456789abcdef\n", release, "", "3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9"},
		{"OpenWrt", "", release, "", "60f940410e1cf9ab651a99248575f902c32f501e5c7af4c95b30c208029b0252"},
		{"hostname", "", "PRETTY_NAME=\"\"\nVERSION_ID=\"\"\nID=\"\"\n", "", "97c1a9a4e8ac3c65406b02f980d068d4e170b83e7b0dc37c6d8b82c7b3a9622b"},
		{"lsb", "", "NAME=Distro\n", "DISTRIB_DESCRIPTION=\"Legacy Pretty\"\nDISTRIB_RELEASE=\"Legacy Release\"\nDISTRIB_ID=\"Legacy ID\"\n", "4f0e5c4321a76eac6901473c86bb2d5f374ee84616f13fb667c9a1de6d025650"},
		{"spaces", "", "PRETTY_NAME=\"  Demo  \"\nVERSION_ID=\" 1.0 \"\nID=\" a \"\n", "", "6243bab52363fe0e9afbf97bbcf6c35de71936f8f1db7f7ca6c2b35c966ad2e6"},
		{"whitespace field", "", "PRETTY_NAME=\"\"\nVERSION_ID=\"   \"\nID=\"\"\n", "", "0aad7da77d2ed59c396c99a74e49f3a4524dcdbcb5163251b1433d640247aeb4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fstest.MapFS{"etc/os-release": {Data: []byte(tc.release)}}
			if tc.machine != "" {
				root["etc/machine-id"] = &fstest.MapFile{Data: []byte(tc.machine)}
			}
			if tc.lsb != "" {
				root["etc/lsb-release"] = &fstest.MapFile{Data: []byte(tc.lsb)}
			}
			got, err := linuxHWID(root, func() (string, error) { return "sota-fixture", nil })
			if err != nil || got != tc.want {
				t.Fatalf("X-HWID differs from original client: %v", err)
			}
		})
	}
}
