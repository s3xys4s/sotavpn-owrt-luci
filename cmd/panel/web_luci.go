//go:build luci_only

package main

import "embed"

const standaloneAvailable = false

// The router serves its UI through LuCI. No standalone assets are embedded.
var assets embed.FS
