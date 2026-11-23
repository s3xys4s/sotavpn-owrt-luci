"""Build a local ARM64 OCI layout with a separately pinned Ubuntu runtime layer."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def blob(root, media, data):
    digest = sha(data)
    (root / "blobs/sha256" / digest).write_bytes(data)
    return {"mediaType": "application/vnd.oci.image." + media, "size": len(data), "digest": "sha256:" + digest}


def layer(root, files, directories):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        for name, mode in sorted(directories.items()):
            item = tarfile.TarInfo(name)
            item.type, item.mode = tarfile.DIRTYPE, mode
            tar.addfile(item)
        for name, (data, mode) in sorted(files.items()):
            item = tarfile.TarInfo(name)
            item.mode, item.size = mode, len(data)
            tar.addfile(item, io.BytesIO(data))
    raw = stream.getvalue()
    return blob(root, "layer.v1.tar+gzip", gzip.compress(raw, compresslevel=6, mtime=0)), "sha256:" + sha(raw)


def manifest(root, layers, config):
    conf = {"architecture": "arm64", "os": "linux",
            "rootfs": {"type": "layers", "diff_ids": [diff for _, diff in layers]}, "config": config}
    obj = {"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
           "config": blob(root, "config.v1+json", encoded(conf)), "layers": [desc for desc, _ in layers]}
    return blob(root, "manifest.v1+json", encoded(obj))


def base_files():
    if platform.machine() != "aarch64" or 'ID=ubuntu' not in Path("/etc/os-release").read_text().splitlines():
        raise ValueError("runtime builder must be native Ubuntu ARM64")
    # Only the distribution's nft and linked system DSOs enter this base.
    nft = Path("/usr/sbin/nft")
    deps = subprocess.check_output(["ldd", str(nft)], text=True)
    if "not found" in deps:
        raise ValueError("nft runtime dependency unavailable")
    paths = {nft} | {Path(p) for p in re.findall(r"(/[^\s()]+)\s+\(0x", deps)}
    files = {str(p).lstrip("/"): (p.read_bytes(), 0o755) for p in paths}
    certs = sorted(Path("/usr/share/ca-certificates/mozilla").glob("*.crt"))
    if not certs:
        raise ValueError("Ubuntu Mozilla CA package unavailable")
    files["etc/ssl/certs/ca-certificates.crt"] = (b"".join(p.read_bytes().rstrip() + b"\n" for p in certs), 0o444)
    return files


def build(bundle, wrapper, output, expected_base, source_sha):
    upstream = json.loads((bundle / "manifest.json").read_text())
    kernel = (bundle / "kittend").read_bytes()
    executable = wrapper.read_bytes()
    if upstream["architecture"] != "arm64" or sha(kernel) != upstream["bundle"][0]["sha256"]:
        raise ValueError("bundle integrity or architecture mismatch")
    if executable[:6] != b"\x7fELF\x02\x01" or int.from_bytes(executable[18:20], "little") != 183:
        raise ValueError("wrapper is not ARM64 ELF")
    if not re.fullmatch("[a-f0-9]{64}", source_sha):
        raise ValueError("wrapper source digest required")
    if output.exists():
        raise ValueError("output exists")
    output.parent.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix=".oci-", dir=output.parent))
    try:
        (root / "blobs/sha256").mkdir(parents=True)
        files = base_files()
        dirs = {"dev": 0o755, "dev/net": 0o755, "etc": 0o755, "proc": 0o555, "sys": 0o555,
                "tmp": 0o1777, "run": 0o755, "var/lib/sota-gateway": 0o700}
        base = layer(root, files, dirs)
        base_desc = manifest(root, [base], {})
        if expected_base and base_desc["digest"] != expected_base:
            raise ValueError("base digest changed; candidate stopped")
        app = layer(root, {"usr/bin/gateway": (executable, 0o555),
                    upstream["bundle"][0]["source_path"]: (kernel, 0o555)}, {})
        image = manifest(root, [base, app], {"Entrypoint": ["/usr/bin/gateway"], "Cmd": ["run"],
                         "Env": ["PATH=/usr/sbin:/usr/bin:/sbin:/bin"]})
        image["annotations"] = {"org.opencontainers.image.ref.name": "sota-gateway:lab-arm64"}
        (root / "index.json").write_bytes(encoded({"schemaVersion": 2,
            "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": [image]}))
        (root / "oci-layout").write_bytes(encoded({"imageLayoutVersion": "1.0.0"}))
        upstream.update(stage="lab-candidate", wrapper_source_sha256=source_sha,
                        wrapper_sha256=sha(executable), base_image_digest=base_desc["digest"],
                        image_digest=image["digest"], base_files={p: sha(b) for p,(b,_) in files.items()},
                        provider_test="unverified")
        (root / "build-manifest.json").write_text(json.dumps(upstream, indent=2, sort_keys=True) + "\n")
        root.rename(output)
        print("base", base_desc["digest"], "candidate", image["digest"])
    finally:
        if root.exists():
            shutil.rmtree(root)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("bundle", type=Path)
    p.add_argument("wrapper", type=Path)
    p.add_argument("output", type=Path)
    p.add_argument("--wrapper-source-sha", required=True)
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--base-digest")
    g.add_argument("--inspect-new-base", action="store_true", help="first local measurement only")
    args = p.parse_args()
    build(args.bundle, args.wrapper, args.output, args.base_digest, args.wrapper_source_sha)
