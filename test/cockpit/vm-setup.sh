#!/bin/bash
# Sets up the test system for the Cockpit page's test against a real
# Cockpit (test/cockpit/vm-test.js, run by test/vm/run-vm.sh with
# SUITE=cockpit), as root, after test/vm/prepare.sh installed the
# packages: Cockpit and custos-cockpit, the scripted model and the test
# MCP server of custos-tests, a person, and agents with activities.
#
#   carol      a person in agent-clients (sees the agents), wheel (the
#              gateway's admin role, so she may approve; sudo for
#              Cockpit's administrative access) and systemd-journal
#   triage     delegates to patch-pilot (its schedule morning-check)
#   patch-pilot  supervised: probe runs, change waits for an approval
#   disk-watch   bounded: its change is autonomous
#
# Then: a finished activity, an autonomous change, a change declined by
# root, and triage's scheduled run, which delegates to patch-pilot, whose
# change waits for an approval. The test decides it in the page.
#
# Usage: vm-setup.sh RPMDIR PASSWORD; prints KEY=VALUE lines the test reads.
set -euo pipefail
rpms=${1:?usage: vm-setup.sh RPMDIR PASSWORD}
password=${2:?usage: vm-setup.sh RPMDIR PASSWORD}
tests=/usr/libexec/custos/tests
work=/root/custos-cockpit
mkdir -p "$work"
die() { echo "vm-setup: $*" >&2; exit 1; }
eventually() {
	local deadline=$((SECONDS + $1))
	shift
	until "$@"; do
		[ "$SECONDS" -ge "$deadline" ] && return 1
		sleep 1
	done
}
api() { local a=$1 p=$2; shift 2; curl -sS --max-time 60 --unix-socket "/run/agents/$a/api.sock" "http://agent$p" "$@"; }
gw() { local p=$1; shift; curl -sS --max-time 30 --unix-socket /run/mcp-gateway/control.sock "http://localhost$p" "$@"; }
start() { api "$1" /v1/activities -X POST -d "$(jq -nc --arg p "$2" '{prompt: $p}')" | jq -r .id; }
state_is() { [ "$(api "$1" "/v1/activities/$2" | jq -r .state)" = "$3" ]; }
approval_of() { api "$1" "/v1/activities/$2" | jq -r '[.calls[] | select(.approval_id != null and .approval_id != "")][0].approval_id // empty'; }

echo "vm-setup: Cockpit and the page" >&2
# sudo: Cockpit's administrative access (the minimal image may lack it).
zypper -n --gpg-auto-import-keys in --no-recommends cockpit sudo >/tmp/zypper-cockpit.log 2>&1 ||
	{ tail -n 20 /tmp/zypper-cockpit.log >&2; die "installing cockpit failed"; }
zypper -n --gpg-auto-import-keys in --no-recommends --allow-unsigned-rpm "$rpms"/custos-cockpit-*.rpm >/tmp/zypper-page.log 2>&1 ||
	{ tail -n 20 /tmp/zypper-page.log >&2; die "installing custos-cockpit failed"; }
test -f /usr/share/cockpit/custos/manifest.json || die "the page is not installed"
systemctl enable --now cockpit.socket >/dev/null
eventually 30 bash -c 'curl -skf https://127.0.0.1:9090/ >/dev/null' || die "cockpit does not answer on 9090"

echo "vm-setup: the person" >&2
getent passwd carol >/dev/null || useradd -m -c "Carol (approver)" carol
echo "carol:$password" | chpasswd
usermod -aG agent-clients,wheel,systemd-journal,mcp-users carol
# Cockpit's administrative access is sudo with carol's own password (SUSE
# asks for root's by default: targetpw).
printf 'Defaults:carol !targetpw\ncarol ALL=(ALL) ALL\n' >/etc/sudoers.d/90-custos-carol
chmod 0440 /etc/sudoers.d/90-custos-carol
# Only our file: visudo -c also judges the distribution's /usr/etc/sudoers
# (whose mode it finds wrong on Leap 16), which is not ours to change.
visudo -cf /etc/sudoers.d/90-custos-carol >/tmp/visudo.log 2>&1 || { cat /tmp/visudo.log >&2; die "sudoers"; }

echo "vm-setup: the gateway's test servers and the model" >&2
systemctl enable --now mcp-gateway.service >/dev/null 2>&1
eventually 30 test -S /run/mcp-gateway/mcp.sock || die "the gateway is not running"
systemctl stop custos-fakemodel.service >/dev/null 2>&1 || true
systemd-run --unit=custos-fakemodel --property=DynamicUser=yes "$tests/fakemodel" --listen 127.0.0.1:8008 >/dev/null
printf '%s\n' '# Custos Cockpit test (test/cockpit/vm-setup.sh)' 'version: 1' 'name: custos-test' \
	"command: [\"$tests/testsrv\"]" 'run_as: principal' >/etc/mcp-gateway/servers.d/custos-test.yaml
