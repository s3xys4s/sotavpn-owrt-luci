package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"
)

func newDeviceHWID() (string, error) {
	return linuxHWID(os.DirFS("/"), os.Hostname)
}

func linuxHWID(root fs.FS, hostname func() (string, error)) (string, error) {
	id := ""
	if data, err := fs.ReadFile(root, "etc/machine-id"); err == nil && utf8.Valid(data) {
		id = strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	}
	if id == "" {
		info := releaseFields(root, "etc/os-release")
		if info == nil {
			info = releaseFields(root, "usr/lib/os-release")
		}
		lsb := releaseFields(root, "etc/lsb-release")
		field := func(key, legacy, fallback string) string {
			if value, ok := info[key]; ok {
				return value
			}
			if value, ok := lsb[legacy]; ok {
				return value
			}
			return fallback
		}
		fields := []string{
			field("PRETTY_NAME", "DISTRIB_DESCRIPTION", "Linux"),
			field("VERSION_ID", "DISTRIB_RELEASE", ""),
			field("ID", "DISTRIB_ID", "linux"),
		}
		var parts []string
		for _, value := range fields {
			if value != "" {
				parts = append(parts, value)
			}
		}
		id = strings.Join(parts, "|")
	}
	if id == "" {
		var err error
		id, err = hostname()
		if err != nil || id == "" {
			return "", errors.New("Не удалось определить HWID устройства")
		}
	}
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:]), nil
}

// device_info_plus reads literal key/value pairs rather than sourcing shell code.
func releaseFields(root fs.FS, path string) map[string]string {
	data, err := fs.ReadFile(root, path)
	if err != nil || !utf8.Valid(data) {
		return nil
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if ok && !strings.Contains(value, "=") {
			fields[key] = strings.TrimSuffix(strings.TrimPrefix(value, "\""), "\"")
		}
	}
	return fields
}
