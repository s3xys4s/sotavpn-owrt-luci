"""Run on the QEMU lab host with exclusive use of its router and DHCP client.

Usage: python3 openwrt_regression.py /var/lib/sota-wrt-lab
Place egress_probe.py alongside this file. Both SSH host keys must be known.
"""
import http.server
import ipaddress
import json
import re
import subprocess
import sys
import threading
import time
from pathlib import Path

lab = Path(sys.argv[1])
controller = "/usr/libexec/sota/controller"
router, client = 22240, 22241
probe = Path(__file__).with_name("egress_probe.py").read_text()


def ssh(port, command, stdin=""):
    args = ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
            "-o", f"UserKnownHostsFile={lab}/known_hosts", "-o", "ConnectTimeout=5",
            "-i", str(lab / "lab_ed25519"), "-p", str(port), "root@127.0.0.1", command]
    return subprocess.run(args, input=stdin, text=True, stdout=subprocess.PIPE,
                          check=True, timeout=85).stdout.strip()


def rpc(method, **params):
    reply = json.loads(ssh(router, controller + " -call " + method, json.dumps(params)))
    if "error" in reply:
        raise RuntimeError(method + ": " + reply["error"])
    return reply["data"]


def test(condition, message):
    if not condition:
        raise AssertionError(message)
    print("PASS", message, flush=True)


def retry(fn, seconds=18):
    until = time.monotonic() + seconds
    while True:
        try:
            return fn()
        except (RuntimeError, subprocess.SubprocessError, AssertionError):
            if time.monotonic() >= until:
                raise
            time.sleep(1)


def client_python(code):
    return ssh(client, "python3 -", probe.split('if __name__ == "__main__":')[0] + "\n" + code)


empty = {"rules": "", "files": [], "sources": []}


def policy(direction="vpn", rules="", exceptions=None, files=(), sources=(), clients="", interval=360):
    current = rpc("network.get")
    for key in ("catalog", "russian_default"):
        current.pop(key, None)
    current.update(direction=direction, rules=rules, files=list(files), sources=list(sources),
                   exceptions=exceptions or empty, clients=clients, refresh_minutes=interval, bypass="")
    return rpc("network.set", network=current)


def exits():
    d = retry(lambda: rpc("diagnostics.ip"))
    assert d["tunnel"]["status"] == d["wan"]["status"] == "ok" and not d["same_ip"], "IP diagnostics"
    return d["wan"]["ip"], d["tunnel"]["ip"]


def check_policy_toggle():
    original = rpc("network.get")
    status = rpc("status")
    assert original["enabled"] and status["core"]["core_state"] == 2
    link = ssh(router,"ip -6 -o addr show dev eth0 scope link | awk '{print $4}'").split("/")[0]
    assert ipaddress.IPv6Address(link).is_link_local
    v6_probe = """
with socket.socket(socket.AF_INET6,socket.SOCK_DGRAM) as sock:
    sock.settimeout(3)
    tx=os.urandom(2)
    sock.sendto(tx+bytes.fromhex("01000001000000000000")+b'\\x09localhost\\x00\\x00\\x01\\x00\\x01',
                (LINK,53,0,socket.if_nametoindex('eth0')))
    try:
        print('answer' if sock.recv(2048)[:2]==tx else 'invalid')
    except TimeoutError:
        print('timeout')
""".replace("LINK",repr(link))
    wan,_ = exits()
    def switch(enabled):
        value=rpc("network.get")
        value.pop("catalog",None)
        value.pop("russian_default",None)
        rpc("network.set",network={**value,"enabled":enabled})
    try:
        switch(False)
        assert not rpc("status")["core"]["policy_enabled"]
        expect_exit("turning routing off restores direct client WAN", wan)
        test(client_python(v6_probe) == "answer","IPv6 DNS positive control with policy off")
        switch(True)
        test(client_python(v6_probe) == "timeout","policy blocks LAN DNS over IPv6")
    finally:
        if not rpc("network.get")["enabled"]:
            switch(True)
        if rpc("status")["core"]["core_state"] != 2:
            connected(status["protocol"],status["selected"])
        test(rpc("network.get")["revision"] == original["revision"],"routing toggle restored the original settings")


