"""Run as root inside a fresh network/mount/PID namespace with an installed OpenWrt rootfs."""
import ipaddress
import os
import socket
import struct
import subprocess
import sys
import threading
import time
from pathlib import Path

root = Path(sys.argv[1]).resolve()
names = ["only.sota.test", "child.only.sota.test", "wild.sota.test",
         "child.wild.sota.test", "same.sota.test", "child.same.sota.test"]
addresses = {name: f"198.51.100.{20+i}" for i,name in enumerate(names)}


def answer(sock):
    while True:
        packet, peer = sock.recvfrom(4096)
        end = packet.index(0, 12)
        start, fields = 12, []
        while start < end:
            n = packet[start]
            fields.append(packet[start+1:start+1+n].decode())
            start += n+1
        addr = addresses.get(".".join(fields))
        question = packet[12:end+5]
        found = addr and question[-4:] == bytes.fromhex("00010001")
        reply = packet[:2] + struct.pack("!HHHHH",0x8180,1,1 if found else 0,0,0) + question
        if found:
            reply += bytes.fromhex("c00c00010001000000780004") + socket.inet_aton(addr)
        sock.sendto(reply, peer)


def query(name):
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(2)
        tx = os.urandom(2)
        question = b"".join(bytes([len(s)])+s.encode() for s in name.split(".")) + bytes.fromhex("0000010001")
        sock.sendto(tx+bytes.fromhex("01000001000000000000")+question,("127.0.0.1",1053))
        data = sock.recv(4096)
        assert data[:2] == tx and socket.inet_aton(addresses[name]) in data, name


def main():
    assert os.readlink("/proc/self/ns/net") != os.readlink("/proc/1/ns/net") or os.getpid()==1, "run via unshare"
    subprocess.run(["ip","link","set","lo","up"],check=True)
    subprocess.run(["nft","-f","-"],input=b"add table inet sota_guard\nadd set inet sota_guard resolved { type ipv4_addr; flags timeout; timeout 1h; }\nadd set inet sota_guard bypass_resolved { type ipv4_addr; flags timeout; timeout 1h; }\n",check=True)
    udp = socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
    udp.bind(("127.0.0.1",1054))
    threading.Thread(target=answer,args=(udp,),daemon=True).start()
    tmp = root/"tmp"/"run"/"sota"
    tmp.mkdir(parents=True,exist_ok=True)
    routes = ["-.only.sota.test","*.wild.sota.test","same.sota.test"]
    exceptions = ["-.same.sota.test"]
    shell = """
. /etc/init.d/sota
procd_open_instance() { :; }
procd_set_param() { case "$1" in command) shift; "$@" & child=$!;; esac; }
procd_close_instance() { :; }
start_domain_resolver exceptions 127.0.0.1:1055 1054 /var/run/sota/test-exceptions.dns
start_domain_resolver domains 127.0.0.1:1053 1055 /var/run/sota/test-list.dns
wait
"""
    for filename, group, domains in [("test-list","resolved",routes),("test-exceptions","bypass_resolved",exceptions)]:
        (tmp/(filename+".dns")).write_text("".join(f"nftset /{d}/#4:inet#sota_guard#{group}\n" for d in domains))
    import signal
    process = subprocess.Popen(["chroot",str(root),"/bin/sh","-c",shell],start_new_session=True)
    try:
        for _ in range(25):
            try:
                query(names[0])
                break
            except (OSError,AssertionError):
                time.sleep(.2)
        for name in names:
            query(name)
        for group, matches in [("resolved",{0,3,4,5}),("bypass_resolved",{4})]:
            for i,name in enumerate(names):
                result=subprocess.run(["nft","get","element","inet","sota_guard",group,
                    "{",str(ipaddress.IPv4Address(addresses[name])),"}"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                assert (result.returncode==0) == (i in matches), f"{group}: {name} expected {i in matches}"
        print("PASS installed SmartDNS, apex, wildcard, negative controls and overlapping exception sets")
    finally:
        if process.poll() is None:
            os.killpg(process.pid,signal.SIGTERM)
        process.wait(timeout=5)


if __name__ == "__main__":
    main()

