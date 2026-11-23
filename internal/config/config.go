// Package config validates the headless candidate before any process starts.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sota-gateway/internal/profile"
)

type Config struct {
	StateDir, ProfileFile string
	Auto                  bool
	Sources               map[string]string
}

var defaults = map[string]string{
	"STATE_DIR": "/var/lib/sota-gateway", "PROFILE_FILE": "", "AUTOCONNECT": "false",
	"API_ENABLED": "false", "WEB_ENABLED": "false", "ACCESS_KEY": "", "ACCESS_KEY_FILE": "",
	"ROUTE_MODE": "full", "COUNTRY": "", "SERVER": "", "HTTP_LISTEN": "", "LOG_LEVEL": "info",
}

func fileValues(path string) (map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("cannot open configuration file")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("invalid configuration size")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, e := dec.Token()
	if e != nil || tok != json.Delim('{') {
		return nil, errors.New("expected JSON configuration object")
	}
	v := map[string]string{}
	for dec.More() {
		k, e := dec.Token()
		if e != nil {
			return nil, errors.New("invalid configuration field")
		}
		name, ok := k.(string)
		if !ok {
			return nil, errors.New("invalid configuration field")
		}
		if _, ok = defaults[name]; !ok {
			return nil, errors.New("unknown configuration field")
		}
		if _, ok = v[name]; ok {
			return nil, errors.New("duplicate configuration field")
		}
		var raw any
		if dec.Decode(&raw) != nil {
			return nil, errors.New("invalid field value")
		}
		switch x := raw.(type) {
		case string:
			v[name] = x
		case bool:
			v[name] = strconv.FormatBool(x)
		default:
			return nil, errors.New("expected a string or boolean")
		}
	}
	if _, e = dec.Token(); e != nil {
		return nil, errors.New("unterminated configuration")
	}
	if _, e = dec.Token(); e != io.EOF {
		return nil, errors.New("unexpected trailing configuration")
	}
	return v, nil
}

func Load() (Config, error) {
	c := Config{Sources: map[string]string{}}
	vals := map[string]string{}
	for k, v := range defaults {
		vals[k] = v
		c.Sources[k] = "default"
	}
	if path, ok := os.LookupEnv("SOTA_CONFIG_FILE"); ok {
		if !filepath.IsAbs(path) {
			return c, errors.New("SOTA_CONFIG_FILE requires an absolute path")
		}
		f, e := fileValues(path)
		if e != nil {
			return c, e
		}
		for k, v := range f {
			vals[k] = v
			c.Sources[k] = "file"
		}
	}
	for _, pair := range os.Environ() {
		k, v, _ := strings.Cut(pair, "=")
		if !strings.HasPrefix(k, "SOTA_") || k == "SOTA_CONFIG_FILE" {
			continue
		}
		name := strings.TrimPrefix(k, "SOTA_")
		if _, ok := defaults[name]; !ok {
			return c, errors.New("unknown SOTA_ environment variable")
		}
		vals[name] = v
		c.Sources[name] = "env"
	}
	for _, k := range []string{"WEB_ENABLED", "API_ENABLED", "AUTOCONNECT"} {
		if vals[k] != "true" && vals[k] != "false" {
			return c, fmt.Errorf("%s must be true or false", k)
		}
	}
	if vals["WEB_ENABLED"] == "true" && vals["API_ENABLED"] != "true" {
		return c, errors.New("WEB_ENABLED requires API_ENABLED")
	}
	if vals["API_ENABLED"] == "true" || vals["HTTP_LISTEN"] != "" {
		return c, errors.New("HTTP management unavailable in headless candidate")
	}
	if c.Sources["ACCESS_KEY"] != "default" && c.Sources["ACCESS_KEY_FILE"] != "default" {
		return c, errors.New("ACCESS_KEY and ACCESS_KEY_FILE are mutually exclusive")
	}
	if c.Sources["ACCESS_KEY"] != "default" || c.Sources["ACCESS_KEY_FILE"] != "default" {
		return c, errors.New("key-based provisioning unavailable; this adapter requires a provider profile file")
	}
	if vals["COUNTRY"] != "" || vals["SERVER"] != "" {
		return c, errors.New("server selection requires provider provisioning")
	}
	if vals["ROUTE_MODE"] != "full" {
		return c, errors.New("only full route mode implemented in lab")
	}
	if vals["LOG_LEVEL"] != "info" {
		return c, errors.New("only info log level implemented")
	}
	c.Auto = vals["AUTOCONNECT"] == "true"
	c.StateDir = vals["STATE_DIR"]
	c.ProfileFile = vals["PROFILE_FILE"]
	if !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) == "/" {
		return c, errors.New("STATE_DIR must name a private absolute directory")
	}
	if c.ProfileFile != "" && !filepath.IsAbs(c.ProfileFile) {
		return c, errors.New("PROFILE_FILE requires an absolute path")
	}
	if c.Auto && c.ProfileFile == "" {
		return c, errors.New("AUTOCONNECT requires PROFILE_FILE")
	}
	if c.ProfileFile != "" {
		if _, e := c.Profile(); e != nil {
			return c, e
		}
	}
	return c, nil
}

func (c Config) Profile() ([]byte, error) {
	f, e := os.Open(c.ProfileFile)
	if e != nil {
		return nil, errors.New("provider profile unavailable")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("profile requires a private regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil || len(b) > 1<<20 {
		return nil, errors.New("invalid provider profile size")
	}
	// Only accept the reviewed full-mode subset; upstream routing and arbitrary
	// inbounds in a supplied file could change the isolation contract.
	var p struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != 1 || json.Unmarshal(b, &p) != nil {
		return nil, errors.New("profile must contain only outbounds")
	}
	return profile.Full(p.Outbounds, profile.Auto)
}