def expect_exit(label, expected, domain=False):
    lookup = ('import socket\nassert "1.1.1.1" in socket.gethostbyname_ex("one.one.one.one")[2]\n'
              if domain else "")
    def measure():
        assert client_python(lookup + "print(trace())") == expected, label
    retry(measure)
    test(True, label)


def connected(mode, selected):
    rpc("tunnel.connect", id=selected, protocol=mode)
    def check():
        status = rpc("status")
        assert status["core"]["core_state"] == 2 and status["protocol"] == mode
    retry(check)


def element(group, ip):
    address = ipaddress.IPv4Address(ip)
    cmd = f"nft get element inet sota_guard {group} '{{ {address} }}' >/dev/null 2>&1 && echo yes || echo no"
    return ssh(router, cmd) == "yes"


def doh_probe(fresh_dns):
    original = rpc("network.get")
    original.pop("catalog",None)
    original.pop("russian_default",None)
    capture = ssh(router, "tcpdump -lnni sota0 -s 256 'port 53 or (tcp port 443 and (host 8.8.4.4 or host 8.8.8.8))' >/tmp/sota-reg-doh 2>/dev/null </dev/null & echo $!")
    assert capture.isdigit()
    try:
        current = rpc("network.set", network={**original,"doh_url":"https://dns.google/dns-query"})
        def dns_ok():
            assert int(client_python(fresh_dns+"\nprint(fresh_dns())")) in (0,3)
        retry(dns_ok,30)
        time.sleep(1)
        packets = ssh(router,"cat /tmp/sota-reg-doh")
        test(bool(re.search(r"> 8\.8\.(4\.4|8\.8)\.443: Flags \[P",packets)), "DoH to the selected resolver traverses TUN")
        test(not re.search(r"[0-9a-f]{24}\.cloudflare\.com",packets), "no plain DNS request for the client test name")
        current.pop("catalog",None)
        current.pop("russian_default",None)
        rpc("network.set",network={**current,"doh_url":"https://dns.google:444/dns-query"})
        time.sleep(2)
        outcome=client_python(fresh_dns+"\ntry: print(fresh_dns())\nexcept (TimeoutError,ConnectionError): print('offline')")
        test(outcome in ("offline","2","5"), "unavailable DoH does not fall back to plain DNS")
    finally:
        ssh(router,"kill "+capture+" 2>/dev/null || :")
        original["revision"] = rpc("network.get")["revision"]
        rpc("network.set",network=original)


def preset_samples(current):
    doc = json.loads(ssh(router,'cat "/etc/sota/route-list.txt.$(uci get sota.main.rule_hash)"'))
    youtube = next(p for p in current["catalog"] if p["name"] == "YouTube" and p["kind"] == "cidr4")
    listed = next(row["text"] for row in doc["lists"] if row["url"] == youtube["url"])
    excluded = next(row["text"] for row in doc["exceptions"]["files"] if row["name"] == current["russian_default"]["name"])
    for group, text in [("listed",listed), ("bypass",excluded)]:
        cidr = next(ipaddress.ip_network(s.strip(), strict=False) for s in text.splitlines()
                    if re.fullmatch(r"[\d.]+/\d+",s.strip()))
        test(element(group,next(iter(cidr.hosts()))), "installed preset CIDR in " + group)
    for group, domain in [("resolved","youtube.com"),("bypass_resolved","vk.com")]:
        addresses = json.loads(client_python("print(json.dumps(socket.gethostbyname_ex("+repr(domain)+")[2]))"))
        test(addresses and all(element(group,ip) for ip in addresses), "installed preset DNS answers for " + domain)


class ListHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        content = self.server.entries.get(self.path)
        self.send_response(200 if content else 503)
        self.end_headers()
        self.wfile.write((content or "").encode())

    def log_message(self, *_):
        pass


