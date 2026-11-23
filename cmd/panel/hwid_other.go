//go:build !linux

package main

import (
	"crypto/rand"
	"encoding/hex"
)

// The gateway ships on Linux. Preserve the existing identity behavior for
// development builds on other platforms.
func newDeviceHWID() (string, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
