"""Test the LAN HTTP panel and IP diagnostics without printing IP addresses."""
import ipaddress
import json
from urllib.request import Request, build_opener, ProxyHandler


class Panel:
    def __init__(self, origin="http://192.168.1.88:8080"):
        self.origin = origin
        self.http = build_opener(ProxyHandler({}))

    def get(self, path):
        with self.http.open(self.origin+path,timeout=10) as r:
            return r.read()

    def rpc(self, method, params=None):
        req = Request(self.origin+"/rpc", json.dumps({"jsonrpc":"2.0","id":1,"method":method,
                      "params":params or {}}).encode(), headers={"Origin":self.origin,"Content-Type":"application/json"})
        with self.http.open(req, timeout=40) as response:
            reply = json.load(response)
        if "error" in reply:
            raise RuntimeError(reply["error"]["message"])
        return reply["result"]


if __name__ == "__main__":
    panel = Panel()
    assert "Проверить IP".encode() in panel.get("/")
    assert all(panel.get(path) for path in ("/app.js","/app.css"))
    status = panel.rpc("status")
    assert status["core"]["core_responding"]
    measure = panel.rpc("diagnostics.ip")
    assert ipaddress.IPv4Address(measure["wan"]["ip"]).is_global
    if status["core"]["core_state"] == 0:
        assert measure["tunnel"] == {"ip":None,"status":"disconnected"}
    print("HTTP without login, UI assets, RPC, original core and direct WAN OK")
    print("tunnel measurement:", measure["tunnel"]["status"])