cp /usr/share/custos/gateway/servers.d/agents.yaml /etc/mcp-gateway/servers.d/agents.yaml
eventually 40 bash -c "curl -s --unix-socket /run/mcp-gateway/control.sock http://localhost/v1/servers | grep -q custos-test &&
	curl -s --unix-socket /run/mcp-gateway/control.sock http://localhost/v1/servers | grep -q '\"agents\"'" ||
	die "the gateway does not take the test servers"

echo "vm-setup: the agents" >&2
agent() { # agent <name> <description> <servers> [yaml]
	cat >"$work/$1.yaml" <<EOF
version: 1
name: $1
description: $2
model: {provider: openai, name: qwen3-32b, endpoint: "http://127.0.0.1:8008/v1"}
instruction: You look before you change anything.
gateway: {servers: [$3]}
limits: {max_invocation_seconds: 600}
interaction: {agui: true, client_tools: deny}
${4:-}
EOF
	agent-admin delete "$1" >/dev/null 2>&1 || true
	agent-admin create "$1" --from - <"$work/$1.yaml" >/dev/null
}
agent patch-pilot "Keeps the host patched within the maintenance window." custos-test
# change is in the task category patch-errata (§6a): supervised until
# promoted, and its proposals count for the Agents tab's readiness.
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change, category: patch-errata}' |
	agent-admin permissions patch-pilot >/dev/null
agent disk-watch "Frees space when /var fills up." custos-test
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change, autonomous: true}' |
	agent-admin permissions disk-watch >/dev/null
agent-admin mode disk-watch bounded >/dev/null
agent triage "Looks at the host every morning and hands work to the other agents." agents \
	'schedules: [{name: morning-check, on_calendar: "*-*-* 06:00", prompt: "DELEGATE patch-pilot CHANGE"}]'
printf '%s\n' "- {server: agents, tool: delegate, read_only: true, args: {agent: '^patch-pilot\$'}}" |
	agent-admin permissions triage >/dev/null
for a in patch-pilot disk-watch triage; do
	eventually 30 test -S "/run/agents/$a/api.sock" || die "agent $a has no socket"
done

echo "vm-setup: activities" >&2
probe=$(start patch-pilot "PROBE the pending patches")
eventually 60 state_is patch-pilot "$probe" finished || die "patch-pilot's probe did not finish"
auto=$(start disk-watch "CHANGE: free the package cache")
eventually 60 state_is disk-watch "$auto" finished || die "disk-watch's autonomous change did not finish"
declined=$(start patch-pilot "CHANGE the kernel parameters")
eventually 60 state_is patch-pilot "$declined" waiting_approval || die "the change to decline does not wait"
gw "/v1/approvals/$(approval_of patch-pilot "$declined")" -X POST -d '{"decision":"deny","scope":"once"}' >/dev/null
eventually 60 state_is patch-pilot "$declined" finished || die "the declined change did not end"
# triage's schedule, as its timer starts it, delegates to patch-pilot:
# that change waits.
systemctl start --no-block agent-schedule@triage_morning-check.service
waiting=
for _ in $(seq 90); do
	waiting=$(api patch-pilot /v1/activities | jq -r '[.activities[] | select(.state == "waiting_approval" and .origin.kind == "delegation")][0].id // empty')
	[ -n "$waiting" ] && break
	sleep 1
done
[ -n "$waiting" ] || die "triage's delegated change does not wait for an approval"
approval=$(approval_of patch-pilot "$waiting")
[ -n "$approval" ] || die "the waiting change has no approval id"

# A second host (#112): no Custos launcher, the contract alone; root's
# activity on it runs until it is cancelled.
systemctl stop custos-second-host-mock.service >/dev/null 2>&1 || true
systemd-run --quiet --unit=custos-second-host-mock.service "$tests/secondhost" -name mock -service custos-second-host-mock.service
eventually 10 test -S /run/agents/mock/api.sock || die "the second host has no socket"
api mock /v1/activities -X POST -d '{"prompt":"hello from the second host","wait":true}' >/dev/null
mockrun=$(api mock /v1/activities -X POST -d '{"prompt":"WAIT for the page"}' | jq -r .id)
[ -n "$mockrun" ] || die "the second host started no activity"

echo "MOCK_ACTIVITY=$mockrun"
echo "WAITING_ACTIVITY=$waiting"
echo "WAITING_APPROVAL=$approval"
echo "DECLINED_APPROVAL=$(api patch-pilot "/v1/activities/$declined" | jq -r '[.calls[] | select(.approval_id != null)][0].approval_id')"
echo "T0=$(date +%s)"
