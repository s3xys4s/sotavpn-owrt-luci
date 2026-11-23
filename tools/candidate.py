"""Fetch a release, verify its GitHub asset digest and extract an inspection candidate."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile
import urllib.request
from urllib.parse import urlsplit

from extract import MAX_PACKAGE, extract


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urlsplit(newurl).scheme != "https":
            raise ValueError("non-HTTPS redirect")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def candidate(tag, version, arch, source, cache, output):
    if not re.fullmatch(r"v[0-9]+(?:\.[0-9]+){2}", tag):
        raise ValueError("expected a versioned release tag")
    opener = urllib.request.build_opener(HTTPSRedirect())
    base = "https://api.github.com/repos/sotavpn/sotavpn-app/releases/tags/"
    with opener.open(base + tag, timeout=25) as response:
        release = json.loads(response.read(2 << 20))
    assets = [a for a in release["assets"] if a["name"] == f"sotaconnect_{arch}.deb"]
    if release["tag_name"] != tag or release["draft"] or len(assets) != 1:
        raise ValueError("release or Debian asset missing/ambiguous")
    asset = assets[0]
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", asset.get("digest") or ""):
        raise ValueError("release has no verifiable SHA-256")
    expected = asset["digest"].split(":")[1]
    release_url = f"https://github.com/sotavpn/sotavpn-app/releases/download/{tag}/{asset['name']}"
    if asset["browser_download_url"] != release_url:
        raise ValueError("release asset URL changed")
    source = source or release_url
    if source != release_url and (urlsplit(source).scheme, urlsplit(source).netloc) != ("https", "sotacdn.com"):
        raise ValueError("source must be the official release or HTTPS Sota CDN")
    cache.mkdir(parents=True, exist_ok=True)
    package = cache / (expected + ".deb")
    if not package.exists():
        tmp = None
        try:
            with tempfile.NamedTemporaryFile(dir=cache, delete=False) as dest:
                tmp = Path(dest.name)
                h = hashlib.sha256()
                with opener.open(source, timeout=45) as response:
                    while chunk := response.read(1 << 20):
                        if dest.tell() + len(chunk) > min(asset["size"], MAX_PACKAGE):
                            raise ValueError("download exceeds official size or local limit")
                        dest.write(chunk)
                        h.update(chunk)
                if dest.tell() != asset["size"] or h.hexdigest() != expected:
                    raise ValueError("release asset digest/size mismatch")
            try:
                os.link(tmp, package)
            except FileExistsError:
                pass
        finally:
            if tmp is not None:
                tmp.unlink(missing_ok=True)
    # The cache only proves content identity. On a cache hit cite the versioned
    # asset whose digest was verified, independently of its original transport.
    extract(package, output, release_url, expected, version, arch)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("tag")
    p.add_argument("version", help="full Debian version")
    p.add_argument("arch", choices=("amd64", "arm64"))
    p.add_argument("output", type=Path)
    p.add_argument("--cache", type=Path, default=Path("cache"))
    p.add_argument("--source", help="alternative official CDN URL for the same release asset")
    args = p.parse_args()
    try:
        candidate(args.tag, args.version, args.arch, args.source, args.cache, args.output)
    except (ValueError, KeyError, OSError) as e:
        p.exit(1, "candidate stopped: " + str(e) + "\n")
