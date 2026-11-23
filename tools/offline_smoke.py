"""Run kittend's local control protocol in an empty, offline ARM/Linux jail."""

import argparse
import ctypes
from contextlib import contextmanager, nullcontext
import hashlib
import json
import os
from pathlib import Path
import resource
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time


def confine(root, tun):
    # Run only in a single-threaded supervisor. No host descriptors are inherited.
    os.chroot(root)
    os.chdir("/")
    libc = ctypes.CDLL(None, use_errno=True)
    prctl = libc.prctl
    if prctl(38, 1, 0, 0, 0) != 0:  # PR_SET_NO_NEW_PRIVS
        raise OSError(ctypes.get_errno(), "PR_SET_NO_NEW_PRIVS")
    if tun and prctl(8, 1, 0, 0, 0):  # PR_SET_KEEPCAPS across setuid
        raise OSError(ctypes.get_errno(), "keepcaps")
    os.setgroups([])
    os.setgid(65534)
    os.setuid(65534)
    if tun:
        header = (ctypes.c_uint32 * 2)(0x20080522, 0)
        mask = (1 << 12) | (1 << 13)  # CAP_NET_ADMIN and CAP_NET_RAW in offline netns
        data = (ctypes.c_uint32 * 6)(mask, mask, mask, 0, 0, 0)
        if libc.capset(header, data) or any(prctl(47, 2, cap, 0, 0) for cap in (12, 13)):
            raise OSError(ctypes.get_errno(), "netns capability")
    os.umask(0o077)
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_FSIZE, (8 << 20, 8 << 20))
    resource.setrlimit(resource.RLIMIT_NOFILE, (256, 256))


@contextmanager
def empty_root():
    with tempfile.TemporaryDirectory(prefix="sota-offline-") as temp:
        root = Path(temp)
        procfs = root / "proc"
        procfs.mkdir()
        subprocess.run(["mount", "-t", "proc", "-o", "ro,nosuid,nodev,noexec,hidepid=2",
                        "proc", str(procfs)], check=True)
        try:
            yield root
        finally:
            subprocess.run(["umount", str(procfs)], check=True)


def probe(core, expected, client_pid, crash):
    with empty_root() as root:
        shutil.copyfile(core, root / "kittend")
        actual = hashlib.sha256((root / "kittend").read_bytes()).hexdigest()
        if actual != expected:
            raise ValueError("sandbox binary digest mismatch")
        (root / "kittend").chmod(0o555)
        root.chmod(0o755)
        state = root / "state"
        state.mkdir(mode=0o700)
        os.chown(state, 65534, 65534)
        if client_pid:
            (root / "dev/net").mkdir(parents=True)
            os.mknod(root / "dev/net/tun", stat.S_IFCHR | 0o666, os.makedev(10, 200))
            (root / "dev/net/tun").chmod(0o666)
            (root / "etc").mkdir()
            (root / "etc/resolv.conf").write_text("nameserver 198.18.0.2\n")
            (root / "tmp").mkdir()
            (root / "tmp").chmod(0o1777)
        options = dict(preexec_fn=lambda: confine(root, client_pid is not None), env={"HOME": "/state", "PATH": "/bin",
                       "GOMAXPROCS": "2"}, stdin=subprocess.DEVNULL, close_fds=True)
        help_run = subprocess.run(["/kittend", "--help"], capture_output=True,
                                  timeout=6, **options)
        print((help_run.stdout + help_run.stderr).decode(errors="replace")[:4000], flush=True)
        sock = state / "control.sock"
        with tempfile.TemporaryFile() as log, socket.socket() as origin:
            origin.bind(("127.0.0.1", 0))
            if not client_pid:
                origin.listen(1)
            origin.settimeout(4)
            proc = subprocess.Popen(["/kittend", "--state-dir", "/state", "--endpoint",
                                     "/state/control.sock"], stdout=log, stderr=log, **options)
            try:
                deadline = time.monotonic() + 6
                while not sock.is_socket():
                    if proc.poll() is not None or time.monotonic() > deadline:
                        log.seek(0)
                        # This harness takes only an empty, offline state directory.
                        raise RuntimeError("startup: " + log.read(1500).decode(errors="replace"))
                    time.sleep(.05)
                with socket.socket(socket.AF_UNIX) as client:
                    client.settimeout(3)
                    client.connect(str(sock))
                    with client.makefile("rb") as reader:
                        methods = ("hello", "status", "start", "status")
                        if not crash:
                            methods += ("stop", "status")
                        for seq, method in enumerate(methods, 1):
                            params = {"protocolVersion": 1} if method == "hello" else {}
                            if method == "start":
                                opts = {"inbound_mode": "proxy"}
                                if client_pid:
                                    opts = {"inbound_mode": "tun", "interface_name": "sotatest0",
                                            "ipv6": False, "bypass_lan": False,
                                            "dns_servers": ["198.18.0.2"], "network_stack": "gvisor"}
                                params = {"configuration": json.dumps({"outbounds": [
                                    {"type": "direct", "tag": "proxy"}]}), "localOptionsJson": json.dumps(opts)}
                            request = {"id": seq, "method": method, "params": params}
                            client.sendall(json.dumps(request).encode() + b"\n")
                            for _ in range(32):
                                line = reader.readline(65537)
                                if not line.endswith(b"\n") or len(line) > 65536:
                                    raise ValueError("incomplete or oversized IPC frame")
                                reply = json.loads(line)
                                if reply.get("id") == seq:
                                    print(method, json.dumps(reply), flush=True)
                                    if reply.get("error"):
                                        raise ValueError("IPC method failed: " + method)
                                    result = reply["result"]
                                    if method == "hello" and result["protocolVersion"] != 1:
                                        raise ValueError("unsupported protocol")
                                    if seq in (2, 6) and result["state"] != 0:
                                        raise ValueError("expected idle")
                                    if seq == 4 and client_pid:
                                        import transit_fixture as fixture
                                        fixture.run("ip", "route", "add", "default", "dev", "sotatest0",
                                                    "table", "109", "metric", "10")
                                        fixture.run(*fixture.net(client_pid, sys.executable,
                                                    fixture.__file__, "up"))
                                    elif seq == 4:
                                        offline_tcp(result["proxy_port"], origin)
                                    break
                            else:
                                raise ValueError("no matching IPC response")
                        if crash:
                            proc.kill()
                            proc.wait(3)
            except Exception:
                if client_pid:
                    for cmd in (["ip", "rule"], ["ip", "route", "show", "table", "all"],
                                ["ip", "-s", "link"], ["nft", "list", "ruleset"]):
                        subprocess.run(cmd)
                    log.seek(0)
                    print("offline fixture log", log.read(5000).decode(errors="replace"), flush=True)
                raise
            finally:
                if proc.poll() is None:
                    proc.send_signal(signal.SIGTERM)
                    try:
                        proc.wait(4)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
            print("core_sha256", actual, "exit", proc.returncode, flush=True)
            allowed = (-signal.SIGKILL,) if crash else (0, -signal.SIGTERM)
            if proc.returncode not in allowed:
                raise RuntimeError("unclean shutdown")
        if client_pid:
            fixture.run(*fixture.net(client_pid, sys.executable, fixture.__file__, "down"))


