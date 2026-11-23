"""Exercise the HTTP panel across a real encrypted local test transport."""
import argparse
from contextlib import ExitStack
import ipaddress
import json
import multiprocessing
import os
from pathlib import Path
import pwd
import secrets
import socket
import subprocess
import tempfile
import time

from lab_mounts import isolate
from live_accept import add_process, run
from panel_runtime import panel
from panel_smoke import Panel
from transit_fixture import net


def exercise(pid, client, ctl, gateway, state, binary, serverroot):
    with tempfile.TemporaryDirectory(prefix="sota-web-test-") as tmp, ExitStack() as stack:
        t = Path(tmp)
        t.chmod(0o711)
        shared = t/"transport.json"
        key = secrets.token_hex(20)
        port = 18631
        shared.write_text(json.dumps({"server":"127.0.0.1","server_port":port,"method":"aes-256-gcm","password":key}))
        os.chown(shared,65534,65534)
        shared.chmod(0o400)
        ss = add_process(stack,net(pid,"setpriv","--reuid=65534","--regid=65534","--clear-groups",
            "env","LD_LIBRARY_PATH="+str(serverroot/"usr/lib/aarch64-linux-gnu")+":"+str(serverroot/"lib/aarch64-linux-gnu"),
            str(serverroot/"usr/bin/ss-server"),"-c",str(shared)))
        broker = multiprocessing.get_context("fork").Process(target=panel,args=(pid,client,ctl,gateway,state))
        broker.start()
        stack.callback(broker.join,4)
        stack.callback(broker.terminate)
        account = pwd.getpwnam("sota-panel")
        cfg=t/"state";cfg.mkdir(mode=0o700)
        os.chown(cfg,account.pw_uid,account.pw_gid)
        with socket.socket() as s:
            s.bind(("127.0.0.1",0)); httpport=s.getsockname()[1]
        add_process(stack,["setpriv","--reuid=sota-panel","--regid=sota-panel","--clear-groups",
            "env","-i","SOTA_WEB_ENABLED=true","SOTA_API_ENABLED=true",f"SOTA_HTTP_LISTEN=127.0.0.1:{httpport}",
            str(binary),"-state",str(cfg)])
        http=Panel(f"http://127.0.0.1:{httpport}")
        for _ in range(40):
            try:
                if http.rpc("status")["core"]["core_responding"]:break
            except OSError:pass
            time.sleep(.1)
        else:raise RuntimeError("test UI unavailable")
        assert http.rpc("diagnostics.ip")["tunnel"]["status"]=="disconnected"
        http.rpc("profile.import",{"profile":{"outbounds":[{"type":"shadowsocks","tag":"proxy","server":"127.0.0.1",
                 "server_port":port,"method":"aes-256-gcm","password":key}]}})
        http.rpc("tunnel.connect")
        good=http.rpc("diagnostics.ip")
        for path in ("wan","tunnel"):
            if good[path]["status"]!="ok" or not ipaddress.ip_address(good[path]["ip"]).is_global:
                raise ValueError("missing public IPv4: "+path)
        print("HTTP RPC -> separate client -> kittend TUN -> encrypted Shadowsocks fixture -> external IP OK",flush=True)
        http.rpc("tunnel.restart")
        assert http.rpc("status")["core"]["core_state"] == 2
        assert http.rpc("diagnostics.ip")["tunnel"]["status"]=="ok"
        print("explicit restart restored the imported transport",flush=True)
        ss.terminate();ss.wait(3)
        failed=http.rpc("diagnostics.ip")
        assert failed["wan"]["status"]=="ok" and failed["tunnel"]=={"ip":None,"status":"unavailable"}
        print("upstream failure: WAN reachable, active TUN returned no IP",flush=True)
        http.rpc("tunnel.disconnect")
        assert http.rpc("diagnostics.ip")["tunnel"]["status"]=="disconnected"
        print("explicit disconnect OK",flush=True)
        http.rpc("tunnel.connect")
        assert http.rpc("status")["core"]["core_state"] == 2
        http.rpc("tunnel.disconnect")
        print("immediate reconnect after TUN teardown OK",flush=True)


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("layout",type=Path);p.add_argument("binary",type=Path);p.add_argument("serverroot",type=Path)
    args=p.parse_args()
    args.profile=None;args.preflight=False
    os.umask(0o077)  # Match sota-lab-runtime.service when runc creates /dev.
    isolate()
    # Private clone only. Keep the production broker and account untouched.
    subprocess.run(["mount","-t","tmpfs","-o","mode=0755,nodev,nosuid,noexec","tmpfs","/run/sota-lab"],check=True)
    with tempfile.TemporaryDirectory(prefix="sota-web-oci-") as work, \
         tempfile.TemporaryDirectory(prefix="sota-web-state-",dir="/dev/shm") as state, ExitStack() as stack:
        run(args,Path(work),Path(state),stack,lambda *a:exercise(*a,args.binary.resolve(),args.serverroot.resolve()))
