"""Exercise a full reboot of the dedicated QEMU router/client pair, using their existing disks."""
import json
import subprocess
import time

from openwrt_regression import client, client_python, connected, exits, lab, retry, router, rpc, ssh, test


def boot_id():
    return ssh(router, "cat /proc/sys/kernel/random/boot_id")


def reboot_pair():
    previous = boot_id()
    # The QEMU LAN uses a connected socket. Restart the client with the router
    # so that its virtual Ethernet cable is reconnected to the new QEMU process.
    for port, action in ((client, "poweroff"), (router, "reboot")):
        try:
            ssh(port, "sync && " + action)
        except subprocess.CalledProcessError:
            pass

    def both_down():
        for unit in ("sota-wrt-router", "sota-wrt-client"):
            result = subprocess.run(["systemctl", "is-active", unit], capture_output=True, text=True)
            assert result.stdout.strip() in ("inactive", "failed", "unknown"), unit

    retry(both_down, 90)
    subprocess.run(["sh", str(lab / "openwrt_vm.sh")], check=True)
    def ready():
        assert previous != boot_id()
        assert ssh(client, "ip -4 route show default | grep 'via 192.0.2.1'")
        assert rpc("status")["has_key"]
    retry(ready, 120)


def active(selected, mode):
    def check():
        state = rpc("status")
        assert state["selected"] == selected and state["protocol"] == mode
        assert state["want_tunnel"] and state["core"]["core_state"] == 2
        assert ssh(router, "/usr/libexec/sota-policy check && ip -4 route show table 21519 | grep 'dev sota0'")
        exits()
    retry(check, 100)


def ordinary_lan(wan):
    def check():
        rules = ssh(router, "ip -4 rule show")
        assert "lookup 21519" not in rules and "8999:" not in rules
        assert ssh(router, "if nft list table inet sota_guard >/dev/null 2>&1 || ip link show sota0 >/dev/null 2>&1; then echo active; fi") == ""
        assert ssh(router, "netstat -ln | grep -E ':(1053|1054|1055|1056) ' || true") == ""
        assert json.loads(client_python("print(json.dumps([trace(),stun(stun_address())]))")) == [wan, wan]
    retry(check, 35)


def main():
    starting = rpc("status")
    assert starting["core"]["core_state"] == 2 and starting["want_tunnel"]
    selected, mode = starting["selected"], starting["protocol"]
    revision = rpc("network.get")["revision"]
    wan, _ = exits()
    ssh(router, "/etc/init.d/sota enabled && /etc/init.d/sota stop")
    ordinary_lan(wan)
    test(True, "stopping procd removes Sota routes, rules, TUN and DNS listeners and restores WAN")
    ssh(router, "/etc/init.d/sota start")
    active(selected, mode)
    test(True, "starting procd reconnects the saved server and transport")
    reboot_pair()
    active(selected, mode)
    test(True, "active tunnel automatically returns after a full OpenWrt reboot")
    rpc("tunnel.disconnect")
    ordinary_lan(wan)
    reboot_pair()
    state = rpc("status")
    assert not state["want_tunnel"] and not state["core"]["policy_enabled"] and state["core"]["core_state"] == 0
    ordinary_lan(wan)
    test(True, "manual Stop survives a full reboot, WAN TCP, UDP and DNS remain available")
    ssh(router, "/etc/init.d/sota stop")
    result = json.loads(ssh(router, """ubus call sota connect '{"id":0,"protocol":""}'"""))
    assert "error" not in result
    active(selected, mode)
    test(True, "LuCI RPC starts an inactive procd service with the saved selection")
    assert rpc("network.get")["revision"] == revision and rpc("status")["hwid"] == starting["hwid"]
    test(True, "original settings and HWID survived both reboots")


if __name__ == "__main__":
    main()
