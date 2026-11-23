#!/bin/sh
# Run in a clean native Ubuntu ARM64 build directory. No registry credentials.
set -eu
if [ "$#" -ne 4 ]; then
    echo 'usage: sh tools/check_candidate.sh TAG DEBIAN_VERSION NEW_DIRECTORY BASE_IMAGE_DIGEST' >&2
    exit 2
fi
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
dest=$3
mkdir -- "$dest"
dest=$(CDPATH='' cd -- "$dest" && pwd)
cd -- "$root"
python3 tools/candidate.py "$1" "$2" arm64 "$dest/bundle" --cache "$dest/cache"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w -buildid=' -o "$dest/gateway" ./cmd/gateway
src=$(python3 tools/wrapper_id.py)
python3 tools/build_oci.py "$dest/bundle" "$dest/gateway" "$dest/image" --wrapper-source-sha "$src" --base-digest "$4"
core=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["bundle"][0]["sha256"])' "$dest/bundle/manifest.json")
python3 tools/offline_smoke.py "$dest/bundle/kittend" "$core"
python3 tools/offline_smoke.py "$dest/bundle/kittend" "$core" --tun
python3 tools/offline_smoke.py "$dest/bundle/kittend" "$core" --tun --crash
python3 tools/oci_smoke.py "$dest/image"
python3 tools/live_accept.py "$dest/image" --preflight
printf '%s\n' 'Offline candidate checks passed. Provider, target router and rollback gates remain separate.'
if [ -z "${SOTA_CI_PROFILE_FILE:-}" ] || [ -z "${SOTA_CI_EXPECT_EGRESS:-}" ]; then
    printf '%s\n' 'Provider acceptance unverified: private profile or expected egress is absent.' >&2
    exit 77
fi
python3 tools/live_accept.py "$dest/image" --profile "$SOTA_CI_PROFILE_FILE" --expect-egress "$SOTA_CI_EXPECT_EGRESS"
