set -eu
v=$1
base="/var/lib/sota-wrt-lab/routerich-test-$v"
r="$base/root"
test -f "$r/etc/openwrt_release"
umask 022
test -e "$r/dev/null" || mknod -m 666 "$r/dev/null" c 1 3
test -e "$r/dev/urandom" || mknod -m 444 "$r/dev/urandom" c 1 9
test -e "$r/dev/random" || mknod -m 444 "$r/dev/random" c 1 8
cat /etc/resolv.conf > "$r/tmp/resolv.conf"
chroot "$r" /bin/mkdir -p /var/run /tmp/lock /tmp/opkg-lists /etc/sota
case "$v" in
25.*)
 cp "$base/sota-lab.pem" "$r/etc/apk/keys/"
 cp "$base/"*.apk "$r/tmp/"
 chroot "$r" /bin/sh -ec 'apk update; apk verify /tmp/*.apk; apk add --no-scripts /tmp/*.apk; apk list --installed | grep -E "^(sota-gateway|luci-app-sota|smartdns)-"'
 ;;
24.*)
 cp "$base/"*.ipk "$r/tmp/"
 chroot "$r" /bin/sh -ec 'opkg update; opkg --offline-root / install /tmp/*.ipk; opkg list-installed | grep -E "^(sota-gateway|luci-app-sota|smartdns) "'
 ;;
esac
test -x "$r/usr/sbin/smartdns"
test -f "$r/www/luci-static/resources/view/sota/lists.js"
chroot "$r" /usr/libexec/sota/controller -install-route-defaults
chroot "$r" /bin/sh -ec 'test "$(uci -q get sota.main.enabled)" = 0; test "$(uci -q get sota.main.local_rpc || echo 0)" = 0; test -z "$(uci -q get sota.main.web)"'
if chroot "$r" /usr/bin/env SOTA_API_ENABLED=true SOTA_WEB_ENABLED=true /usr/libexec/sota/controller 2>"$base/web-start.log"; then exit 1; fi
grep -q 'requires -router' "$base/web-start.log"
sha256sum "$r/usr/libexec/sota/controller" "$r/usr/libexec/sota/kittend"
echo "$v clean native package installation and defaults passed"

