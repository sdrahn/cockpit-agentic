#!/bin/bash
# The OpenShell backend (CONTRACT.md §3a, §16a) with a real NVIDIA
# OpenShell: an agent created with agent-admin runs in an OpenShell
# sandbox, reaches a gateway on the host over HTTP with a token that only
# OpenShell's supervisor holds, and is supervised through its relayed
# socket. CI runs it on a GitHub runner with Docker (job openshell); it
# needs root, systemd, Docker, the openshell CLI with a local gateway, and
# the binaries from `make build build-tests`.
#
# Usage: test/openshell/run-spike.sh --disposable [--results DIR]
#
# The gateway is test/vm/fakegateway: it serves the tools, holds the
# approval of agentic-test__change, and refuses requests without the real
# token. mcp-gateway's own HTTPS listener with OAuth is the next step
# (open item O10).
set -u
here=$(cd "$(dirname "$0")/../.." && pwd)
disposable= results=
while [ $# -gt 0 ]; do
	case $1 in
	--disposable) disposable=1 ;;
	--results) results=${2:?}; shift ;;
	*) echo "usage: $0 --disposable [--results DIR]" >&2; exit 2 ;;
	esac
	shift
done
[ -n "$disposable" ] || { echo "$0: installs agents, units and users on this system; use --disposable" >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo "$0: runs as root" >&2; exit 2; }
work=$(mktemp -d)
t0=$(date +%s)
failed=0
results_list=()
current_section=start
pass() { echo "PASS: $*"; results_list+=("$current_section"$'\t'pass$'\t'"$*"); }
fail() { echo "FAIL: $*"; failed=1; results_list+=("$current_section"$'\t'fail$'\t'"$*"); }
skip() { echo "SKIP: $*"; results_list+=("$current_section"$'\t'skip$'\t'"$*"); }
check() { local d=$1; shift; if "$@"; then pass "$d"; else fail "$d"; fi; }
section() { printf '\n=== %s\n' "$*"; current_section=$*; }
eventually() { local deadline=$((SECONDS + $1)); shift; until "$@"; do [ "$SECONDS" -ge "$deadline" ] && return 1; sleep 1; done; }
not() { ! "$@"; }
json_ok() { jq -e "$2" >/dev/null 2>&1 <<<"$1"; }
api() { local p=$1; shift; curl -sS --max-time 180 --unix-socket /run/agents/sb/api.sock "http://agent$p" "$@"; }
admin() { curl -sS --max-time 10 "http://127.0.0.1:18444$1" "${@:2}"; }
run_activity() { api /v1/activities -X POST -d "$(jq -nc --arg p "$1" '{prompt: $p, wait: true}')"; }
summary_has() { grep -qF -- "$2" <<<"$(jq -r .summary <<<"$1")"; }
journal_has() { journalctl -o json --no-pager --since "@$t0" AGENT_NAME=sb "AGENT_EVENT=$1" 2>/dev/null | jq -e "select($2)" >/dev/null 2>&1; }
osh() { openshell -g local "$@"; }

section Preconditions
check "openshell is installed" command -v openshell
check "the local OpenShell gateway answers" eventually 60 osh status
check "docker runs" docker info -q

section Install
# What the package installs (packaging/suse), into this system.
make -s -C "$here" install install-tests DESTDIR= PREFIX=/usr SYSCONFDIR=/etc UNITDIR=/etc/systemd/system \
	SYSUSERSDIR=/usr/lib/sysusers.d TMPFILESDIR=/usr/lib/tmpfiles.d SBINDIR=/usr/sbin >/dev/null
getent group mcp-users >/dev/null || groupadd -r mcp-users
systemd-sysusers /usr/lib/sysusers.d/cockpit-agentic.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/cockpit-agentic.conf
systemctl daemon-reload
check "agent-admin is installed" command -v agent-admin
check "the relay is installed" test -x /usr/libexec/cockpit-agentic/agent-openshell-relay

section "Agent image"
img=localhost/cockpit-agentic-agent:ci
check "the agent image builds" docker build -q -t "$img" -f "$here/packaging/openshell/Containerfile" "$here"

section "Model and gateway on the host"
head -c 24 /dev/urandom | base64 >"$work/token"
systemd-run --unit=osh-fakemodel /usr/libexec/cockpit-agentic/tests/fakemodel --listen 0.0.0.0:8008 >/dev/null
systemd-run --unit=osh-fakegateway /usr/libexec/cockpit-agentic/tests/fakegateway --listen 0.0.0.0:18443 --admin 127.0.0.1:18444 --token-file "$work/token" >/dev/null
check "the fake gateway answers" eventually 20 bash -c 'curl -s -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:18443/mcp | grep -q 401'

