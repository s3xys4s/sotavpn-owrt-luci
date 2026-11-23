# Working on Sota Gateway

Start with [README.md](README.md) or [README.ru.md](README.ru.md). Both contain the full build from a clean checkout. The router release consists of `sota-gateway` and `luci-app-sota`. Make changes in the source tree and rebuild the packages. The SDK package staging area and the output under `build/` are generated.

## Where to change things

- For connection state, recovery, profiles or HWID, start in `cmd/panel/`. `cmd/panel/router.go` connects the controller to the OpenWrt core. `internal/core/` implements the IPC adapter and `internal/profile/` handles transport profiles.
- For routing lists, inspect `cmd/panel/route_*.go`, `cmd/panel/routing.go` and `cmd/panel/presets/`. For changes to the form, edit `cmd/panel/web/routing.js`; the package recipe installs the same file into LuCI. The English strings live in `cmd/panel/web/messages.json`, and the menu translation lives in `openwrt/luci-app-sota/po/ru/sota.po`.
- For router lifecycle, inspect `openwrt/sota-gateway/files/etc/init.d/sota` and `files/usr/libexec/sota-policy` under that package. For a new LuCI RPC method, update the Go handler, the rpcd adapter, the LuCI ACL and its caller together.
- For standalone UI controls, edit `cmd/panel/web/`. The LuCI views and toolbar are under `openwrt/luci-app-sota/files/www/luci-static/resources/`. Keep both interfaces consistent when changing shared features. Router controllers are built with `-tags=luci_only`.
- The original engine comes from the pinned Sota Connect release. Its compatibility contract is in `research/contract-v1.json`. The extractor and the package script check the upstream artifact. Changes to the supported upstream version need matching compatibility evidence.

## Verify a change

Run the Go suite on Linux. Local tests use loopback HTTP fixtures. Format changed Go files with `gofmt`.

```sh
go test ./cmd/... ./internal/...
go test -tags=luci_only ./cmd/... ./internal/...
```

Choose integration tests to match the change. The OpenWrt lab scripts operate on a dedicated router/client VM pair and temporarily modify its settings. The service test actually reboots the guests. Paths, expected starting state and prerequisites are documented in the scripts and the [regression report](openwrt/REGRESSION.md).

| Change | Verification |
| --- | --- |
| Transport, routing, lists or exceptions | `tools/openwrt_regression.py` measures client TCP/UDP egress and DNS. Exact and wildcard domains, overlapping CIDR and HTTP refresh are distinct cases. |
| Service startup or cleanup | `tools/openwrt_service_regression.py` checks automatic restoration after boot and a persistent manual Stop. Compare client egress and the router's rules with the expected state. |
| DNS or DoH | `tools/openwrt_dns_regression.py` and `tools/openwrt_doh_regression.py` run against installed router filesystems in private network and mount namespaces. |
| UI or localization | Run `tools/openwrt_luci_regression.mjs` with the lab's LuCI URL. Run `tools/standalone_regression.mjs` with the standalone URL and the LuCI Sota directory URL. These require Node 22+ and Chrome/Chromium. Set `CHROME` for a custom executable. |
| Router packaging | Use the README build for each affected release. Inspect the IPK/APK and APK signature. For a release bump, keep both package recipes, install notes and archive names consistent. |

## State and repository hygiene

Preserve existing keys, HWIDs, UCI settings and user lists across upgrades. Start and Restart use the selected server and transport. Manual Stop restores ordinary WAN and DNS, and survives reboot. A process crash with an active tunnel calls for reconnection. Report which of those paths you tested.

Use fixture credentials in tests. Keep subscription keys, provider profiles, real device state, APK private keys, browser profiles and raw diagnostic logs outside Git. A package build needs no subscription. Check the staged file list before committing. `tools/lab/history/` holds older one-off browser probes; the maintained regression entry points are in the table above.

