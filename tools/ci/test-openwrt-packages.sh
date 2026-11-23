#!/bin/bash
# Run as root on a disposable ARM64 Linux runner. The chroot gets its own files and processes.
set -euo pipefail
version=$1
artifacts=$(realpath "$2")
case "$version" in
    24.10.8) sha=b14965862f260290f28fecb38458f42f4d5a79cf54cb80767969adae32d6abfa; ext=ipk ;;
    25.12.6) sha=b766d9a306554de050bd426ae76b5106b99113cf0f540ead30ae0f9d51678828; ext=apk ;;
    *) exit 2 ;;
esac
[[ $(id -u) == 0 && $(uname -m) == aarch64 ]]
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
root=$work/root
zip=("$artifacts"/*-"$version".zip)
[[ ${#zip[@]} == 1 && -f ${zip[0]} ]]
(cd "$artifacts" && sha256sum -c SHA256SUMS)
unzip -q "${zip[0]}" -d "$work/bundle"
(cd "$work/bundle" && sha256sum -c SHA256SUMS)
packages=("$work/bundle"/*."$ext")
[[ ${#packages[@]} == 2 ]]
for package in "${packages[@]}"; do cmp "$package" "$artifacts/$(basename "$package")"; done

image="openwrt-$version-mediatek-filogic-routerich_ax3000-squashfs-sysupgrade.bin"
curl -fsSL --retry 3 "https://downloads.openwrt.org/releases/$version/targets/mediatek/filogic/$image" -o "$work/image"
echo "$sha  $work/image" | sha256sum -c -
member=$(tar -tf "$work/image" | grep -E '^sysupgrade-[^/]+/root$')
tar -xOf "$work/image" "$member" > "$work/root.squashfs"
unsquashfs -no-progress -d "$root" "$work/root.squashfs" >/dev/null
mkdir -p "$root/tmp/run" "$root/tmp/lock" "$root/etc/sota"
for dev in 'null 3' 'random 8' 'urandom 9'; do
    read -r name minor <<< "$dev"
    [[ -e "$root/dev/$name" ]] || mknod -m 666 "$root/dev/$name" c 1 "$minor"
done
cat /etc/resolv.conf > "$root/tmp/resolv.conf"
cp "${packages[@]}" "$root/tmp/"
if [[ $ext == apk ]]; then
    cmp "$artifacts/sota-lab.pem" "$work/bundle/sota-lab.pem"
    cp "$work/bundle/sota-lab.pem" "$root/etc/apk/keys/"
fi

unshare --mount --pid --fork --kill-child --mount-proc \
    setpriv --bounding-set=-sys_module,-sys_admin,-net_admin,-sys_boot chroot "$root" /bin/sh -ec '
    if command -v opkg >/dev/null; then
        opkg update && opkg install /tmp/*.ipk
    else
        apk update && apk verify /tmp/*.apk && apk add /tmp/*.apk
    fi
    /usr/libexec/sota/controller -install-route-defaults
    test "$(uci -q get sota.main.enabled)" = 0
    test -z "$(uci -q get sota.main.local_rpc || :)"
    if SOTA_WEB_ENABLED=true SOTA_API_ENABLED=true /usr/libexec/sota/controller 2>/tmp/controller.log; then exit 1; fi
    grep -q "requires -router" /tmp/controller.log'

isolated=(unshare --net --mount --propagation private --pid --fork --kill-child --mount-proc)
"${isolated[@]}" sh tools/lab/check-routerich-http.sh "$root"
"${isolated[@]}" python3 tools/openwrt_dns_regression.py "$root"
"${isolated[@]}" python3 tools/openwrt_doh_regression.py "$root"
"${isolated[@]}" bash tools/ci/test-openwrt-policy.sh "$root"
core="$root/usr/libexec/sota/kittend"
hash=bde34f88ab454f2f8d7ee2c24dba2eff94da4371969d05f5c0b45faf3f824af8
python3 tools/offline_smoke.py "$core" "$hash" --tun
python3 tools/offline_smoke.py "$core" "$hash" --tun --crash
