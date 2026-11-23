"""Run a provider profile in a throwaway OCI gateway with a private slirp WAN."""

import argparse
from contextlib import ExitStack
import ipaddress
import json
import os
from pathlib import Path
import select
import stat
import subprocess
import sys
import tempfile
import time

from oci_smoke import unpack
from lab_mounts import isolate
from transit_fixture import FORWARD_FILTER, net


def command(*args, data=None, timeout=20):
    p = subprocess.run(args, input=data, capture_output=True, timeout=timeout)
    if p.returncode:
        # Native errors, replies and diagnostics can contain sensitive profiles.
        raise RuntimeError(f"{Path(args[0]).name} failed (exit {p.returncode})")
    return p.stdout


def private_peer(proc):
    for _ in range(80):
        if proc.poll() is not None:
            raise RuntimeError("client namespace failed")
        if not os.path.samefile(f"/proc/{proc.pid}/ns/net", "/proc/self/ns/net"):
            return proc.pid
        time.sleep(.025)
    raise RuntimeError("peer still shares the host network")


def add_process(stack, args, **kwargs):
    proc = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL, **kwargs)
    def stop():
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(4)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
    stack.callback(stop)
    return proc


def run(args, temp, state, stack, panel=None):
    cfg = unpack(args.layout, temp / "rootfs")
    # Only the temporary secret copy is mounted. Existing user data is read-only.
    profile = state / "provider.json"
    if args.profile:
        with os.fdopen(os.open(args.profile, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), "rb") as f:
            st = os.fstat(f.fileno())
            if not stat.S_ISREG(st.st_mode) or st.st_mode & 0o077 or st.st_size > 1 << 20:
                raise ValueError("profile must be a private regular file, at most 1 MiB")
            profile.write_bytes(f.read((1 << 20) + 1))
    elif panel is None:
        # Documentation address. No provider login or credentials in preflight.
        profile.write_text(json.dumps({"outbounds": [{"type": "trojan", "tag": "proxy",
            "server": "192.0.2.250", "server_port": 443, "password": "synthetic-offline-fixture",
            "tls": {"enabled": True, "server_name": "test.invalid"}}]}))
    if panel is None:
        profile.chmod(0o400)
        os.chown(profile, 65534, 65534)
    os.chown(state, 65534, 65534)
    resolv = temp / "rootfs/etc/resolv.conf"
    resolv.write_text("nameserver 10.0.2.3\n")
    resolv.chmod(0o644)
    caps = ["CAP_NET_ADMIN", "CAP_NET_RAW"]
    spec = {"ociVersion": "1.0.2", "root": {"path": str(temp / "rootfs"), "readonly": True},
            "process": {"terminal": False, "user": {"uid": 65534, "gid": 65534}, "cwd": "/",
                        "args": cfg["config"]["Entrypoint"] + cfg["config"]["Cmd"],
                        "env": cfg["config"]["Env"] + ([] if panel else ["SOTA_PROFILE_FILE=/var/lib/sota-gateway/provider.json"]),
                        "noNewPrivileges": True,
                        "capabilities": {k: caps for k in ("bounding","effective","inheritable","permitted","ambient")}},
            "mounts": [
                {"destination": "/proc", "type":"proc", "source":"proc","options":["ro","nosuid","nodev","noexec"]},
                {"destination": "/dev", "type":"tmpfs", "source":"tmpfs","options":["nosuid","mode=755"]},
                # runc otherwise creates /dev/net with the service's 0077 umask.
                {"destination": "/dev/net", "type":"tmpfs", "source":"tmpfs","options":["nosuid","noexec","mode=755"]},
                {"destination": "/tmp", "type":"tmpfs", "source":"tmpfs","options":["nosuid","nodev","mode=1777"]},
                {"destination": "/var/lib/sota-gateway", "type":"bind", "source":str(state),
                 "options":["rbind","rw","nosuid","nodev","noexec"]}],
            "linux": {"namespaces":[{"type": t} for t in ("pid","network","mount","ipc","uts")],
                      "devices":[{"path":"/dev/net/tun","type":"c","major":10,"minor":200,"fileMode":438,
                                  "uid":65534,"gid":65534}],
                      "resources":{"devices":[{"allow":False,"access":"rwm"},
                                              {"allow":True,"type":"c","major":10,"minor":200,"access":"rw"}]}}}
    (temp / "config.json").write_text(json.dumps(spec))
    runc = ["runc", "--root", str(temp / "runc")]
    name = temp.name
    stack.callback(lambda: subprocess.run([*runc, "delete", "--force", name], capture_output=True, timeout=10))
    with tempfile.TemporaryFile() as preflight_log:
        run_rc = subprocess.run([*runc,"run","-d","--bundle",str(temp),name], stdin=subprocess.DEVNULL,
                                stdout=subprocess.DEVNULL,
                                stderr=preflight_log if args.preflight else subprocess.DEVNULL,
                                timeout=15).returncode
        if run_rc:
            if args.preflight:
                preflight_log.seek(0)
                print(preflight_log.read(1500).decode(errors="replace"), flush=True)
            raise RuntimeError("OCI start failed")
    pid = json.loads(command(*runc,"state",name))["pid"]
    if os.path.samefile(f"/proc/{pid}/ns/net","/proc/self/ns/net"):
        raise RuntimeError("gateway unexpectedly shares host netns")
    def gateway(*cmd, **kw):
        return command(*net(pid, *cmd), **kw)
    def ctl(method):
        p = subprocess.run([*runc,"exec",name,"/usr/bin/gateway",method], capture_output=True, timeout=32)
        if p.returncode:
            if args.preflight:
                print("synthetic controller diagnostic", p.stdout[:1200].decode(errors="replace"),
                      p.stderr[:500].decode(errors="replace"), flush=True)
            # gateway emits only fixed controller errors. Do not pass on daemon replies.
            status = json.loads(p.stdout or b'{}')
            reasons = {"tested transit firewall unavailable", "transit firewall differs from tested policy",
                       "core rejected operation", "core IPC failed", "core unavailable"}
            raise ValueError(status.get("problem") if status.get("problem") in reasons else "controller operation failed")
        return json.loads(p.stdout)
    for _ in range(40):
        try:
            if ctl("status")["core_responding"]:
                break
        except ValueError:
            pass
        time.sleep(.1)
    else:
        raise RuntimeError("core readiness timeout")
    rr, wr = os.pipe()
    try:
        add_process(stack, ["slirp4netns","-c","--disable-host-loopback","--enable-sandbox","--enable-seccomp",
                            "--ready-fd",str(wr),str(pid),"wan"], pass_fds=(wr,))
        os.close(wr)
        wr = -1
        if not select.select([rr],[],[],8)[0] or not os.read(rr,1):
            raise RuntimeError("isolated userspace WAN did not start")
    finally:
        os.close(rr)
        if wr != -1:
            os.close(wr)
    client = private_peer(add_process(stack, ["unshare","--net","--","sleep","infinity" if panel else "180"]))
    gateway("ip","link","add","lan","type","veth","peer","name","eth0","netns",str(client))
    gateway("ip","addr","add","192.0.2.1/30","dev","lan")
    gateway("ip","link","set","lan","up")
    command(*net(client,"ip","addr","add","192.0.2.2/30","dev","eth0"))
    command(*net(client,"ip","link","set","eth0","up"))
    command(*net(client,"ip","route","add","default","via","192.0.2.1"))
    gateway("nft","-f","-",data=FORWARD_FILTER + b"""
table inet wan_guard {
    chain output {
        type filter hook output priority -10; policy accept;
        oifname "wan" ip daddr 10.0.2.3 udp dport 53 accept
        oifname "wan" ip daddr 10.0.2.3 tcp dport 53 accept
        oifname "wan" ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/4 } drop
    }
}
""")
    gateway("ip","rule","add","pref","90","iif","lan","table","109")
    gateway("ip","rule","add","pref","91","to","192.0.2.0/30","lookup","main")
    gateway("ip","rule","add","pref","92","uidrange","65534-65534","lookup","main")
    gateway("ip","route","add","blackhole","default","metric","32000","table","109")
    gateway("sysctl","-qw","net.ipv4.ip_forward=1")
    if panel is not None:
        panel(pid, client, ctl, gateway, state)
        return
    direct = None
    probe = Path(__file__).with_name("egress_probe.py").resolve()
    if args.profile or args.check_wan:
        # The host test interpreter inside the gateway netns uses a numeric
        # HTTPS endpoint. It never receives the profile.
        direct = command(*net(pid, sys.executable, str(probe), "--tcp-only")).strip().decode()
        print("direct WAN TCP/TLS baseline obtained",flush=True)
        if args.preflight:
            check = json.loads(command(*net(pid, sys.executable, str(probe)),timeout=25))
            if not all(ipaddress.ip_address(check[k]).version == 4 for k in ("tcp", "udp")):
                raise ValueError("incomplete WAN probe")
            print("external UDP/STUN and DNS probe reachable from test WAN",flush=True)
    print("isolated WAN and client ready",flush=True)
    if args.preflight:
        print(gateway("nft","-nn","list","chain","inet","sota_test","forward").decode(), flush=True)
    ctl("connect")
    gateway("ip","route","add","default","dev","sotatest0","metric","10","table","109")
    if args.preflight:
        print("synthetic profile accepted by OCI controller; TUN present",flush=True)
    else:
        results = json.loads(command(*net(client,sys.executable,str(probe)),timeout=25))
        expected = ipaddress.ip_network(args.expect_egress) if args.expect_egress else None
        if any(x == direct or (expected and ipaddress.ip_address(x) not in expected)
               for x in (results["tcp"],results["udp"])):
            raise ValueError("exit does not match expected provider range or matches direct WAN")
        print("expected provider egress verified with TCP/TLS, UDP/STUN and DNS" if expected
              else "TCP/UDP exit differs from direct WAN; provider exit attribution unverified",flush=True)
    ctl("disconnect")
    route = gateway("ip","route","show","table","109").decode()
    if "blackhole default" not in route or "dev sotatest0" in route:
        raise RuntimeError("unexpected post-stop routing")
    print("post-stop blackhole present",flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("layout", type=Path)
    mode = p.add_mutually_exclusive_group(required=True)
    mode.add_argument("--preflight",action="store_true")
    mode.add_argument("--profile",type=Path)
    p.add_argument("--expect-egress",help="independently known IPv4 address or prefix for test server")
    p.add_argument("--check-wan",action="store_true",help="also check the external TLS baseline in preflight")
    args = p.parse_args()
    if args.expect_egress:
        ipaddress.IPv4Network(args.expect_egress)
    if os.geteuid() != 0:
        p.error("isolated namespace setup requires root")
    isolate()
    with tempfile.TemporaryDirectory(prefix="sota-live-") as work, \
         tempfile.TemporaryDirectory(prefix="sota-secret-", dir="/dev/shm") as secret, ExitStack() as stack:
        try:
            run(args, Path(work), Path(secret), stack)
            if args.profile and not args.expect_egress:
                raise SystemExit(77)
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
            raise SystemExit("live acceptance stopped: " + str(error))
