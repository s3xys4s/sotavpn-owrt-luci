#!/bin/sh
# Observe a fresh distribution client in an offline netns using a private DNS stub.
# Requires a directory with app/ (extracted DEB) and deps/ (extracted Ubuntu runtime).
set -eu
ref=$(realpath "$1")
if [ "${2:-}" != inner ]; then
    exec unshare --mount --propagation private --net --pid --fork --kill-child --mount-proc -- "$0" "$ref" inner
fi
test "$$" -eq 1
test "$(ls /proc/net/dev >/dev/null && awk 'NR>2 {print $1}' /proc/net/dev)" = lo:
ip link set lo up
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
tr -d '-' </proc/sys/kernel/random/uuid > "$jail/etc/machine-id"
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
    sleep 7
    if kill -0 "$app" 2>/dev/null; then echo reference-process-alive; fi
    xdotool search --onlyvisible --name ".*" getwindowname %@ 2>/dev/null | head -8
    sleep 8
    if ! kill -0 "$app" 2>/dev/null; then sed -n "1,5p" /tmp/display.log; sed -n "1,8p" /tmp/reference.log; fi
    ' </dev/null
