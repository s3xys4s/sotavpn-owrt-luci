#!/bin/sh
# Observe a fresh distribution client in an offline netns using a private DNS stub.
# Requires a directory with app/ (extracted DEB) and deps/ (extracted Ubuntu runtime).
set -eu
ref=$(realpath "$1")
scenario=$2
if [ "${3:-}" != inner ]; then
    exec unshare --mount --propagation private --net --uts --pid --fork --kill-child --mount-proc -- "$0" "$ref" "$scenario" inner
fi
test "$$" -eq 1
test "$(ls /proc/net/dev >/dev/null && awk 'NR>2 {print $1}' /proc/net/dev)" = lo:
ip link set lo up
hostname sota-fixture
jail=$(mktemp -d)
mount -t tmpfs -o mode=755,size=160m tmpfs "$jail"
mkdir -p "$jail"/usr "$jail"/etc "$jail"/dev "$jail"/proc "$jail"/tmp "$jail"/run "$jail"/app "$jail"/home
mount -t overlay -o "ro,lowerdir=$ref/deps/usr:/usr" overlay "$jail/usr"
mount --bind "$ref/app/usr/lib/sota-connect" "$jail/app"
mount -o remount,bind,ro "$jail/app"
for link in bin sbin lib; do ln -s "usr/$link" "$jail/$link"; done
for file in null zero random urandom; do touch "$jail/dev/$file"; mount --bind "/dev/$file" "$jail/dev/$file"; done
mount -t proc -o ro,nodev,nosuid,noexec,hidepid=2 proc "$jail/proc"
printf 'nameserver 127.0.0.1\noptions attempts:1 timeout:1\n' > "$jail/etc/resolv.conf"
printf 'hosts: files dns\n' > "$jail/etc/nsswitch.conf"
printf 'nobody:x:65534:65534:Lab:/home:/bin/false\n' > "$jail/etc/passwd"
printf 'nogroup:x:65534:\n' > "$jail/etc/group"
mkdir -p "$jail/etc/fonts" "$jail/etc/ssl/certs"
mount --bind /etc/fonts "$jail/etc/fonts"; mount -o remount,bind,ro "$jail/etc/fonts"
cp /etc/ssl/certs/ca-certificates.crt "$jail/etc/ssl/certs/"
if [ "$scenario" = machine ]; then printf '0123456789abcdef0123456789abcdef\n' > "$jail/etc/machine-id"; fi
mkdir -p "$jail/var/lib/dbus"
printf '%s\n' 87654321876543218765432187654321 > "$jail/var/lib/dbus/machine-id"
cat > "$jail/etc/os-release" <<'RELEASE'
NAME="OpenWrt"
VERSION="Fixture Version"
ID="openwrt"
VERSION_ID="fixture-version-id"
PRETTY_NAME="OpenWrt Fixture Pretty"
BUILD_ID="r99999-fixture"
RELEASE
case "$scenario" in
 minimal) printf 'ID=minimal\n' > "$jail/etc/os-release";;
 whitespace) printf '   \n' > "$jail/etc/machine-id";;
esac
chmod 1777 "$jail/tmp"
mkdir "$jail/run/user"
chown 65534:65534 "$jail/home" "$jail/run/user"; chmod 700 "$jail/run/user"
# Only syntactically valid DNS names from this fresh, offline instance are emitted.
python3 -u -c '
import socket, struct, re
s=socket.socket(type=socket.SOCK_DGRAM); s.bind(("127.0.0.1",53)); seen=set()
while True:
 p,peer=s.recvfrom(4096)
 i=12; labels=[]
 while i<len(p) and 0<p[i]<64:
  n=p[i]; i+=1; labels.append(p[i:i+n]); i+=n
 name=b".".join(labels).decode("ascii","ignore").lower()
 if re.fullmatch(r"[a-z0-9.-]{3,253}",name) and name not in seen: print(name); seen.add(name)
 if i+5<=len(p): s.sendto(p[:2]+bytes.fromhex("8183 0001 0000 0000 0000")+p[12:i+5],peer)
' &
setpriv --no-new-privs chroot --userspec=65534:65534 "$jail" /usr/bin/env -i \
    HOME=/home PATH=/usr/bin:/bin DISPLAY=:87 XDG_RUNTIME_DIR=/run/user LIBGL_ALWAYS_SOFTWARE=1 \
    LD_LIBRARY_PATH=/app/lib:/usr/lib/aarch64-linux-gnu dbus-run-session -- /bin/sh -c '
    Xvfb :87 -screen 0 1024x768x24 -nolisten tcp >/tmp/display.log 2>&1 &
    sleep 1
    /app/sotavpn >/tmp/reference.log 2>&1 &
    app=$!
    sleep 6
    if kill -0 "$app" 2>/dev/null; then echo reference-process-alive; fi
    xdotool search --onlyvisible --name ".*" getwindowname %@ 2>/dev/null | head -8
    ' </dev/null
python3 - "$jail" <<'PY'
import pathlib,sys,re
root=pathlib.Path(sys.argv[1])
for f in list((root/'home').rglob('*'))+[root/'tmp/reference.log']:
 if f.is_file() and (f.suffix in ('.log','.json','.txt') or 'sota_logs' in str(f)) and f.stat().st_size<4*1024*1024:
  b=f.read_bytes()
  ids=set(re.findall(rb'"(?:flutter\.)?device_id"\s*:\s*"([^"\n]+)"',b))
  hashes=set(re.findall(rb'(?i)(?<![0-9a-f])[0-9a-f]{64}(?![0-9a-f])',b))
  if ids:print('device_id',ids)
  if hashes:print('digest candidates',f.name,sorted(hashes)[:10])
  if f.name=='reference.log':
   for l in b.splitlines():
    s=re.sub(rb'^\[[^]]+\]\s*','',l)
    if any(k in s.lower() for k in [b'header',b'hwid',b'hash',b'pretty',b'linux device',b'hostname as']): print(s[:160].decode('utf-8','replace'))
PY