def main():
    starting = rpc("status")
    original = rpc("network.get")
    preset_samples(original)
    test(starting["has_key"] and starting["hwid"] and starting["core"]["core_state"] == 2,
         "registered device and active tunnel")
    selected, mode = starting["selected"], starting["protocol"]
    test(selected and any(loc["id"] == selected for loc in rpc("server.list")), "refresh provider locations")
    check_policy_toggle()
    backup = ssh(router, "umask 077; mktemp -d /root/sota-regression.XXXXXX")
    assert re.fullmatch(r"/root/sota-regression\.[A-Za-z0-9]+", backup)
    ssh(router, f"set -e; cp -p /etc/config/sota {backup}/config; cp -p /etc/sota/route-list.txt* {backup}/")
    with http.server.ThreadingHTTPServer(("127.0.0.1", 0), ListHandler) as server:
        server.entries = {"/routes": "198.51.100.141/32", "/exceptions": "198.51.100.142/32"}
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = f"http://10.0.2.2:{server.server_port}"
        try:
            for _ in range(3):
                for rule in ("one.one.one.one", ""):
                    policy(direction="wan", rules=rule)
                    def check_dns_listener():
                        assert client_python("print(stun_address())")
                    retry(check_dns_listener)
            test(True, "client DNS survives repeated removal and re-addition of the last domain rule")
            policy(direction="wan")
            for protocol in dict.fromkeys(["vless", "hysteria2", "auto", mode]):
                rpc("tunnel.disconnect")
                connected(protocol, selected)
                wan, vpn = exits()
                def check_transport():
                    observed = json.loads(client_python("print(json.dumps([trace(),stun(stun_address())]))"))
                    assert observed == [vpn, vpn], "TCP or UDP egress"
                retry(check_transport)
                test(True, protocol + " client TLS, DNS, UDP/STUN through VPN")
            stun_ip = client_python("print(stun_address())")
            # A cached DNS reply is legitimate while offline. Probe a fresh
            # name each time and distinguish a resolver failure from an answer.
            fresh_dns = """
def fresh_dns():
    name = os.urandom(12).hex() + ".cloudflare.com"
    question = b"".join(bytes([len(s)]) + s.encode() for s in name.split(".")) + bytes.fromhex("00 0001 0001")
    tx = os.urandom(2)
    packet = udp("1.1.1.1", 53, tx + bytes.fromhex("0100 0001 0000 0000 0000") + question)
    assert packet[:2] == tx and len(packet) >= 12
    return packet[3] & 15
"""
            test(int(client_python(fresh_dns + "\nprint(fresh_dns())")) in (0,3), "fresh external DNS through the active tunnel")
            doh_probe(fresh_dns)
            rpc("tunnel.disconnect")
            expect_exit("explicit stop restores WAN", wan)
            test(client_python("print(stun("+repr(stun_ip)+"))")==wan, "UDP uses direct WAN after stop")
            test(int(client_python(fresh_dns+"\nprint(fresh_dns())")) in (0,3), "normal router DNS after stop")
            time.sleep(4)
            stopped=rpc("status")
            test(not stopped["want_tunnel"] and not stopped["core"]["policy_enabled"] and stopped["core"]["core_state"]==0,"manual stop remains stopped")
            connected(mode, selected)
            rpc("tunnel.restart", id=selected,protocol=mode)
            wan,vpn=exits()
            expect_exit("restart reconnects the selected transport",vpn)
            old_pid=ssh(router,"pidof kittend")
            ssh(router, """ubus call service signal '{"name":"sota","instance":"core","signal":9}' >/dev/null""")
            def recovered():
                status=rpc("status")
                assert status["want_tunnel"] and status["core"]["core_state"]==2
                assert ssh(router,"pidof kittend") != old_pid
                current_wan,current_vpn=exits()
                assert current_wan!=current_vpn and client_python("print(trace())")==current_vpn
            retry(recovered,75)
            test(True, "procd and the controller restore the VPN after a core crash")
            wan, vpn = exits()
            expect_exit("unmatched destination, WAN-list mode -> VPN", vpn)
            policy(direction="wan", rules="1.1.1.0/24")
            expect_exit("manual CIDR, WAN-list mode -> WAN", wan)
            policy()
            expect_exit("unmatched destination, VPN-list mode -> WAN", wan)
            policy(rules="1.1.1.0/24")
            expect_exit("manual CIDR, VPN-list mode -> VPN", vpn)
            policy(rules="1.1.1.0/24", exceptions={**empty, "rules":"1.1.1.1/32"})
            expect_exit("overlapping CIDR exception wins -> WAN", wan)
            policy(files=[{"name":"exact.txt","text":"one.one.one.one\n"}])
            expect_exit("file, exact domain -> VPN", vpn, domain=True)
            policy(rules="one.one.one")
            expect_exit("exact parent excludes its subdomain -> WAN", wan, domain=True)
            policy(rules="*.one.one.one")
            expect_exit("wildcard includes subdomain -> VPN", vpn, domain=True)
            policy(rules="1.1.1.0/24", exceptions={**empty, "rules":"one.one.one.one"})
            expect_exit("exact domain exception overrides CIDR -> WAN", wan, domain=True)
            policy(rules="1.1.1.0/24", exceptions={**empty,"files":[{"name":"direct.txt","text":"*.one.one.one"}]})
            expect_exit("wildcard exception from file overrides CIDR -> WAN", wan, domain=True)
            policy(direction="wan", clients="192.0.2.254/32")
            expect_exit("client outside selection -> WAN", wan)
            policy(rules="192.0.2.60/32", files=[{"name":"fixed.txt","text":"198.51.100.143/32"}],
                   sources=[base+"/routes"], exceptions={**empty,"sources":[base+"/exceptions"]}, interval=1)
            test(all(element(group,ip) for group,ip in [("listed","192.0.2.60"),("listed","198.51.100.141"),
                 ("listed","198.51.100.143"),("bypass","198.51.100.142")]),"manual, file and both HTTP sources combined")
            server.entries = {"/routes":"198.51.100.144/32", "/exceptions":"198.51.100.145/32"}
            def renewed():
                assert element("listed","198.51.100.144") and element("bypass","198.51.100.145")
            retry(renewed, 85)
            test(not element("listed","198.51.100.141") and not element("bypass","198.51.100.142")
                 and element("listed","198.51.100.143"), "scheduled refresh replaces both HTTP lists and retains static file")
            server.entries = {"/exceptions":"198.51.100.146/32"}
            def partially_renewed():
                assert element("bypass","198.51.100.146")
            retry(partially_renewed,85)
            test(element("listed","198.51.100.144") and not element("bypass","198.51.100.145"),
                 "an HTTP 503 retains the last good list while the other source updates")
            current = rpc("network.get")
            for key in ("catalog","russian_default"):
                current.pop(key,None)
            for invalid in ({"rules":"192.0.2.23/99"}, {"revision":"expired"}):
                try:
                    rpc("network.set",network={**current,**invalid})
                except RuntimeError:
                    pass
                else:
                    raise AssertionError("accepted invalid settings")
                assert rpc("network.get")["revision"] == current["revision"]
            test(True, "malformed CIDR and stale revision leave settings intact")
            changed = rpc("network.set",network={**current,"dns":"1.0.0.1"})
            test(changed["dns"] == "1.0.0.1" and int(client_python(fresh_dns + "\nprint(fresh_dns())")) in (0,3),
                 "updated DNS resolver answers client queries")
            ssh(router,"/etc/init.d/firewall reload >/dev/null 2>&1 && /usr/libexec/sota-policy check")
            test(any(e["level"]=="info" for e in rpc("logs")), "firewall reload and event journal")
        finally:
            ssh(router,f"set -e; cp -p {backup}/route-list.txt* /etc/sota/; cp -p {backup}/config /etc/config/sota; /etc/init.d/sota reload")
            restored = retry(lambda: rpc("status"))
            if restored["core"]["core_state"] != 2 or restored["protocol"] != mode:
                rpc("tunnel.disconnect")
                connected(mode,selected)
            test(rpc("network.get")["revision"] == original["revision"] and
                 rpc("status")["hwid"] == starting["hwid"], "original routing revision and HWID restored")
            exits()
            ssh(router, f"rm -f {backup}/route-list.txt* {backup}/config && rmdir {backup}")
            server.shutdown()


if __name__ == "__main__":
    main()

