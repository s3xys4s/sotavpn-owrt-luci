#!/bin/sh
# Run in Linux with an official AArch64 SDK and the LuCI-only Go controller.
set -eu
project=$(cd "$(dirname "$0")/.." && pwd)
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || { echo 'Usage: sh tools/build_openwrt.sh SDK [output-directory]' >&2; exit 2; }
sdk=$(realpath "$1")
outdir=${2:-build/openwrt}
test -f "$sdk/include/package.mk"
test -x "$sdk/staging_dir/host/bin/po2lmo" || { echo 'Prepare the SDK host tool po2lmo first' >&2; exit 1; }
cd "$project"
echo 'bde34f88ab454f2f8d7ee2c24dba2eff94da4371969d05f5c0b45faf3f824af8  build/arm64/kittend' | sha256sum -c -
controller=build/openwrt-controller-arm64
grep -aqF -- '-tags=luci_only' "$controller" || { echo 'Build the controller with -tags=luci_only' >&2; exit 1; }
readelf -h "$controller" | grep -q 'Machine:.*AArch64'
for package in sota-gateway luci-app-sota; do
	dest="$sdk/package/$package"
	mkdir -p "$dest"
	cp -R "openwrt/$package/." "$dest/"
done
cp cmd/panel/web/routing.js cmd/panel/web/i18n.js cmd/panel/web/messages.json "$sdk/package/luci-app-sota/"
mkdir -p "$sdk/package/sota-gateway/artifacts"
install -m 0644 build/arm64/kittend "$sdk/package/sota-gateway/artifacts/kittend"
install -m 0644 "$controller" "$sdk/package/sota-gateway/artifacts/panel"
test -f "$sdk/.config" || make -C "$sdk" defconfig
sed -i '/CONFIG_PACKAGE_sota-gateway[ =]/d;/CONFIG_PACKAGE_luci-app-sota[ =]/d' "$sdk/.config"
printf '%s\n' 'CONFIG_PACKAGE_sota-gateway=m' 'CONFIG_PACKAGE_luci-app-sota=m' >> "$sdk/.config"
make -C "$sdk" defconfig
make -C "$sdk" NO_DEPS=1 package/sota-gateway/clean package/luci-app-sota/clean
make -C "$sdk" NO_DEPS=1 package/sota-gateway/compile package/luci-app-sota/compile V=s
mkdir -p "$outdir"
if ! grep -q '^CONFIG_USE_APK=y' "$sdk/.config"; then
	for package in sota-gateway luci-app-sota; do
		version=$(sed -n 's/^PKG_VERSION:=//p' "openwrt/$package/Makefile")
		release=$(sed -n 's/^PKG_RELEASE:=//p' "openwrt/$package/Makefile")
		f=$(find "$sdk/bin" -name "${package}_${version}-r${release}_*.ipk" -print -quit)
		test -n "$f" || { echo "SDK failed to produce $package" >&2; exit 1; }
		cp "$f" "$outdir/"
	done
	exit 0
fi
signed=$(mktemp -d)
mkdir "$signed/keys"
cp "$sdk/public-key.pem" "$signed/keys/sota-lab.pem"
apk="$sdk/staging_dir/host/bin/apk"
for package in sota-gateway luci-app-sota; do
	version=$(sed -n 's/^PKG_VERSION:=//p' "openwrt/$package/Makefile")
	release=$(sed -n 's/^PKG_RELEASE:=//p' "openwrt/$package/Makefile")
	f=$(find "$sdk/bin" -name "$package-$version-r$release.apk" -print -quit)
	test -n "$f" || { echo "SDK failed to produce $package" >&2; exit 1; }
	out="$signed/$(basename "$f")"
	cp "$f" "$out"
	"$apk" adbsign --allow-untrusted --reset-signatures --sign-key "$sdk/private-key.pem" "$out"
	"$apk" verify --keys-dir "$signed/keys" "$out"
	cp "$out" "$outdir/"
	rm "$out"
done
cp "$sdk/public-key.pem" "$outdir/sota-lab.pem"
rm "$signed/keys/sota-lab.pem"
rmdir "$signed/keys" "$signed"
