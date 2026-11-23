"""Exercise the installed DoH resolver in a disposable mount and network namespace.

Run: unshare --net --mount --pid --fork --mount-proc python3 openwrt_doh_regression.py ROOTFS
"""
import base64
import contextlib
import http.server
import os
import signal
import socket
import ssl
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path
from urllib.parse import urlsplit,parse_qs

root=Path(sys.argv[1]).resolve()
bootstrap_names=[]
https_names=[]


def question(data):
    offset=12
    labels=[]
    while data[offset]:
        size=data[offset]
        labels.append(data[offset+1:offset+1+size].decode())
        offset+=size+1
    return ".".join(labels),data[12:offset+5]


def response(data,ip):
    name,q=question(data)
    has_a=q[-4:]==bytes.fromhex("00010001")
    answer=bytes.fromhex("c00c000100010000001e0004")+socket.inet_aton(ip) if has_a else b""
    return data[:2]+struct.pack("!HHHHH",0x8180,1,int(has_a),0,0)+q+answer


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version="HTTP/1.1"
    def send_answer(self,data):
        https_names.append(question(data)[0])
        msg=response(data,"198.51.100.77")
        self.send_response(200)
        self.send_header("Content-Type","application/dns-message")
        self.send_header("Content-Length",str(len(msg)))
        self.end_headers()
        self.wfile.write(msg)
    def do_POST(self):
        self.send_answer(self.rfile.read(int(self.headers["Content-Length"])))
    def do_GET(self):
        self.send_answer(base64.urlsafe_b64decode(parse_qs(urlsplit(self.path).query)["dns"][0]+"==="))
    def log_message(self,*_): pass


def bootstrap(sock):
    while True:
        data,peer=sock.recvfrom(4096)
        name,_=question(data)
        bootstrap_names.append(name)
        sock.sendto(response(data,"127.0.0.3"),peer)


def lookup(name):
    with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as s:
        s.settimeout(2)
        labels=b"".join(bytes([len(p)])+p.encode() for p in name.split("."))
        q=os.urandom(2)+bytes.fromhex("01000001000000000000")+labels+bytes.fromhex("0000010001")
        s.sendto(q,("127.0.0.1",1056))
        reply=s.recv(2048)
        return reply[:2]==q[:2] and socket.inet_aton("198.51.100.77") in reply


@contextlib.contextmanager
def resolver(host):
    script="""
. /etc/init.d/sota
procd_open_instance(){ :; }
procd_set_param(){ if [ "$1" = command ]; then shift; exec "$@"; fi; }
procd_close_instance(){ :; }
start_doh_resolver "https://$1/dns-query" 127.0.0.2
wait
"""
    child=subprocess.Popen(["chroot",str(root),"/bin/sh","-c",script,"sh",host],start_new_session=True)
    try: yield
    finally:
        if child.poll() is None: os.killpg(child.pid,signal.SIGTERM)
        child.wait(timeout=6)


def main():
    assert os.getpid()==1 and (root/"etc/init.d/sota").is_file()
    subprocess.run(["mount","--make-rprivate","/"],check=True)
    subprocess.run(["ip","link","set","lo","up"],check=True)
    (root/"tmp/run/sota").mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory() as work, socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as dns:
        pem=Path(work)/"cert"; key=Path(work)/"key"
        subprocess.run(["openssl","req","-x509","-newkey","rsa:2048","-nodes","-days","1","-subj","/CN=resolver.sota.test","-addext","subjectAltName=DNS:resolver.sota.test","-keyout",str(key),"-out",str(pem)],check=True,stderr=subprocess.DEVNULL)
        ca=str(root/"etc/ssl/certs/ca-certificates.crt")
        subprocess.run(["mount","--bind",str(pem),ca],check=True)
        dns.bind(("127.0.0.2",53))
        threading.Thread(target=bootstrap,args=(dns,),daemon=True).start()
        with http.server.ThreadingHTTPServer(("127.0.0.3",443),Handler) as server:
            tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(str(pem),str(key))
            server.socket=tls.wrap_socket(server.socket,server_side=True)
            threading.Thread(target=server.serve_forever,daemon=True).start()
            with resolver("resolver.sota.test"):
                for _ in range(18):
                    try:
                        if lookup("valid.sota.test"): break
                    except OSError: pass
                    time.sleep(.5)
                else: raise RuntimeError("no authenticated DoH answer")
            assert "valid.sota.test" in https_names and "valid.sota.test" not in bootstrap_names
            with resolver("mismatch.sota.test"):
                for _ in range(8):
                    try: assert not lookup("negative.sota.test"),"accepted certificate for another host"
                    except OSError: pass
                    time.sleep(.5)
            assert "mismatch.sota.test" in bootstrap_names
            assert "negative.sota.test" not in https_names+bootstrap_names
            server.shutdown()
        subprocess.run(["umount",ca],check=True)
    print("PASS DoH wire response, bootstrap isolation, certificate hostname rejection and no plaintext fallback")


if __name__=="__main__": main()

