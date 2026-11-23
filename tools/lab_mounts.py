"""Keep runc and all helper mount operations inside a recursively private mount namespace."""

import os
from pathlib import Path
import subprocess
import sys


def isolate():
    current = os.readlink("/proc/self/ns/mnt")
    outer = os.environ.get("SOTA_LAB_PARENT_MNT")
    if outer is None:
        os.environ["SOTA_LAB_PARENT_MNT"] = current
        os.execvp("unshare", ["unshare","--mount","--propagation","private","--",
                              sys.executable,*sys.argv])
    if current == outer or current == os.readlink("/proc/1/ns/mnt"):
        raise RuntimeError("refusing setup in host mount namespace")
    mounts = Path("/proc/self/mountinfo").read_text().splitlines()
    if any(any(tag.startswith(("shared:", "master:")) for tag in line.split(" - ",1)[0].split()[6:])
           for line in mounts):
        raise RuntimeError("mount propagation is not private")
    if not Path("/sys/fs/cgroup/cgroup.controllers").is_file():
        subprocess.run(["mount","-t","cgroup2","-o","nosuid,nodev,noexec","none","/sys/fs/cgroup"],
                       check=True)
