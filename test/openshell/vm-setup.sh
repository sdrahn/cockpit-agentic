#!/bin/bash
# Sets up NVIDIA OpenShell in the Leap 16 test VM (test/vm/run-vm.sh with
# SUITE=openshell) as its RPM intends: rootless Podman, the gateway a
# systemd user service of an ordinary user (osh, lingering), and that
# gateway registered for root too, who runs agent-admin and the relays
# (CONTRACT.md §16a). Run as root, after test/vm/prepare.sh.
set -euo pipefail
user=osh
log() { echo "[vm-setup] $*"; }

log "Podman"
zypper -n in --no-recommends podman curl jq audit >/tmp/zypper-osh.log 2>&1 ||
	{ tail -n 30 /tmp/zypper-osh.log; exit 1; }

log "user $user (rootless Podman, lingering)"
id "$user" >/dev/null 2>&1 || useradd -m "$user"
grep -q "^$user:" /etc/subuid || usermod --add-subuids 200000-265535 --add-subgids 200000-265535 "$user"
loginctl enable-linger "$user"
uid=$(id -u "$user")
for _ in $(seq 60); do [ -d "/run/user/$uid" ] && break; sleep 1; done
[ -d "/run/user/$uid" ] || { echo "no user manager for $user" >&2; exit 1; }

log "OpenShell (its installer, for $user)"
curl -LsSf https://raw.githubusercontent.com/NVIDIA/OpenShell/main/install.sh -o /tmp/openshell-install.sh
SUDO_USER=$user sh /tmp/openshell-install.sh
openshell --version

log "the gateway registration for root"
mkdir -p /root/.config
rm -rf /root/.config/openshell
cp -a "/home/$user/.config/openshell" /root/.config/
openshell gateway list
openshell status
log "SELinux: $(getenforce)"
