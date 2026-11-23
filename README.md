# Sota Gateway

[Русский](README.ru.md) · [Agent guide](AGENTS.md)

Sota Gateway runs a Sota Connect VPN on an ARM64 OpenWrt router. Devices on the LAN use the router as their gateway. LuCI lets you choose a server and transport, route selected IPv4 networks and domains through VPN or WAN, and compare the public IP of each path.

- **Connection.** Auto follows the provider profile. VLESS and Hysteria2 select a transport explicitly. Start remembers the connection across reboots. Stop restores normal routing and DNS and persists across reboots. Restart reconnects the selected server. OpenWrt's `procd` supervises the service.
- **Routing and DNS.** Enter one IPv4/CIDR, exact domain or wildcard per line. Combine manual rules, uploaded TXT files and periodically refreshed HTTP(S) lists. Exceptions take priority and go through WAN. Set a DNS resolver or a DNS over HTTPS endpoint with bootstrap DNS. Domain rules use IPv4 answers from the gateway's resolver. The active policy blocks IPv6 forwarding on the selected LAN interface.
- **Management.** LuCI follows the OpenWrt language and includes IP diagnostics, recent logs and a read-only HWID field. The router packages use LuCI exclusively. The repository also contains the optional standalone HTTP panel for a trusted Linux lab, with its own language selector.

## Build the OpenWrt packages

The steps below build the gateway and LuCI packages for **Routerich AX3000**, target `mediatek/filogic`, architecture `aarch64_cortex-a53`. Choose the release matching the router's **Status → Overview**. 24.10.8 produces IPK packages. 25.12.6 produces signed APK packages and the public key. [24.10.8 SDK](https://downloads.openwrt.org/releases/24.10.8/targets/mediatek/filogic/) · [25.12.6 SDK](https://downloads.openwrt.org/releases/25.12.6/targets/mediatek/filogic/)

Use an x86-64 Linux build machine or WSL2, a case-sensitive Linux filesystem, Go 1.23 or newer and Python 3.9 or newer. Install the OpenWrt SDK host prerequisites. On Debian/Ubuntu a typical setup is shown below. Run the build commands as a regular user from the repository root.

```sh
sudo apt-get update
sudo apt-get install build-essential clang flex bison g++ gawk gettext git libncurses-dev libssl-dev python3 python3-setuptools rsync unzip zlib1g-dev file curl ca-certificates openssl tar zstd xz-utils perl time
go version
```

