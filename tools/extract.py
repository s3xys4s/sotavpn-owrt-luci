"""Extract a byte-identical candidate and check its reviewed package/IPC contract."""

import argparse
import hashlib
import io
import json
from pathlib import Path
import re
import shutil
import struct
import tarfile
import tempfile
from urllib.parse import urlsplit

from elf_contract import inspect_core, read_elf


CORE = "usr/libexec/sota-connect/kittend"
UNIT = "usr/lib/systemd/system/sota-connect.service"
BASELINE = Path(__file__).resolve().parents[1] / "research" / "contract-v1.json"
MAX_PACKAGE = 128 << 20
MAX_PAYLOAD = 300 << 20
MACHINES = {"arm64": 183, "amd64": 62}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def dump(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def members(blob):
    if not blob.startswith(b"!<arch>\n"):
        raise ValueError("invalid Debian ar header")
    offset, entries = 8, {}
    while offset < len(blob):
        header = blob[offset:offset + 60]
        if len(header) != 60 or header[58:] != b"`\n":
            raise ValueError("truncated ar header")
        name = header[:16].decode("ascii").strip().rstrip("/")
        size = int(header[48:58])
        offset += 60
        if size < 0 or size > len(blob) - offset or name in entries:
            raise ValueError("duplicate or truncated ar member")
        entries[name] = blob[offset:offset + size]
        offset += size + size % 2
    if set(entries) != {"debian-binary", "control.tar.gz", "data.tar.gz"} or entries["debian-binary"] != b"2.0\n":
        raise ValueError("Debian outer structure/compression changed")
    return entries


def inventory(archive, control=False):
    files, selected, total = {}, {}, 0
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r|gz") as tar:
        for item in tar:
            name = item.name.removeprefix("./")
            if name in ("", ".") and item.isdir():
                continue
            if any(p in ("", ".", "..") for p in name.split("/")) or "\\" in name or name in files:
                raise ValueError("invalid or duplicate tar member")
            total += item.size
            if len(files) >= 5000 or total > MAX_PAYLOAD or item.size < 0:
                raise ValueError("archive exceeds reviewed size limits")
            record = dict(mode=oct(item.mode), size=item.size)
            if item.isfile():
                data = tar.extractfile(item).read()
                record.update(kind="file", sha256=sha(data))
                if control or name in (CORE, UNIT):
                    selected[name] = data
                if data.startswith(b"\x7fELF"):
                    record["elf"] = read_elf(data)[0]
            elif item.isdir():
                record["kind"] = "dir"
            elif item.issym():
                record.update(kind="symlink", target=item.linkname)
            else:
                raise ValueError("unreviewed tar entry type at " + name)
            files[name] = record
    return files, selected


def analyze(blob, version, arch):
    entries = members(blob)
    controls, meta = inventory(entries["control.tar.gz"], control=True)
    files, data = inventory(entries["data.tar.gz"])
    fields = dict(line.split(": ", 1) for line in meta["control"].decode().splitlines()
                  if line and not line.startswith(" ") and ": " in line)
    if (fields["Package"], fields["Version"], fields["Architecture"]) != ("sotavpn", version, arch):
        raise ValueError("package identity does not match requested package/version/architecture")
    core = inspect_core(data[CORE])
    if core["elf"]["machine"] != MACHINES[arch] or "build\tGOOS=linux" not in core["go_build"]:
        raise ValueError("unexpected daemon architecture or OS")
    shape = {k: [v["kind"], v["mode"], v.get("target")] for k, v in files.items()}
    contract = dict(layout_sha256=sha(json.dumps(shape, sort_keys=True).encode()),
                    control_files=sorted(controls), unit_sha256=sha(data[UNIT]),
                    wire_types=core["wire_types"], dynamic_symbols=core["elf"]["dynamic_symbols"])
    return contract, data[CORE], dict(debian=fields, files=files, control=controls, core=core)


def extract(package, output, source, expected, version, arch, baseline=BASELINE):
    if output.exists():
        raise ValueError("output exists; use a new candidate directory")
    url = urlsplit(source)
    if url.scheme != "https" or url.hostname not in ("github.com", "sotacdn.com", "storage.sota.ac") or url.username or url.password or url.query or url.fragment:
        raise ValueError("expected an official HTTPS artifact source without credentials")
    if package.stat().st_size > MAX_PACKAGE or not re.fullmatch("[a-f0-9]{64}", expected):
        raise ValueError("invalid package size or digest")
    blob = package.read_bytes()
    if sha(blob) != expected:
        raise ValueError("package SHA-256 mismatch")
    contract, core, report = analyze(blob, version, arch)
    reviewed = json.loads(baseline.read_text(encoding="utf-8"))
    differences = sorted(k for k in contract if contract[k] != reviewed.get(k))
    if differences:
        raise ValueError("review required before packaging: " + ", ".join(differences))
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".extract-", dir=output.parent))
    try:
        binary = staging / "kittend"
        binary.write_bytes(core)
        binary.chmod(0o555)
        digest = sha(binary.read_bytes())
        if digest != report["files"][CORE]["sha256"]:
            raise ValueError("extracted executable differs")
        dump(staging / "inventory.json", report)
        dump(staging / "manifest.json", dict(
            package_url=source, package_version=version, architecture=arch,
            package_sha256=expected, package_bytes=len(blob),
            bundle=[dict(source_path=CORE, path="kittend", bytes=len(core), sha256=digest)],
            contract_sha256=sha(baseline.read_bytes()), adapter_version="kittend-ipc-1",
            wrapper_commit=None, base_image_digest=None, image_digest=None,
            stage="extracted", dynamic_compatibility="untested"))
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("package", type=Path)
    p.add_argument("output", type=Path)
    p.add_argument("--source", required=True)
    p.add_argument("--sha256", required=True)
    p.add_argument("--version", required=True, help="full Debian version, including build number")
    p.add_argument("--arch", choices=MACHINES, required=True)
    args = p.parse_args()
    try:
        extract(args.package, args.output, args.source, args.sha256, args.version, args.arch)
    except (OSError, ValueError, KeyError, IndexError, StopIteration, struct.error, tarfile.TarError) as exc:
        p.exit(1, "extraction stopped: " + str(exc) + "\n")
