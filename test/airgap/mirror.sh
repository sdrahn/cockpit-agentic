#!/bin/bash
# Builds the air-gapped gate's offline mirror, on a machine with network
# (the CI runner): Ollama (test/airgap/versions) without its GPU
# libraries, and the model in Ollama's own store, as a site would put
# them on its media. run-vm.sh (SUITE=airgap) copies it into a VM that
# has no network.
#
# Usage: mirror.sh <dir>; leaves <dir>/ollama.tar and <dir>/models.tar.
# Needs sudo (Ollama's installer) and curl.
set -euo pipefail
dir=$(mkdir -p "$1" && cd "$1" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=/dev/null
. "$here/versions"
log() { echo "[mirror] $*"; }

if [ -f "$dir/ollama.tar" ] && [ -f "$dir/models.tar" ]; then
	log "using the mirror in $dir (cached)"
else
	log "Ollama $OLLAMA_VERSION"
	curl -fsSL --retry 3 https://ollama.com/install.sh -o "$dir/install.sh"
	OLLAMA_VERSION=$OLLAMA_VERSION sh "$dir/install.sh"
	sudo systemctl disable --now ollama.service >/dev/null 2>&1 || true
	/usr/local/bin/ollama --version
	# The CPU runtime only: the CUDA and ROCm libraries are gigabytes the
	# VM does not use.
	tar -C /usr/local -cf "$dir/ollama.tar" --exclude='lib/ollama/cuda*' --exclude='lib/ollama/rocm*' bin/ollama lib/ollama
	log "the model $MODEL"
	models=$(mktemp -d)
	OLLAMA_MODELS=$models OLLAMA_HOST=127.0.0.1:11435 /usr/local/bin/ollama serve >"$dir/serve.log" 2>&1 &
	serve=$!
	for _ in $(seq 30); do curl -sf http://127.0.0.1:11435/api/version >/dev/null && break; sleep 1; done
	for attempt in 1 2 3; do
		OLLAMA_HOST=127.0.0.1:11435 /usr/local/bin/ollama pull "$MODEL" && break
		[ "$attempt" = 3 ] && { kill "$serve"; exit 1; }
		sleep 20
	done
	kill "$serve"
	tar -C "$models" -cf "$dir/models.tar" .
	rm -rf "$models" "$dir/install.sh"
fi

name=${MODEL%%:*} tag=${MODEL#*:}
digest=$(tar -xOf "$dir/models.tar" "./manifests/registry.ollama.ai/library/$name/$tag" | sha256sum | cut -d' ' -f1)
log "$MODEL: manifest sha256 $digest; $(du -h "$dir/models.tar" | cut -f1) of model, $(du -h "$dir/ollama.tar" | cut -f1) of Ollama"
if [ -n "$MODEL_MANIFEST" ] && [ "$digest" != "$MODEL_MANIFEST" ]; then
	echo "mirror: $MODEL is not the tested model: manifest $digest, test/airgap/versions says $MODEL_MANIFEST" >&2
	exit 1
fi
[ -n "$MODEL_MANIFEST" ] || log "MODEL_MANIFEST is not set in test/airgap/versions: set it to $digest"