**1. Obtain the upstream VPN engine.** The project implements the controller, web interfaces, routing, packaging and test tooling. The original `kittend` engine is distributed as a binary in Sota Connect. The extraction tool verifies the release asset digest, version, architecture and IPC contract, then writes to a new `build/arm64` directory. It does not require a subscription key. [Sota Connect v1.8.11 release](https://github.com/sotavpn/sotavpn-app/releases/tag/v1.8.11)

```sh
python3 tools/candidate.py v1.8.11 '1.8.11+1892' arm64 build/arm64
```

If you already have the official ARM64 Debian package, use the offline command instead of step 1. It checks the pinned package SHA-256.

```sh
python3 tools/extract.py /path/to/sotaconnect_arm64.deb build/arm64 \
  --source https://github.com/sotavpn/sotavpn-app/releases/download/v1.8.11/sotaconnect_arm64.deb \
  --sha256 44ec209c690ed718879486341529dd89e01b3783790b306c2b5cfe7681ecedbd \
  --version '1.8.11+1892' --arch arm64
```

**2. Build the controller and packages.** Choose `24.10.8` or `25.12.6` for `version`. The SDK helper verifies the downloaded archives, builds the matching LuCI translation tool and reuses the SDK on subsequent runs. For APK it creates or reuses a signing key pair in that SDK. Keep the private key there for future updates.

```sh
set -e
version=25.12.6
sdk=$(sh tools/prepare_filogic_sdk.sh "$version")
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags=luci_only -trimpath \
  -ldflags='-s -w' -o build/openwrt-controller-arm64 ./cmd/panel
sh tools/build_openwrt.sh "$sdk" "build/candidate-filogic-$version"
ls "build/candidate-filogic-$version"
```

For an already prepared AArch64 SDK, pass its absolute path to `tools/build_openwrt.sh`. The packaging script checks the engine SHA-256 and the controller build tag. To produce both Routerich ZIPs with install notes and SHA-256 manifests after building both versions, run `pwsh -File tools/bundle-routerich.ps1` (PowerShell 7.2+).

## GitHub releases

Push a Git tag on the commit you want to release, from any branch. The example below tags the current commit.

```sh
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions runs the Go tests, builds both OpenWrt targets and publishes the IPKs, signed APKs, public key, two Routerich install ZIPs and `SHA256SUMS` on the tag's GitHub Release. Package versions come from the two OpenWrt Makefiles. Bump the relevant `PKG_RELEASE` when shipping changed package contents so the router can recognize an upgrade.

For a fork, set the repository Actions secret `SOTA_APK_SIGNING_KEY` to the PEM private key used for APK releases. Keep using the same key for updates. To test a build before tagging, select **Actions → OpenWrt packages → Run workflow**. The manual run produces downloadable build artifacts.

Pull requests and pushes to `master` also run regression tests. Two native ARM64 jobs install the built IPKs/APKs into clean Routerich OpenWrt filesystems. They exercise LuCI and rpcd, real LAN packet classification and CIDR exceptions, domain and wildcard DNS rules, authenticated DoH, tunnel traffic and core failure using local fixtures. Both jobs must pass before tag publication. A pull request uses a temporary APK key. The live subscription and full VM reboot checks are documented in [the lab regression report](openwrt/REGRESSION.md).

## Install and use

Copy both packages for the matching release into a dedicated directory on the router. For 25.12.6 also copy the public `sota-lab.pem`. From that directory run the commands for your release.

| OpenWrt | Install |
| --- | --- |
| 24.10.8 | `opkg update && opkg install ./sota-gateway_*.ipk ./luci-app-sota_*.ipk` |
| 25.12.6 | `cp sota-lab.pem /etc/apk/keys/ && apk update && apk verify ./*.apk && apk add ./*.apk` |

Enable and start the service, reload the firewall and restart rpcd. Open **Services → Sota Gateway**, enter the subscription key, select the client-facing interface (usually `br-lan`), server and protocol, then press **Start** and **Check IP**. Disable software and hardware flow offloading on the router before enabling the policy.

```sh
/etc/init.d/sota enable && /etc/init.d/sota start &&
/etc/init.d/firewall reload && /etc/init.d/rpcd restart
```

## Development and tests

```sh
go test ./cmd/... ./internal/...
go test -tags=luci_only ./cmd/... ./internal/...
# Optional Linux lab binaries, built on Linux for the host architecture
mkdir -p build
go build -o build/panel ./cmd/panel
go build -o build/gateway ./cmd/gateway
```

| Path | Contents |
| --- | --- |
| `cmd/panel/`, `cmd/panel/web/`, `cmd/panel/presets/` | Controller, standalone UI, shared routing editor, translations and built-in lists |
| `cmd/gateway/`, `internal/`, `deploy/linux/` | Linux lab gateway, IPC and profile handling, systemd units |
| `openwrt/` | Package recipes, LuCI views, UCI defaults, rpcd access, procd and nftables rules |
| `tools/`, `research/` | Build and regression tools, upstream compatibility contract and lab reports |

The router release was exercised with an ARM64 OpenWrt VM and a separate LAN client. The final packages were also installed into the official Routerich root filesystems for both releases. Physical Routerich hardware remains untested. The reports and Linux lab notes are in Russian. [Regression report](openwrt/REGRESSION.md) · [Routerich install notes](openwrt/ROUTERICH.md) · [Linux lab](docs/linux-lab.ru.md)
