#!/bin/sh
# Prepare the two release SDKs used for Routerich AX3000. Print the SDK path on stdout.
set -eu
case "${1:-}" in
    24.10.8)
        gcc=13.3.0
        sdk_hash=d9fd074f281823a3023e8f6a73aaac598018c898ec9ac4d1c507b596930d7482
        luci_rev=cac97ed67cfbcad90db49f5f3b1245c2c4cbfae5
        luci_hash=d0630593cda6b10dc08ac7be1f603464414e98a677dae4858db178a6969d50c3
        ;;
    25.12.6)
        gcc=14.3.0
        sdk_hash=1c04ac47bdfc361d6aab299eb520d570e368013dedcb8d29cf39dc7168ae642e
        luci_rev=75a306df84a020c57a631ef5b4c02ace56d93387
        luci_hash=9365b8ce335bdc5b41ac12155bbb85a4161b8835a992c4cda44ad73b4660e2d5
        ;;
    *) echo 'Usage: sh tools/prepare_filogic_sdk.sh {24.10.8|25.12.6} [SDK cache directory]' >&2; exit 2 ;;
esac
version=$1
dir=${2:-"${XDG_CACHE_HOME:-$HOME/.cache}/sota-gateway"}
mkdir -p "$dir"
cd "$dir"
dir=$(pwd -P)
work=$(mktemp -d "$dir/.prepare-XXXXXX")
trap 'rm -rf "$work"' EXIT
download() {
    if [ ! -f "$2" ]; then
        curl --proto '=https' --proto-redir '=https' --fail --location --retry 2 "$1" -o "$work/download" >&2
        echo "$3  $work/download" | sha256sum -c - >&2
        mv "$work/download" "$2"
    else
        echo "$3  $2" | sha256sum -c - >&2
    fi
}
name="openwrt-sdk-$version-mediatek-filogic_gcc-${gcc}_musl.Linux-x86_64"
sdk="$dir/$name"
if [ ! -f "$sdk/include/package.mk" ]; then
    download "https://downloads.openwrt.org/releases/$version/targets/mediatek/filogic/$name.tar.zst" "$name.tar.zst" "$sdk_hash"
    tar --zstd -xf "$name.tar.zst" -C "$work"
    mv -T "$work/$name" "$sdk"
fi
grep -Fq "^$luci_rev" "$sdk/feeds.conf.default" || { echo 'Unexpected LuCI feed revision' >&2; exit 1; }
if [ ! -x "$sdk/staging_dir/host/bin/po2lmo" ]; then
    archive="luci-$luci_rev.tar.gz"
    download "https://codeload.github.com/openwrt/luci/tar.gz/$luci_rev" "$archive" "$luci_hash"
    src="luci-$luci_rev/modules/luci-base/src"
    tar -xzf "$archive" -C "$work" "$src"
    make -C "$work/$src" po2lmo >&2
    install -m 0755 "$work/$src/po2lmo" "$sdk/staging_dir/host/bin/po2lmo"
fi
# Sota's packages contain prebuilt ARM64 executables and depend on stock runtime packages.
mkdir -p "$sdk/feeds"
[ -e "$sdk/feeds/base" ] || ln -s ../package "$sdk/feeds/base"
if [ "$version" = 25.12.6 ]; then
    if [ ! -f "$sdk/private-key.pem" ]; then
        [ ! -e "$sdk/public-key.pem" ] || { echo 'Restore the matching private APK signing key' >&2; exit 1; }
        (umask 077; openssl genrsa -out "$work/private-key.pem" 4096)
        ln "$work/private-key.pem" "$sdk/private-key.pem"
    fi
    openssl rsa -in "$sdk/private-key.pem" -pubout -out "$work/public-key.pem" 2>/dev/null
    if [ -f "$sdk/public-key.pem" ]; then
        cmp "$work/public-key.pem" "$sdk/public-key.pem" >&2
    else
        install -m 0644 "$work/public-key.pem" "$sdk/public-key.pem"
    fi
fi
printf '%s\n' "$sdk"
