set -eu
r=$1
sh "$(dirname "$0")/check-routerich-rpc.sh" "$r"
chroot "$r" /sbin/procd -S >"$r/tmp/reg-procd.log" 2>&1 &
i=0; until chroot "$r" /bin/ubus call system board >/dev/null 2>&1; do i=$((i+1)); test "$i" -lt 10; sleep 1; done
chroot "$r" /usr/sbin/uhttpd -f -h /www -p 127.0.0.1:8089 -x /cgi-bin -u /ubus >"$r/tmp/reg-uhttpd.log" 2>&1 &
origin=http://127.0.0.1:8089
jar="$r/tmp/reg-cookies"
page="$r/tmp/reg-page"
sleep 1
curl --noproxy '*' -fsSL -c "$jar" -b "$jar" -d 'luci_username=root&luci_password=' "$origin/cgi-bin/luci/admin/services/sota/status" > "$page"
for view in status lists exceptions network; do
  code=$(curl --noproxy '*' -sSL -b "$jar" -o "$page" -w '%{http_code}' "$origin/cgi-bin/luci/admin/services/sota/$view")
  test "$code" = 200
  grep -q "sota/$view" "$page"
  curl --noproxy '*' -fsS "$origin/luci-static/resources/view/sota/$view.js" >/dev/null
done
echo 'PASS stock uhttpd served four authenticated LuCI routes and JavaScript views'
