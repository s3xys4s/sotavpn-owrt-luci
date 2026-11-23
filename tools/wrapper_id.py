"""Identify the wrapper source tree even outside a Git checkout."""

import hashlib
from pathlib import Path


def wrapper_id(root):
    result = hashlib.sha256()
    files = [root / "go.mod", *root.glob("cmd/**/*.go"), *root.glob("internal/**/*.go")]
    for path in sorted(files, key=lambda p: p.relative_to(root).as_posix()):
        result.update(path.relative_to(root).as_posix().encode() + b"\0")
        result.update(hashlib.sha256(path.read_bytes()).digest())
    return result.hexdigest()


if __name__ == "__main__":
    print(wrapper_id(Path(__file__).resolve().parents[1]))
