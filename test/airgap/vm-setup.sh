#!/bin/bash
# The air-gapped gate's setup in the VM, at its second boot, which has no
# network (run-vm.sh, SUITE=airgap): checks that nothing outside is
# reachable, installs the packages from <dir>/rpms (prepare.sh packages),
# then Ollama and the model from the offline mirror (<dir>/mirror), and
# serves the model at 127.0.0.1:11434. It also logs, and drops, every new
# connection an agent opens that leaves the host (kernel log prefix
# "custos-egress agent:"); other processes' are only logged.
#
# Usage: vm-setup.sh <dir>
set -euo pipefail
dir=$1
# shellcheck source=/dev/null
. "$dir/versions"
die() { echo "airgap: $*" >&2; exit 1; }
log() { echo "[airgap] $*"; }

log "the network"
if curl -sS -m 15 -o /dev/null https://download.opensuse.org/ 2>/dev/null; then
	die "download.opensuse.org is reachable: this is not an air-gapped system"
fi
if getent hosts download.opensuse.org >/dev/null 2>&1; then
	log "  names resolve, but nothing answers"
fi
log "  nothing outside is reachable"
nft -f - <<'NFT' && log "  egress logged (custos-egress)" || log "  egress not logged: nft refused the rules"
table inet custos_airgap {
	chain out {
		type filter hook output priority 0; policy accept;
		oifname "lo" accept
		ct state established,related accept
		socket cgroupv2 level 2 "system.slice/system-agent.slice" log prefix "custos-egress agent: " drop
		log prefix "custos-egress other: "
	}
}
NFT

log "the packages, from $dir/rpms"
bash "$dir/prepare.sh" "$dir" packages

log "Ollama $OLLAMA_VERSION and $MODEL, from the mirror"
tar -C /usr/local -xf "$dir/mirror/ollama.tar"
restorecon -R /usr/local/bin/ollama /usr/local/lib/ollama 2>/dev/null || true
mkdir -p /var/lib/ollama/models
tar -C /var/lib/ollama/models -xf "$dir/mirror/models.tar"
# The agents (agent_t) may connect to HTTP ports only: Ollama's is one
# once labelled (user guide, chapter 5).
semanage port -a -t http_port_t -p tcp 11434 2>/dev/null || semanage port -m -t http_port_t -p tcp 11434
systemctl stop custos-ollama.service >/dev/null 2>&1 || true
systemd-run --unit=custos-ollama -p Environment=OLLAMA_MODELS=/var/lib/ollama/models \
	-p Environment=OLLAMA_HOST=127.0.0.1:11434 /usr/local/bin/ollama serve >/dev/null
for _ in $(seq 60); do curl -sf http://127.0.0.1:11434/api/version >/dev/null && break; sleep 1; done
curl -sf http://127.0.0.1:11434/v1/models | grep -q "\"$MODEL\"" || die "Ollama does not serve $MODEL"
log "  $(/usr/local/bin/ollama --version 2>&1 | tail -1); serving $MODEL"
# The first answer loads the model; later ones are faster.
curl -sf -m 300 http://127.0.0.1:11434/v1/chat/completions \
	-d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Say ok. /no_think\"}]}" >/dev/null ||
	die "the model does not answer"
log "  the model answers"
