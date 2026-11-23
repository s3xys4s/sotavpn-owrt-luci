"""Run the built OCI config in runc with no network uplink, capabilities or secrets."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
from lab_mounts import isolate


def get(layout, desc):
    alg, digest = desc["digest"].split(":")
    if alg != "sha256" or len(digest) != 64 or not all(c in "0123456789abcdef" for c in digest):
        raise ValueError("unsupported OCI digest")
    data = (layout / "blobs/sha256" / digest).read_bytes()
    if hashlib.sha256(data).hexdigest() != digest or len(data) != desc["size"]:
        raise ValueError("OCI blob mismatch")
    return data


def unpack(layout, root):
    root.mkdir()
    root.chmod(0o755)
    meta = json.loads((layout / "build-manifest.json").read_text())
    desc, = json.loads((layout / "index.json").read_text())["manifests"]
    if desc["digest"] != meta["image_digest"]:
        raise ValueError("candidate digest mismatch")
    image = json.loads(get(layout, desc))
    config = json.loads(get(layout, image["config"]))
    for layer, diff in zip(image["layers"], config["rootfs"]["diff_ids"], strict=True):
        raw = gzip.decompress(get(layout, layer))
        if "sha256:" + hashlib.sha256(raw).hexdigest() != diff:
            raise ValueError("OCI diff_id mismatch")
        with tarfile.open(fileobj=io.BytesIO(raw)) as tf:
            for item in tf:
                if not (item.isfile() or item.isdir()) or Path(item.name).is_absolute() or ".." in Path(item.name).parts:
                    raise ValueError("unexpected OCI entry")
                parent = root
                for part in Path(item.name).parts[:-1]:
                    parent = parent / part
                    if not parent.exists():
                        parent.mkdir()
                        parent.chmod(0o755)
                tf.extract(item, root, filter="data")
                if item.isdir():
                    (root / item.name).chmod(item.mode & 0o7777)
    daemon = root / meta["bundle"][0]["source_path"]
    if hashlib.sha256(daemon.read_bytes()).hexdigest() != meta["bundle"][0]["sha256"]:
        raise ValueError("OCI executable differs from upstream")
    return config


def smoke(layout, temp):
    root = temp / "rootfs"
    config = unpack(layout, root)
    daemon = root / "usr/libexec/sota-connect/kittend"
    state = temp / "state"
    state.mkdir(mode=0o700)
    os.chown(state, 65534, 65534)
    # The CLI reads the same mount and Unix socket as the process under test.
    (state / "config.json").write_text('{"AUTOCONNECT":true,"LOG_LEVEL":"info"}')
    env = [*config["config"]["Env"], "SOTA_CONFIG_FILE=/var/lib/sota-gateway/config.json",
           "SOTA_AUTOCONNECT=false"]
    spec = {"ociVersion": "1.0.2", "root": {"path": str(root), "readonly": True},
            "process": {"terminal": False, "user": {"uid": 65534, "gid": 65534}, "cwd": "/",
                        "args": config["config"]["Entrypoint"] + config["config"]["Cmd"],
                        "env": env, "noNewPrivileges": True,
                        "capabilities": {k: [] for k in ("bounding", "effective", "inheritable", "permitted", "ambient")}},
            "mounts": [{"destination": "/proc", "type": "proc", "source": "proc",
                        "options": ["nosuid", "nodev", "noexec", "ro"]},
                       {"destination": "/var/lib/sota-gateway", "type": "bind", "source": str(state),
                        "options": ["rbind", "rw", "nosuid", "nodev", "noexec"]}],
            "linux": {"namespaces": [{"type": t} for t in ("pid", "network", "mount", "ipc", "uts")]}}
    (temp / "config.json").write_text(json.dumps(spec))
    runc = ["runc", "--root", str(temp / "runtime")]
    name = "sota-lab"
    def command(*args, **kw):
        return subprocess.run([*runc, *args], cwd=temp, timeout=15, check=True,
                              capture_output=True, text=True, **kw).stdout
    def gateway(arg):
        return json.loads(command("exec", name, "/usr/bin/gateway", arg))
    try:
        with tempfile.TemporaryFile(mode="w+") as log:
            run = subprocess.run([*runc, "run", "-d", "--bundle", str(temp), name],
                                 stdin=subprocess.DEVNULL, stdout=log, stderr=log, timeout=15)
            if run.returncode:
                log.seek(0)
                raise RuntimeError("runc start failed: " + log.read(1500))
        for _ in range(50):
            try:
                status = gateway("status")
                if status["core_responding"]:
                    break
            except subprocess.CalledProcessError:
                pass
            time.sleep(.1)
        else:
            raise RuntimeError("controller/core did not become ready")
        print("gateway", json.dumps(status), flush=True)
        if not status["core_process_alive"] or status["core_state"] != 0 or status["traffic_verification"] != "unverified":
            raise RuntimeError("incorrect idle status")
        sources = gateway("check")["sources"]
        if [sources[k] for k in ("AUTOCONNECT", "LOG_LEVEL", "API_ENABLED")] != ["env", "file", "default"]:
            raise RuntimeError("configuration precedence mismatch")
        invalid = [
            {"SOTA_WEB_ENABLED": "true"},
            {"SOTA_ACCESS_KEY": "redaction-fixture", "SOTA_ACCESS_KEY_FILE": "/unused"},
            {"SOTA_AUTOCONNECT": "true"}, {"SOTA_ROUTE_MODE": "provider"}]
        for case in invalid:
            args = [flag for k, v in case.items() for flag in ("--env", k+"="+v)]
            try:
                command("exec", *args, name, "/usr/bin/gateway", "check")
            except subprocess.CalledProcessError as e:
                if "redaction-fixture" in e.stdout + e.stderr:
                    raise RuntimeError("secret in validation output")
            else:
                raise RuntimeError("invalid configuration accepted")
        # PID is reported by runc, and the two tables belong to its private netns.
        pid = json.loads(command("state", name))["pid"]
        for table in ("tcp", "tcp6"):
            if any(line.split()[3] == "0A" for line in Path(f"/proc/{pid}/net/{table}").read_text().splitlines()[1:]):
                raise RuntimeError("TCP listener in headless candidate")
        print("config precedence, validation, redaction and zero TCP listeners OK", flush=True)
        print("runtime", command("exec", name, "/usr/sbin/nft", "--version").strip(), flush=True)
        # Only signal the packaged direct child of this private container init.
        children = set()
        for path in Path(f"/proc/{pid}/task").glob("*/children"):
            children.update(path.read_text().split())
        child, = [p for p in children if os.path.samefile(f"/proc/{p}/exe", daemon)
                  and os.path.samefile(f"/proc/{p}/ns/pid", f"/proc/{pid}/ns/pid")]
        os.kill(int(child), 9)
        for _ in range(50):
            if not Path(f"/proc/{child}").exists() and gateway("status")["core_responding"]:
                break
            time.sleep(.1)
        else:
            raise RuntimeError("supervisor failed to recover core")
        print("supervisor recovered killed core", flush=True)
        command("kill", name, "TERM")
        for _ in range(60):
            if json.loads(command("state", name))["status"] == "stopped":
                break
            time.sleep(.1)
        else:
            raise RuntimeError("shutdown timeout")
        print("SIGTERM shutdown OK", flush=True)
    finally:
        subprocess.run([*runc, "delete", "--force", name], cwd=temp, capture_output=True, timeout=10)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("layout", type=Path)
    args = p.parse_args()
    isolate()
    with tempfile.TemporaryDirectory(prefix="sota-oci-") as tmp:
        try:
            smoke(args.layout.resolve(), Path(tmp))
        except subprocess.CalledProcessError as err:
            raise SystemExit("runc failed: " + err.stderr)