def offline_tcp(proxy_port, origin):
    """A separate client sends a nonce via SOCKS5 to a loopback-only echo origin."""
    nonce = os.urandom(24)
    def echo():
        with origin.accept()[0] as peer:
            peer.settimeout(3)
            with peer.makefile("rb") as reader:
                if reader.read(len(nonce)) == nonce:
                    peer.sendall(nonce[::-1])
    worker = threading.Thread(target=echo, daemon=True)
    worker.start()
    with socket.create_connection(("127.0.0.1", proxy_port), timeout=3) as proxy:
        with proxy.makefile("rb") as r:
            proxy.sendall(b"\x05\x01\x00")
            if r.read(2) != b"\x05\x00":
                raise ValueError("SOCKS5 negotiation failed")
            proxy.sendall(b"\x05\x01\x00\x01\x7f\x00\x00\x01" +
                          origin.getsockname()[1].to_bytes(2, "big"))
            if r.read(3) != b"\x05\x00\x00":
                raise ValueError("SOCKS5 connect failed")
            atyp = r.read(1)
            sizes = {b"\x01": 4, b"\x04": 16}
            r.read(sizes[atyp] + 2)
            proxy.sendall(nonce)
            if r.read(len(nonce)) != nonce[::-1]:
                raise ValueError("data plane echo mismatch")
    worker.join(4)
    print("offline SOCKS5 TCP echo OK", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path)
    parser.add_argument("sha256")
    parser.add_argument("--tun", action="store_true")
    parser.add_argument("--crash", action="store_true", help="kill an active test tunnel")
    args = parser.parse_args()
    if args.crash and not args.tun:
        parser.error("--crash requires --tun")
    if os.geteuid() != 0:
        parser.error("namespace and chroot setup needs root")
    outer = os.environ.get("SOTA_SMOKE_OUTER_NETNS")
    current = os.readlink("/proc/self/ns/net")
    if outer is None:
        os.environ["SOTA_SMOKE_OUTER_NETNS"] = current
        os.execvp("unshare", ["unshare", "--net", "--mount", "--propagation", "private",
                             "--pid", "--fork", "--kill-child", "--mount-proc", "--",
                             sys.executable, __file__, *sys.argv[1:]])
    if outer == current or os.getpid() != 1:
        raise RuntimeError("network or PID isolation missing")
    devices = [line.split(":")[0].strip() for line in Path("/proc/net/dev").read_text().splitlines()[2:]]
    if devices != ["lo"]:
        raise RuntimeError("network namespace has external interfaces")
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True)
    if args.tun:
        from transit_fixture import topology
    with (topology() if args.tun else nullcontext(None)) as client:
        probe(args.core, args.sha256, client, args.crash)
