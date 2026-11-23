"""Private Linux client and upstream networks for the offline TUN experiment."""

from contextlib import contextmanager, ExitStack
import os
import socket
import struct
import subprocess
import sys


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def net(pid, *args):
    return ["nsenter", "-t", str(pid), "-n", "--", *args]


FORWARD_FILTER = b"""
table inet sota_test {
    chain forward {
        type filter hook forward priority -10; policy drop;
        iifname "lan" oifname "sotatest0" meta nfproto ipv4 counter accept
        iifname "sotatest0" oifname "lan" ct state established,related counter accept
        counter
    }
}
"""


@contextmanager
def topology():
    if os.getpid() != 1 or "SOTA_SMOKE_OUTER_NETNS" not in os.environ:
        raise RuntimeError("fixture requires its private PID and network namespaces")
    if os.readlink("/proc/self/ns/net") == os.environ["SOTA_SMOKE_OUTER_NETNS"]:
        raise RuntimeError("refusing host network")
    with ExitStack() as cleanup:
        peers = {}
        for link, prefix in (("lan", "192.0.2"), ("wan", "198.18.0")):
            holder = subprocess.Popen(["unshare", "--net", "--", "sleep", "120"])
            cleanup.callback(holder.wait)
            cleanup.callback(holder.terminate)
            import time
            for _ in range(100):
                if os.readlink(f"/proc/{holder.pid}/ns/net") != os.readlink("/proc/self/ns/net"):
                    break
                time.sleep(.01)
            else:
                raise RuntimeError("peer namespace was not created")
            peers[link] = holder.pid
            run("ip", "link", "add", link, "type", "veth", "peer", "name", "eth0",
                "netns", str(holder.pid))
            run("ip", "addr", "add", prefix + ".1/30", "dev", link)
            run("ip", "link", "set", link, "up")
            run(*net(holder.pid, "ip", "link", "set", "lo", "up"))
            run(*net(holder.pid, "ip", "addr", "add", prefix + ".2/30", "dev", "eth0"))
            run(*net(holder.pid, "ip", "link", "set", "eth0", "up"))
            run(*net(holder.pid, "ip", "route", "add", "default", "via", prefix + ".1"))
            v6 = "fd42:5750:" + ("1" if link == "lan" else "2") + "::"
            run("ip", "-6", "addr", "add", v6 + "1/64", "dev", link, "nodad")
            run(*net(holder.pid, "ip", "-6", "addr", "add", v6 + "2/64", "dev", "eth0", "nodad"))
            run(*net(holder.pid, "ip", "-6", "route", "add", "default", "via", v6 + "1"))
        # The only default route leads to the private fixture, never to the host LAN.
        run("ip", "route", "add", "default", "via", "198.18.0.2")
        run("sysctl", "-qw", "net.ipv4.ip_forward=1")
        run("sysctl", "-qw", "net.ipv6.conf.all.forwarding=1")
        run("nft", "-f", "-", input=FORWARD_FILTER)
        run("ip", "rule", "add", "pref", "90", "iif", "lan", "table", "109")
        run("ip", "rule", "add", "pref", "91", "to", "192.0.2.0/30", "lookup", "main")
        run("ip", "rule", "add", "pref", "92", "uidrange", "65534-65534", "lookup", "main")
        run("ip", "route", "add", "blackhole", "default", "metric", "32000", "table", "109")
        origin = subprocess.Popen(net(peers["wan"], sys.executable, __file__, "origin"),
                                  stdout=subprocess.PIPE, text=True)
        cleanup.callback(origin.wait)
        cleanup.callback(origin.terminate)
        if origin.stdout.readline().strip() != "ready":
            raise RuntimeError("fixture origin failed to bind")
        check_v6(False)  # Gateway itself can reach the upstream IPv6 listener.
        yield peers["lan"]


def serve():
    import selectors
    ip = "198.18.0.2"
    with selectors.DefaultSelector() as sel, socket.socket() as tcp, \
            socket.socket(type=socket.SOCK_DGRAM) as udp, socket.socket(type=socket.SOCK_DGRAM) as dns, \
            socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as v6:
        for sock, port in ((tcp, 29551), (udp, 29552), (dns, 53)):
            sock.bind((ip, port))
            sel.register(sock, selectors.EVENT_READ)
        tcp.listen(3)
        v6.bind(("fd42:5750:2::2", 29552))
        sel.register(v6, selectors.EVENT_READ)
        print("ready", flush=True)
        while True:
            for key, _ in sel.select():
                sock = key.fileobj
                if sock is tcp:
                    with tcp.accept()[0] as peer:
                        peer.settimeout(2)
                        if peer.getpeername()[0] == "198.18.0.1":
                            peer.sendall(peer.recv(1024))
                else:
                    msg, addr = sock.recvfrom(1024)
                    if sock is not v6 and addr[0] != "198.18.0.1":
                        continue
                    if sock is dns:
                        if msg[2:] != DNS_QUESTION:
                            continue
                        msg = msg[:2] + DNS_ANSWER
                    sock.sendto(msg, addr)


DNS_NAME = b"\x04sota\x04test\x00\x00\x01\x00\x01"
DNS_QUESTION = bytes.fromhex("0100 0001 0000 0000 0000") + DNS_NAME
DNS_ANSWER = (bytes.fromhex("8180 0001 0001 0000 0000") + DNS_NAME +
              bytes.fromhex("c00c 0001 0001 0000003c 0004 c0000263"))


def valid_dns(packet, txid):
    if len(packet) < 12 or packet[:2] != txid:
        return False
    flags, questions, answers = struct.unpack_from("!HHH", packet, 2)
    if not flags & 0x8000 or flags & 15 or (questions, answers) != (1, 1):
        return False
    if packet[12:12 + len(DNS_NAME)] != DNS_NAME:
        return False
    rr = packet[12 + len(DNS_NAME):]
    for owner in (b"\xc0\x0c", DNS_NAME[:-4]):
        if rr.startswith(owner):
            rdata = rr[len(owner):]
            return (len(rdata) >= 14 and rdata[:4] == b"\x00\x01\x00\x01"
                    and rdata[8:14] == b"\x00\x04\xc0\x00\x02\x63")
    return False


def check_v6(blocked):
    with socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as sock:
        sock.settimeout(.5)
        nonce = os.urandom(24)
        sock.connect(("fd42:5750:2::2", 29552))
        sock.send(nonce)
        try:
            reply = sock.recv(1024)
        except TimeoutError:
            if not blocked:
                raise
        else:
            if blocked or reply != nonce:
                raise ValueError("unexpected IPv6 path")
    print("IPv6 transit blocked" if blocked else "IPv6 upstream reachable", flush=True)


def verify(should_pass):
    target = "198.18.0.2"
    for kind, port in (("tcp", 29551), ("udp", 29552), ("dns", 53)):
        nonce = os.urandom(24)
        msg = nonce[:2] + DNS_QUESTION if kind == "dns" else nonce
        try:
            with socket.socket(type=socket.SOCK_STREAM if kind == "tcp" else socket.SOCK_DGRAM) as sock:
                sock.settimeout(2)
                sock.connect((target, port))
                sock.sendall(msg)
                result = sock.recv(2048)
        except OSError:
            if should_pass:
                raise
        else:
            if not should_pass or not (valid_dns(result, nonce[:2]) if kind == "dns" else result == nonce):
                raise RuntimeError(kind + " unexpected response")
        print(kind, "ok" if should_pass else "blocked", flush=True)
    check_v6(True)


if __name__ == "__main__":
    if sys.argv[1] == "origin":
        serve()
    else:
        verify(sys.argv[1] == "up")
