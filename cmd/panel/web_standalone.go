//go:build !luci_only

package main

import "embed"

const standaloneAvailable = true

//go:embed web/*
var assets embed.FS
