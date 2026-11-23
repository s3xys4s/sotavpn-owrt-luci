#!/bin/sh
set -eu
release=$(realpath "$1")
addr=$2
case "${3:-}" in
    "") set -- sota-lab-runtime sota-lab-panel ;;
    --panel-only) set -- sota-lab-panel ;;
    *) exit 2 ;;
esac
case "$release" in /opt/sota-lab/releases/*) ;; *) exit 2;; esac
python3 -c 'import ipaddress,sys; assert ipaddress.IPv4Address(sys.argv[1]).is_private' "$addr"
test -x "$release/panel"
test -r "$release/image/index.json"
test -z "$(find "$release" -xdev ! -user root -print -quit)"
find "$release" -xdev -type d -exec chmod go-w {} +
find "$release" -xdev -type f -exec chmod go-w {} +
if ! id sota-panel >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /var/lib/sota-panel --no-create-home --shell /usr/sbin/nologin sota-panel
fi
test "$(id -u sota-panel)" -ne 0
install -d -m 0750 -o root -g sota-panel /etc/sota-panel
umask 077
if ! test -e /etc/sota-panel/service.env; then
    printf 'SOTA_API_ENABLED=true\nSOTA_WEB_ENABLED=true\nSOTA_HTTP_LISTEN=%s:8080\n' "$addr" > /etc/sota-panel/service.env
elif grep -Fxq "SOTA_HTTP_LISTEN=$addr:8443" /etc/sota-panel/service.env; then
    sed -i "/^SOTA_HTTP_LISTEN=/s/:8443$/:8080/" /etc/sota-panel/service.env
fi
ln -sfnT "$release" /opt/sota-lab/current
for unit do
    install -m 0644 "$release/deploy/linux/$unit.service" "/etc/systemd/system/$unit.service"
done
systemctl daemon-reload
systemctl enable "$@"
systemctl restart "$@"
systemctl is-active "$@"
rm -f -- /etc/sota-panel/admin-password /etc/sota-panel/panel.key /etc/sota-panel/panel.crt
