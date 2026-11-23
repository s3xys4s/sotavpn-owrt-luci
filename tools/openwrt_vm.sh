#!/bin/sh
# Two private KVM guests. Socket Ethernet joins the router LAN to its test client.
set -eu
dir=/var/lib/sota-wrt-lab
cd "$dir"
hostuser=sota-wrt-vm
id "$hostuser" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$hostuser"
version=openwrt-25.12.6-armsr-armv8-generic
test -r "$version-kernel.bin" && test -r "$version-ext4-rootfs.img.gz"
test -f lab_ed25519 || ssh-keygen -q -t ed25519 -N '' -f lab_ed25519
for role in router client; do
    disk="$dir/$role.ext4"
    if ! test -e "$disk"; then
        candidate=$(mktemp "$dir/$role.XXXXXX")
        gzip -dc "$version-ext4-rootfs.img.gz" > "$candidate"
        truncate -s 768M "$candidate"
        resize2fs "$candidate" >/dev/null
        seed=$(mktemp)
        cat > "$seed" <<'EOF'
#!/bin/sh
uci -q delete network.wan6
uci set network.lan=interface
uci set network.lan.device=eth0
uci set network.lan.proto=static
uci set network.lan.ipaddr=192.0.2.1
uci set network.lan.netmask=255.255.255.0
uci set network.wan=interface
uci set network.wan.device=eth1
uci set network.wan.proto=dhcp
uci -q delete network.lan.ip6assign
uci set firewall.sota_lab_admin=rule
uci set firewall.sota_lab_admin.src=wan
uci set firewall.sota_lab_admin.src_ip=10.0.2.2
uci set firewall.sota_lab_admin.proto=tcp
uci set firewall.sota_lab_admin.dest_port='22 80'
uci set firewall.sota_lab_admin.target=ACCEPT
EOF
        if [ "$role" = client ]; then
            cat >> "$seed" <<'EOF'
uci set network.lan.proto=dhcp
uci -q delete network.lan.ipaddr
uci -q delete network.lan.netmask
uci set network.wan.proto=static
uci set network.wan.ipaddr=10.0.2.15
uci set network.wan.netmask=255.255.255.0
uci set dhcp.lan.ignore=1
/etc/init.d/dnsmasq disable
ln -sf /tmp/resolv.conf.d/resolv.conf.auto /etc/resolv.conf
EOF
        fi
        echo 'uci commit; exit 0' >> "$seed"
        debugfs -w -R "write $seed /etc/uci-defaults/zz-sota-lab" "$candidate" >/dev/null
        debugfs -w -R 'mkdir /etc/dropbear' "$candidate" 2>/dev/null || :
        debugfs -w -R "write $dir/lab_ed25519.pub /etc/dropbear/authorized_keys" "$candidate" >/dev/null
        rm -f "$seed"
        chown "$hostuser" "$candidate"
        mv "$candidate" "$disk"
    fi
    systemctl is-active --quiet "sota-wrt-$role" && continue
    systemctl reset-failed "sota-wrt-$role" 2>/dev/null || :
    case "$role" in
        router) lan=listen=127.0.0.1:18239; forwards='hostfwd=tcp:127.0.0.1:22240-:22,hostfwd=tcp:127.0.0.1:18081-:80'; mac=01 ;;
        client) lan=connect=127.0.0.1:18239; forwards='hostfwd=tcp:127.0.0.1:22241-:22'; mac=02 ;;
    esac
    systemd-run --unit="sota-wrt-$role" --property=User="$hostuser" --property=SupplementaryGroups=kvm --property=CPUAffinity=0 \
        --property=NoNewPrivileges=yes --property=ProtectHome=yes --property=PrivateTmp=yes \
        /usr/bin/qemu-system-aarch64 -machine virt,accel=kvm,gic-version=host -cpu host -smp 1 -m 512 \
        -display none -monitor none -serial stdio -no-reboot -kernel "$dir/$version-kernel.bin" \
        -append 'console=ttyAMA0 root=/dev/vda rootwait rw' -drive "file=$disk,format=raw,if=virtio" \
        -netdev "socket,id=lan,$lan" -device "virtio-net-pci,netdev=lan,mac=52:54:00:a1:01:$mac" \
        -netdev "user,id=mgmt,$forwards" -device "virtio-net-pci,netdev=mgmt,mac=52:54:00:a1:02:$mac"
    if [ "$role" = router ]; then sleep 1; fi
done
