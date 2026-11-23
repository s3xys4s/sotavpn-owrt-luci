set -eu
r=$1
mount --make-rprivate /
mount -t proc proc "$r/proc"
ip link set lo up
chroot "$r" /bin/sh -ec '
umask 077
mkdir -p /var/run/ubus /var/run/sota/core /etc/sota
/sbin/ubusd >/tmp/reg-ubus.log 2>&1 &
i=0; until ubus list >/dev/null 2>&1; do i=$((i+1)); test "$i" -lt 20; sleep 1; done
/sbin/rpcd >/tmp/reg-rpcd.log 2>&1 &
/usr/libexec/sota/kittend --endpoint /var/run/sota/core.sock --state-dir /var/run/sota/core >/tmp/reg-core.log 2>&1 &
/usr/libexec/sota/controller -router -state /etc/sota >/tmp/reg-control.log 2>&1 &
i=0; until [ "$(ubus call sota status 2>/dev/null | jsonfilter -e "@.data.core.core_responding")" = true ]; do i=$((i+1)); test "$i" -lt 25; sleep 1; done
ubus call sota network | jsonfilter -e "@.data.catalog[0].name" | grep -q YouTube
ubus call sota network | jsonfilter -e "@.data.exceptions.files[0].name" | grep -q "Sota Connect"
test -S /var/run/sota/rpc.sock
test -z "$(netstat -lnt | grep -E ":(8080|18082) " || :)"
echo "PASS default LuCI rpcd, core, preset installation, Unix RPC and closed web ports"
'