section "Creating the agent"
cat >"$work/sb.yaml" <<EOF
version: 1
name: sb
description: OpenShell spike agent
model: {provider: openai, name: fake, endpoint: "http://127.0.0.1:8008/v1"}
instruction: You are a test agent.
gateway: {url: "http://127.0.0.1:18443/mcp", servers: [agentic-test]}
limits: {max_invocation_seconds: 300}
interaction: {agui: true, client_tools: deny}
sandbox: {kind: openshell, openshell_gateway: local}
EOF
agent-admin delete sb >/dev/null 2>&1
export AGENTIC_OPENSHELL_IMAGE=$img
check "agent-admin create sb (openshell)" agent-admin create sb --from "$work/sb.yaml"
check "the sandbox policy is written" test -s /etc/cockpit-agentic/agents/sb/openshell-policy.yaml
check "the provider profile is imported" eventually 10 bash -c 'openshell -g local profile list -o json 2>/dev/null | grep -q cockpit-agentic-gateway-sb'
check "agent-admin set-credential sb gateway-token (creates the sandbox)" agent-admin set-credential sb gateway-token <"$work/token"
check "the sandbox exists" eventually 120 bash -c 'openshell -g local sandbox get agent-sb >/dev/null 2>&1'
check "the relay socket is active" systemctl is-active -q agent-openshell@sb.socket

section "Supervising through the relay"
check "the agent answers through the relay" eventually 180 bash -c 'curl -sf --max-time 10 --unix-socket /run/agents/sb/api.sock http://agent/v1/status >/dev/null'
st=$(api /v1/status)
check "status through the relay says lifecycle openshell" json_ok "$st" '.lifecycle.kind == "openshell" and .lifecycle.sandbox == "agent-sb"'
check "the relay passed root on (status is for supervisors)" json_ok "$st" '.contract == 1'
a=$(run_activity PROBE)
check "a read-only call runs" summary_has "$a" "probe ok"
tok=$(admin /tokens)
check "the gateway saw the real token" json_ok "$tok" '.Right > 0'
check "and never the placeholder or no token" json_ok "$tok" '.Wrong == 0'
check "the sandbox's environment has no real token" bash -c "! openshell -g local sandbox exec -n agent-sb -- env 2>/dev/null | grep -qF \"\$(cat $work/token)\""

section "Approval"
id=$(api /v1/activities -X POST -d '{"prompt":"CHANGE"}' | jq -r .id)
check "the change waits for an approval" eventually 60 bash -c "curl -s --unix-socket /run/agents/sb/api.sock http://agent/v1/activities/$id | jq -e '.state == \"waiting_approval\"' >/dev/null"
check "nothing ran before the decision" json_ok "$(admin /executed)" 'index("agentic-test__change") == null'
admin /approve/a-osh-1 -X POST >/dev/null
check "the activity finishes after the approval" eventually 60 bash -c "curl -s --unix-socket /run/agents/sb/api.sock http://agent/v1/activities/$id | jq -e '.state == \"finished\" and (.summary | contains(\"changed test\"))' >/dev/null"
check "journal (from OpenShell's log stream): approval.pending" eventually 60 journal_has approval.pending '.AGENT_APPROVAL_ID == "a-osh-1"'
check "journal: approval.resolved approved" eventually 60 journal_has approval.resolved '.AGENT_OUTCOME == "approved"'

section "AG-UI through the relay"
out=$(api /agui -N -X POST -d '{"threadId":"osh-1","messages":[{"role":"user","content":"PROBE"}]}')
check "the stream comes through the relay" grep -q '"type":"RUN_FINISHED"' <<<"$out"

section "Emergency stop"
check "agent-admin stop-all" agent-admin stop-all
check "the relay is stopped" not systemctl is-active -q agent-openshell@sb.socket
check "the sandbox is stopped" eventually 60 bash -c 'openshell -g local sandbox get agent-sb -o json 2>/dev/null | jq -e "(.status // .state // \"\") | ascii_downcase | test(\"stop|exit|terminat\")" >/dev/null'
check "agent-admin release-all" agent-admin release-all
check "the relay is back" systemctl is-active -q agent-openshell@sb.socket

section Removing
check "agent-admin delete sb" agent-admin delete sb
check "the sandbox is gone" eventually 60 not osh sandbox get agent-sb
systemctl stop osh-fakemodel osh-fakegateway >/dev/null 2>&1

if [ -n "$results" ]; then
	mkdir -p "$results"
	n=0
	{
		echo "1..${#results_list[@]}"
		for r in "${results_list[@]}"; do
			IFS=$'\t' read -r sec st desc <<<"$r"
			n=$((n + 1))
			case $st in pass) echo "ok $n - $sec: $desc" ;; skip) echo "ok $n - $sec: $desc # SKIP" ;; *) echo "not ok $n - $sec: $desc" ;; esac
		done
	} >"$results/results.tap"
	journalctl --no-pager --since "@$t0" -u 'agent-openshell@*' -o short-precise >"$results/relay.log" 2>&1
	openshell -g local logs agent-sb >"$results/sandbox.log" 2>&1 || true
fi
n_fail=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' '$2 == "fail"' | wc -l)
printf '\n%d checks, %d failed\n' "${#results_list[@]}" "$n_fail"
exit "$failed"
