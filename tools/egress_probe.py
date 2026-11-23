"""External test traffic only. Carries no provider identity or configuration."""

import http.client
import ipaddress
import json
import os
import socket
import ssl
import struct
import sys


def trace():
    conn = http.client.HTTPSConnection("1.1.1.1", timeout=7, context=ssl.create_default_context())
    try:
        conn.request("GET", "/cdn-cgi/trace", headers={"Connection": "close"})
        reply = conn.getresponse()
        if reply.status != 200:
            raise ValueError("trace not available")
        lines = reply.read(8192).decode().splitlines()
        return str(ipaddress.IPv4Address(next(line[3:] for line in lines if line.startswith("ip="))))
    finally:
        conn.close()


def udp(host, port, payload):
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(5)
        sock.connect((host, port))
        sock.send(payload)
        return sock.recv(4096)


def skip_name(buf, index):
    while index < len(buf):
        n = buf[index]
        index += 1
        if n == 0:
            return index
        if n & 0xc0 == 0xc0 and index < len(buf):
            return index + 1
        if n > 63:
            break
        index += n
    raise ValueError("invalid resolver response")


def stun_address():
    tx = os.urandom(2)
    question = b"\x04stun\x0acloudflare\x03com\x00\x00\x01\x00\x01"
    packet = udp("1.1.1.1", 53, tx + bytes.fromhex("0100 0001 0000 0000 0000") + question)
    if packet[:2] != tx or len(packet) < 12:
        raise ValueError("invalid DNS transaction")
    flags, qcount, acount = struct.unpack_from("!HHH", packet, 2)
    if not flags & 0x8000 or flags & 15 or qcount != 1:
        raise ValueError("DNS lookup failed")
    index = skip_name(packet, 12) + 4
    for _ in range(acount):
        index = skip_name(packet, index)
        kind, cls, _, length = struct.unpack_from("!HHIH", packet, index)
        index += 10
        if kind == 1 and cls == 1 and length == 4:
            return socket.inet_ntoa(packet[index:index + length])
        index += length
    raise ValueError("missing public STUN IPv4 address")


def stun(host):
    cookie = bytes.fromhex("2112a442")
    tx = os.urandom(12)
    packet = udp(host, 3478, b"\x00\x01\x00\x00" + cookie + tx)
    if packet[:2] != b"\x01\x01" or packet[4:20] != cookie + tx:
        raise ValueError("invalid STUN transaction")
    i = 20
    while i + 4 <= len(packet):
        kind, size = struct.unpack_from("!HH", packet, i)
        data = packet[i + 4:i + 4 + size]
        if kind == 0x20 and len(data) == 8 and data[1] == 1:
            return socket.inet_ntoa(bytes(a ^ b for a, b in zip(data[4:], cookie)))
        i += 4 + ((size + 3) & ~3)
    raise ValueError("missing STUN mapped IPv4")


if __name__ == "__main__":
    try:
        if sys.argv[1:] == ["--tcp-only"]:
            print(trace())
        else:
            print(json.dumps({"tcp": trace(), "udp": stun(stun_address())}))
    except (OSError, ValueError, StopIteration, struct.error, http.client.HTTPException):
        raise SystemExit("external TCP/TLS, UDP/STUN or DNS probe unavailable")
