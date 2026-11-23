"""Restricted local control for the Linux lab. No network-facing listener."""
import argparse
from contextlib import ExitStack
from datetime import datetime, timezone
import ipaddress
import json
import os
from pathlib import Path
import pwd
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time

from lab_mounts import isolate
from live_accept import command, run
from transit_fixture import net


class TunnelError(Exception):
    """A fixed diagnostic code that may be sent to the web process."""


def panel(pid, client, ctl, gateway, state):
    path = Path("/run/sota-lab/control.sock")
    probe = str(Path(__file__).with_name("egress_probe.py").resolve())
    allowed_uid = pwd.getpwnam("sota-panel").pw_uid
    def wait_released():
        # An idle kittend can keep the previous TUN open during async teardown.
        end = time.monotonic() + 10
        while time.monotonic() < end:
            if not any(link.get("ifname") == "sotatest0" for link in json.loads(gateway("ip", "-j", "link", "show"))):
                return
            time.sleep(.12)
        raise TunnelError("tun_busy")

    def iface(*args):
        try:
            return json.loads(gateway("ip", "-j", "route", "get", "1.1.1.1", *args))[0].get("dev")
        except (RuntimeError, ValueError, IndexError):
            return None
    def measure(target, direct):
        entry = {"ip": None, "status": "unavailable"}
        user = ["setpriv","--reuid=65534","--regid=65534","--clear-groups"] if direct else []
        # Use an explicit script path accessible to the unprivileged WAN probe.
        try:
            result = command(*net(target,*user,sys.executable,probe,"--tcp-only"),timeout=10)
            ip = ipaddress.IPv4Address(result.decode().strip())
            if ip.is_global:
                entry.update(ip=str(ip), status="ok")
        except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
            pass
        return entry
    def handle(req):
        op = req.get("op")
        if op == "status":
            return ctl("status")
        if op == "connect":
            profile = req.get("profile")
            if not isinstance(profile,dict) or set(profile) != {"outbounds"}:
                raise ValueError("invalid profile")
            wait_released()
            with tempfile.NamedTemporaryFile(dir=state,delete=False) as candidate:
                try:
                    candidate.write(json.dumps(profile,separators=(",",":")).encode())
                    candidate.flush()
                    os.fchmod(candidate.fileno(),0o400)
                    os.fchown(candidate.fileno(),65534,65534)
                    os.replace(candidate.name,state / "provider.json")
                finally:
                    Path(candidate.name).unlink(missing_ok=True)
            try:
                result = ctl("connect")
                end = time.monotonic() + 10
                while result.get("core_state") == 1 and time.monotonic() < end:
                    time.sleep(.1)
                    result = ctl("status")
                if result.get("core_state") != 2:
                    raise TunnelError("core_start")
                gateway("ip","route","replace","default","dev","sotatest0","metric","10","table","109")
                if iface("from","192.0.2.2","iif","lan") != "sotatest0":
                    raise TunnelError("client_route")
                return result
            except Exception:
                ctl("disconnect")
                raise
        if op == "disconnect":
            result = ctl("disconnect")
            wait_released()
            return result
        if op == "ip":
            wan = measure(pid, True) if iface("uid","65534")=="wan" else {"ip":None,"status":"route_error"}
            routed = iface("from","192.0.2.2","iif","lan")=="sotatest0"
            tunnel = measure(client, False) if routed else {"ip":None,"status":"disconnected"}
            return {"wan":wan, "tunnel":tunnel, "time":datetime.now(timezone.utc).isoformat(),
                    "same_ip":bool(wan["ip"] and tunnel["ip"] == wan["ip"])}
        raise ValueError("unsupported operation")
    path.unlink(missing_ok=True)
    with socket.socket(socket.AF_UNIX) as server:
        server.bind(str(path))
        os.chown(path, allowed_uid,-1)
        path.chmod(0o600)
        server.listen(8)
        while True:
            with server.accept()[0] as conn:
                conn.settimeout(35)
                try:
                    _,uid,_=struct.unpack("3i",conn.getsockopt(socket.SOL_SOCKET,socket.SO_PEERCRED,12))
                    if uid != allowed_uid:
                        continue
                    with conn.makefile("rb") as stream:
                        line = stream.readline((1<<20)+1)
                    if len(line)>1<<20 or not line.endswith(b"\n"):
                        raise ValueError("invalid request")
                    reply={"result":handle(json.loads(line))}
                except TunnelError as error:
                    reply={"error":str(error)}
                except (OSError, RuntimeError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
                    reply={"error":"Операция со стендом не выполнена"}
                try:
                    conn.sendall(json.dumps(reply).encode()+b"\n")
                except OSError:
                    pass


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("layout",type=Path)
    args=p.parse_args()
    args.profile=None
    args.preflight=False
    isolate()
    def terminate(signum,frame):
        raise SystemExit(0)
    signal.signal(signal.SIGTERM,terminate)
    with tempfile.TemporaryDirectory(prefix="sota-runtime-") as work, \
         tempfile.TemporaryDirectory(prefix="sota-runtime-state-",dir="/dev/shm") as state, ExitStack() as stack:
        run(args,Path(work),Path(state),stack,panel)
