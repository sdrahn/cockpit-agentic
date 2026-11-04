#!/bin/bash
# Functional tests of Custos on a real SLES 16 or Leap 16 system
# with mcp-gateway: agents created with agent-admin, run by systemd,
# governed by the gateway (supervised by default, approvals, autonomy
# modes, pause, emergency stop), schedules, AG-UI, delegation, the
# journal records, the sandbox and SELinux. Package custos-tests
# installs it as /usr/libexec/custos/tests/run-tests.sh for
# openQA and other test systems. Next to it:
#   fakemodel  a scripted OpenAI-compatible model server (test/vm/fakemodel)
#   testsrv    the MCP server custos-test (test/vm/testsrv)
#
# Usage: run-tests.sh --disposable [--results DIR]
#
# It runs as root on a system that is there for testing: it creates
# agents (t1, t2) and a user (custos-tester), registers MCP servers
# with the gateway, triggers the emergency stop, and does not undo all
# of it. --disposable says that this is fine.
#   --results DIR  writes results.tap (TAP) and results.xml (JUnit) there
# Every check prints PASS, FAIL or SKIP; the script exits 1 if any failed,
# 2 on wrong usage.
set -u
dir=$(cd "$(dirname "$0")" && pwd)
failed=0
disposable= results=
usage() {
	echo "usage: $0 --disposable [--results DIR]" >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--disposable) disposable=1 ;;
	--results) results=${2:?}; shift ;;
	*) usage ;;
	esac
	shift
done
if [ -z "$disposable" ]; then
	echo "$0: changes this system for good (agents, users, gateway servers, an emergency stop);" >&2
	echo "run it on a system that is there for testing, with --disposable" >&2
	exit 2
fi
[ "$(id -u)" = 0 ] || { echo "$0: runs as root" >&2; exit 2; }
work=/root/custos-test
mkdir -p "$work"
t0=$(date +%s)

# --- the framework (as mcp-gateway's test/vm/run-tests.sh) ------------------

current_section=start
last_check=$SECONDS
results_list=()
record() {
	results_list+=("$current_section"$'\t'"$1"$'\t'"$2"$'\t'"$((SECONDS - last_check))")
	last_check=$SECONDS
}
pass() { echo "PASS: $*"; record pass "$*"; }
fail() { echo "FAIL: $*"; failed=1; record fail "$*"; fail_context; }
skip() { echo "SKIP: $*"; record skip "$*"; }
fail_context() {
	# What the agent answered last, when a check compared it.
	if [ -n "${last_answer:-}" ]; then
		echo "    | the agent answered: $(jq -c '{state, summary, reason, calls: [.calls[]? | {tool, state, reason}]}' <<<"$last_answer" 2>/dev/null || head -c 300 <<<"$last_answer")"
		last_answer=
	fi
	journalctl --since -20s --no-pager -o short-precise 2>/dev/null |
		grep -E 'agent-|agent@|mcp-gateway' | grep -iE 'error|fail|denied|panic|status=[0-9]' |
		tail -6 | sed 's/^/    | /'
	# The SELinux denials behind a "Permission denied", in the log itself.
	# (ausearch when auditd runs, the kernel log when it does not).
	{
		ausearch -m AVC,USER_AVC,SELINUX_ERR -ts recent 2>/dev/null | grep -E '^type=(AVC|USER_AVC|SELINUX_ERR)'
		journalctl -k --since -30s --no-pager -o cat 2>/dev/null | grep -E 'avc: +denied'
	} | sed -E 's/^type=[A-Z_]+ msg=audit\([^)]*\): //; s/^.*(avc: +denied)/\1/' | sort -u | tail -6 | sed 's/^/    | avc: /'
}
check() { # check <description> <command...>
	local desc=$1
	shift
	# Without a running agent, the checks that need one are skipped: they
	# would only wait for their timeouts (no_agent is set by Discovery).
	if [ -n "${no_agent:-}" ] && needs_agent; then
		skip "$desc (no agent is running: see Discovery)"
		return
	fi
	if "$@"; then pass "$desc"; else fail "$desc"; return 1; fi
}
needs_agent() {
	case $current_section in
	"Supervised by default" | Approval:* | "Autonomy modes" | Pause | Cancel | Schedules | AG-UI | "Access to the socket" | Delegation) return 0 ;;
	esac
	return 1
}
section() {
	printf '\n=== %s\n' "$*"
	current_section=$*
	last_answer=
}
eventually() { # eventually <seconds> <command...>
	local deadline=$((SECONDS + $1))
	shift
	until "$@"; do
		[ "$SECONDS" -ge "$deadline" ] && return 1
		sleep 0.5
	done
}
xml() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g' <<<"$1"; }

# write_results <dir>: the checks as TAP (results.tap) and JUnit
# (results.xml, a test suite per section), for openQA's parse_extra_log.
write_results() {
	local r sec st desc secs n=0 last= tests fails skips cases=
	mkdir -p "$1" || return
	{
		echo "1..${#results_list[@]}"
		for r in "${results_list[@]}"; do
			IFS=$'\t' read -r sec st desc secs <<<"$r"
			n=$((n + 1))
			desc=${desc//#/\\#}
			case $st in
			pass) echo "ok $n - $sec: $desc" ;;
			skip) echo "ok $n - $sec: $desc # SKIP" ;;
			*) echo "not ok $n - $sec: $desc" ;;
			esac
		done
	} >"$1/results.tap"
	{
		echo '<?xml version="1.0" encoding="UTF-8"?>'
		printf '<testsuites name="custos" tests="%d">\n' "${#results_list[@]}"
		for r in "${results_list[@]}" ""; do
			IFS=$'\t' read -r sec st desc secs <<<"$r"
			if [ "$sec" != "$last" ] && [ -n "$last" ]; then
				tests=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' -v s="$last" '$1 == s' | wc -l)
				fails=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' -v s="$last" '$1 == s && $2 == "fail"' | wc -l)
				skips=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' -v s="$last" '$1 == s && $2 == "skip"' | wc -l)
				printf '  <testsuite name="%s" tests="%d" failures="%d" errors="0" skipped="%d">\n' \
					"$(xml "$last")" "$tests" "$fails" "$skips"
				printf '%s' "$cases"
				echo '  </testsuite>'
			fi
			[ -z "$r" ] && break
			[ "$sec" != "$last" ] && cases=
			last=$sec
			cases+=$(printf '    <testcase classname="custos.%s" name="%s" time="%s">' "$(xml "$sec")" "$(xml "$desc")" "$secs")
			case $st in
			fail) cases+='<failure message="failed"/>' ;;
			skip) cases+='<skipped/>' ;;
			esac
			cases+=$'</testcase>\n'
		done
		echo '</testsuites>'
	} >"$1/results.xml"
	echo "  results: $1/results.tap, $1/results.xml"
}

# --- helpers -----------------------------------------------------------------

# api <agent> <path> [curl args...]: the agent's socket, as root.
api() { local a=$1 p=$2; shift 2; curl -sS --max-time 60 --unix-socket "/run/agents/$a/api.sock" "http://agent$p" "$@"; }
# gw <path> [curl args...]: the gateway's control API, as root.
gw() { local p=$1; shift; curl -sS --max-time 30 --unix-socket /run/mcp-gateway/control.sock "http://localhost$p" "$@"; }
# run_activity <agent> <prompt>: starts an activity and waits; prints it.
run_activity() { api "$1" /v1/activities -X POST -d "$(jq -nc --arg p "$2" '{prompt: $p, wait: true}')"; }
# start_activity <agent> <prompt>: starts one; prints its id.
start_activity() { api "$1" /v1/activities -X POST -d "$(jq -nc --arg p "$2" '{prompt: $p}')" | jq -r .id; }
activity() { api "$1" "/v1/activities/$2"; }
activity_state_is() { [ "$(activity "$1" "$2" | jq -r .state)" = "$3" ]; }
# approval_of <agent> <activity>: the approval id its waiting call has.
approval_of() { activity "$1" "$2" | jq -r '[.calls[] | select(.approval_id != null and .approval_id != "")][0].approval_id // empty'; }
# journal <agent> <event>: the agent's records of the event since the start, as JSON lines.
journal() { journalctl -o json --no-pager --since "@$t0" "AGENT_NAME=$1" "AGENT_EVENT=$2" 2>/dev/null; }
# journal_has <agent> <event> <jq condition>
journal_has() { journal "$1" "$2" | jq -e "select($3)" >/dev/null 2>&1; }
gw_audit_has() { journalctl -u mcp-gateway.service -o cat --no-pager --since "@$t0" | grep '"audit":true' | jq -e "select($1)" >/dev/null 2>&1; }
enforcing() { [ "$(getenforce 2>/dev/null)" = Enforcing ]; }
label() { ps -o label= -p "$1" 2>/dev/null | tr -d ' '; }
main_pid() { systemctl show -p MainPID --value "agent@$1.service"; }
summary_has() { grep -qF -- "$2" <<<"$(jq -r .summary <<<"$1")"; }
# runs_with <agent> <prompt> <text>: an activity whose summary has <text>.
runs_with() {
	last_answer=$(run_activity "$1" "$2")
	summary_has "$last_answer" "$3"
}
not() { ! "$@"; }
json_ok() { jq -e "$2" >/dev/null 2>&1 <<<"$1"; }
# code_is <status> <command...>: the HTTP status of a curl-based helper.
code_is() { local want=$1; shift; [ "$("$@" -o /dev/null -w '%{http_code}')" = "$want" ]; }
stat_is() { [ "$(stat -c %U:%G:%a "$1" 2>/dev/null)" = "$2" ]; }
user_is() { [ "$(ps -o user= -p "$1" 2>/dev/null | tr -d ' ')" = "$2" ]; }
in_group() { id -nG "$1" 2>/dev/null | grep -qw "$2"; }
servers_have() { gw /v1/servers | grep -q "\"$1\""; }
# as_nobody runs a command as an unprivileged user that is in no group of
# ours (not agent-clients), from a directory it may enter.
as_nobody() { (cd / && setpriv --reuid=nobody --regid="$(id -g nobody)" --clear-groups -- "$@"); }
as_tester() { runuser -u custos-tester -- curl -s --max-time 30 --unix-socket /run/agents/t1/api.sock "$@"; }
count() { journal "$1" "$2" | wc -l; }

# config <name> <servers> [yaml]: an agent configuration on the fake model.
config() {
	cat >"$work/$1.yaml" <<EOF
version: 1
name: $1
description: functional test agent $1
model: {provider: openai, name: fake, endpoint: "http://127.0.0.1:8008/v1"}
instruction: You are a test agent.
gateway: {servers: [$2]}
limits: {max_invocation_seconds: 300}
interaction: {agui: true, client_tools: deny}
${3:-}
EOF
	echo "$work/$1.yaml"
}

# --- preconditions -------------------------------------------------------------

section Preconditions
for p in mcp-gateway custos custos-tests jq curl; do
	check "package or program $p is installed" bash -c "rpm -q $p >/dev/null 2>&1 || command -v $p >/dev/null"
done
systemctl enable --now mcp-gateway.service >/dev/null 2>&1
check "the gateway is running" eventually 30 test -S /run/mcp-gateway/mcp.sock
check "the agents' policy module is installed for the gateway" test -f /usr/share/mcp-gateway/policy/custos/agents.rego
check "group agent-clients exists" getent group agent-clients

section Labels
for d in /run/agents /var/lib/agents /etc/custos; do
	if enforcing; then
		check "$d has the module's label" grep -qE 'agent_(runtime|var_lib|conf)_t' <<<"$(stat -c %C "$d" 2>/dev/null)"
	else
		skip "$d has the module's label (SELinux is not enforcing)"
	fi
done

section Setup
systemctl stop custos-fakemodel.service >/dev/null 2>&1
systemd-run --unit=custos-fakemodel --property=DynamicUser=yes "$dir/fakemodel" --listen 127.0.0.1:8008 >/dev/null 2>&1
check "the fake model answers" eventually 20 bash -c 'curl -sf -X POST -d "{\"messages\":[]}" http://127.0.0.1:8008/v1/chat/completions | grep -q ok'
cat >/etc/mcp-gateway/servers.d/custos-test.yaml <<EOF
# Custos functional tests (run-tests.sh)
version: 1
name: custos-test
command: ["$dir/testsrv"]
run_as: principal
EOF
check "the gateway takes the test server" eventually 30 servers_have custos-test

# --- creating an agent -------------------------------------------------------

section "Creating an agent"
f=$(config t1 custos-test 'schedules: [{name: check, on_calendar: "*-*-* 03:00", prompt: "PROBE (scheduled)"}]')
check "agent-admin check accepts the configuration" agent-admin check "$f"
printf 'version: 1\nname: t1\nmodel: {provider: openai, name: m, endpoint: "http://x"}\nlimitz: {}\n' >"$work/bad.yaml"
check "agent-admin check refuses an unknown key" not agent-admin check "$work/bad.yaml"
agent-admin delete t1 >/dev/null 2>&1
check "agent-admin create t1" agent-admin create t1 --from "$f"
check "agent-admin refuses to create t1 twice" not agent-admin create t1 --from "$f"
check "user agent-t1 exists, without a login shell" bash -c 'getent passwd agent-t1 | grep -q nologin'
check "agent-t1 is in mcp-users" in_group agent-t1 mcp-users
check "agent-t1 is in agent-clients" in_group agent-t1 agent-clients
check "the configuration is root:agent-t1 0640" stat_is /etc/custos/agents/t1/agent.yaml root:agent-t1:640
check "the socket unit is active" systemctl is-active -q agent@t1.socket
check "the gateway data says supervised" grep -q "mode: supervised" /etc/mcp-gateway/policy/agents/t1.yaml
check "the gateway data grants nothing" grep -q "permissions: \[\]" /etc/mcp-gateway/policy/agents/t1.yaml
check "the schedule's timer is enabled" systemctl is-enabled -q 'agent-schedule@t1_check.timer'
check "agent-admin list names t1" bash -c 'agent-admin list | grep -qx t1'

section Discovery
st=$(api t1 /v1/status)
if json_ok "$st" '.contract == 1 and .name == "t1"'; then
	pass "GET /v1/status answers contract 1 (starts the agent)"
else
	fail "GET /v1/status answers contract 1 (starts the agent)"
	no_agent=1
	systemctl status --no-pager agent@t1.service 2>&1 | tail -n 15 | sed 's/^/    | /'
fi
check "status names the units" json_ok "$st" '.units.service == "agent@t1.service"'
check "status says the lifecycle is systemd" json_ok "$st" '.lifecycle.kind == "systemd" and .lifecycle.socket == "agent@t1.socket"'
check "the socket is root:agent-clients 0660" stat_is /run/agents/t1/api.sock root:agent-clients:660
check "the agent runs as agent-t1" user_is "$(main_pid t1)" agent-t1
if enforcing; then
	check "the agent runs in agent_t" grep -q ':agent_t:' <<<"$(label "$(main_pid t1)")"
	grep -q ':agent_t:' <<<"$(label "$(main_pid t1)")" ||
		echo "    | domain $(label "$(main_pid t1)"), program $(stat -c %C /usr/libexec/custos/agent-launcher)"
	check "the launcher is labelled agent_exec_t" grep -q ':agent_exec_t:' <<<"$(stat -c %C /usr/libexec/custos/agent-launcher)"
else
	skip "the agent runs in agent_t (SELinux is not enforcing)"
fi
check "journal: agent.start" eventually 10 journal_has t1 agent.start 'true'

section Sandbox
pid=$(main_pid t1)
# InaccessiblePaths= leaves the name and takes the access away.
check "the agent cannot reach the gateway's control socket" not nsenter -t "$pid" -m \
	curl -s --max-time 5 --unix-socket /run/mcp-gateway/control.sock -o /dev/null http://localhost/v1/whoami
check "the agent sees no agents' sockets" test -z "$(nsenter -t "$pid" -m ls -A /run/agents 2>/dev/null)"
check "the agent cannot write /etc" not nsenter -t "$pid" -m -S "$(id -u agent-t1)" -G "$(id -g agent-t1)" touch /etc/custos-test
for p in NoNewPrivileges=yes ProtectSystem=strict ProtectHome=yes PrivateTmp=yes; do
	check "unit property $p" grep -qx "$p" <<<"$(systemctl show agent@t1.service -p "${p%%=*}")"
done

# --- the gateway governs --------------------------------------------------------

section "Supervised by default"
a=$(run_activity t1 PROBE)
last_answer=$a
check "without permissions, the gateway shows the agent no tools" summary_has "$a" "not offered: custos-test__probe"
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change}' | agent-admin permissions t1
check "with probe read-only, PROBE runs without an approval" eventually 30 runs_with t1 PROBE 'probe ok'
check "journal: tool.result ok for custos-test__probe" eventually 10 journal_has t1 tool.result '.AGENT_TOOL == "custos-test__probe" and .AGENT_OUTCOME == "ok"'
check "journal: arguments are a digest only" journal_has t1 tool.call '.AGENT_ARGS_SHA256 != null'

for decision in approve deny; do
	section "Approval: $decision"
	id=$(start_activity t1 CHANGE)
	check "the activity waits for an approval" eventually 30 activity_state_is t1 "$id" waiting_approval
	aid=$(approval_of t1 "$id")
	check "its call carries the gateway's approval id" test -n "$aid"
	check "the gateway lists the approval for agent-t1" json_ok "$(gw /v1/approvals)" ".[] | select(.id == \"$aid\" and .principal.sub == \"agent-t1\")"
	check "journal: approval.pending with the id" eventually 10 journal_has t1 approval.pending ".AGENT_APPROVAL_ID == \"$aid\""
	check "journal: the call's rationale" journal_has t1 tool.call '.AGENT_RATIONALE == "Writing the test marker; no other impact."'
	check "the call is still waiting before the decision" json_ok "$(activity t1 "$id")" '.calls[-1].state == "waiting_approval"'
	gw "/v1/approvals/$aid" -X POST -d "{\"decision\":\"$decision\",\"scope\":\"once\"}" >/dev/null
	check "the activity finishes" eventually 30 activity_state_is t1 "$id" finished
	want=$([ "$decision" = approve ] && echo approved || echo declined)
	check "its call is $want" json_ok "$(activity t1 "$id")" ".calls[-1].approval == \"$want\""
	check "journal: approval.resolved $want" eventually 10 journal_has t1 approval.resolved ".AGENT_APPROVAL_ID == \"$aid\" and .AGENT_OUTCOME == \"$want\""
	if [ "$decision" = approve ]; then
		check "the change ran" summary_has "$(activity t1 "$id")" 'changed test'
	else
		check "the change did not run" not summary_has "$(activity t1 "$id")" 'changed test'
	fi
done

section "Autonomy modes"
agent-admin mode t1 advisory
check "advisory: the gateway hides the change" eventually 30 runs_with t1 CHANGE 'not offered: custos-test__change'
check "advisory: reading still works" runs_with t1 PROBE 'probe ok'
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change, autonomous: true}' | agent-admin permissions t1
agent-admin mode t1 bounded
n0=$(count t1 approval.pending)
check "bounded: an autonomous change runs without an approval" eventually 30 runs_with t1 CHANGE 'changed test'
check "bounded: no approval was asked" test "$(count t1 approval.pending)" = "$n0"
check "the gateway audited the call as agent-t1" eventually 10 gw_audit_has '.sub == "agent-t1" and .name == "change" and .effect == "allow"'
agent-admin mode t1 supervised
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change}' | agent-admin permissions t1

# --- intervention -----------------------------------------------------------------

section Pause
agent-admin pause t1
check "the gateway data says paused" grep -q 'paused: true' /etc/mcp-gateway/policy/agents/t1.yaml
check "status says paused" json_ok "$(api t1 /v1/status)" '.paused == true'
# What the Cockpit page reads for the posture, as anyone without root.
check "agent-admin show, as nobody, says paused" json_ok "$(as_nobody /usr/sbin/agent-admin show t1)" \
	'.agents.t1.paused == true and .agents.t1.mode == "supervised" and (.agents.t1.permissions | length) == 2'
check "a new activity is refused (409)" code_is 409 api t1 /v1/activities -X POST -d '{"prompt":"PROBE"}'
check "journal: agent.paused" eventually 10 journal_has t1 agent.paused 'true'
agent-admin resume t1
check "after resume, activities run again" eventually 30 runs_with t1 PROBE 'probe ok'

section Cancel
id=$(start_activity t1 CHANGE)
eventually 30 activity_state_is t1 "$id" waiting_approval
aid=$(approval_of t1 "$id")
check "cancel answers 204" code_is 204 api t1 "/v1/activities/$id/cancel" -X POST
check "its state is cancelled" eventually 20 activity_state_is t1 "$id" cancelled
check "journal: activity.cancel" eventually 10 journal_has t1 activity.cancel ".AGENT_INVOCATION == \"$id\""
[ -n "$aid" ] && gw "/v1/approvals/$aid" -X POST -d '{"decision":"deny","scope":"once"}' >/dev/null 2>&1

section Schedules
if ! systemctl start 'agent-schedule@t1_check.service'; then
	journalctl --no-pager -o cat --since "@$t0" -u 'agent-schedule@t1_check.service' 2>&1 | tail -n 15 | sed 's/^/    | /'
fi
check "the schedule's run is an activity of origin schedule" eventually 30 journal_has t1 invocation.start '.AGENT_ORIGIN == "schedule" and .AGENT_SCHEDULE == "check" and .AGENT_USER == "system:schedule"'

section AG-UI
out=$(api t1 /agui -N -w '\nHTTP %{http_code}\n' -X POST -d '{"threadId":"custos-test-1","messages":[{"role":"user","content":"PROBE"}],"tools":[{"name":"evil","parameters":{"type":"object"}}]}')
agui_ok=1
for ev in RUN_STARTED STATE_SNAPSHOT TOOL_CALL_START TOOL_CALL_RESULT TEXT_MESSAGE_CONTENT RUN_FINISHED; do
	check "the stream has $ev" grep -q "\"type\":\"$ev\"" <<<"$out" || agui_ok=
done
if [ -z "$agui_ok" ]; then
	# What the stream had instead: its events, and a RUN_ERROR's message.
	echo "    | stream: $(sed -n 's/^data: //p' <<<"$out" | jq -rs 'map(.type) | join(",")' 2>/dev/null)"
	# Not a stream at all (an HTTP error's body), as it came.
	grep -q '^data: ' <<<"$out" || echo "    | answer: $(head -c 400 <<<"$out")"
	sed -n 's/^data: //p' <<<"$out" | jq -c 'select(.type == "RUN_ERROR")' 2>/dev/null | sed 's/^/    | /'
	journalctl --since -60s --no-pager -o cat -u agent@t1.service 2>/dev/null | tail -n 20 | sed 's/^/    | t1: /'
fi
check "a client tool not allowed is journaled" eventually 10 journal_has t1 tool.denied.local '.AGENT_TOOL == "evil"'

section "Access to the socket"
id custos-tester >/dev/null 2>&1 || useradd -m custos-tester
gpasswd -d custos-tester agent-clients >/dev/null 2>&1
check "a user outside agent-clients cannot connect" not as_tester -f http://agent/v1/status -o /dev/null
usermod -aG agent-clients custos-tester
check "a member of agent-clients reads the status" as_tester -f http://agent/v1/status -o /dev/null
check "but not another user's sessions (403)" code_is 403 as_tester http://agent/apps/t1/users/root/sessions
check "and cannot pause the agent (403)" code_is 403 as_tester -X POST http://agent/v1/pause
check "ADK's builder endpoint is not reachable (404)" code_is 404 api t1 /builder/save -X POST

section Delegation
cp /usr/share/custos/gateway/servers.d/agents.yaml /etc/mcp-gateway/servers.d/agents.yaml
eventually 30 servers_have agents
agent-admin delete t2 >/dev/null 2>&1
check "agent-admin create t2" agent-admin create t2 --from "$(config t2 agents)"
printf '%s\n' "- {server: agents, tool: delegate, read_only: true, args: {agent: '^t1\$'}}" | agent-admin permissions t2
d=$(run_activity t2 "DELEGATE t1 PROBE")
check "t2's delegation returns t1's answer" summary_has "$d" "probe ok"
did=$(jq -r .id <<<"$d")
check "t1 recorded a delegated activity from agent-t2 with t2's activity as parent" eventually 10 journal_has t1 invocation.start ".AGENT_ORIGIN == \"delegation\" and .AGENT_ORIGIN_BY == \"agent-t2\" and .AGENT_PARENT_AGENT == \"t2\" and .AGENT_PARENT_ACTIVITY == \"$did\""
check "the gateway audited agents__delegate by agent-t2" eventually 10 gw_audit_has '.sub == "agent-t2" and .server == "agents" and .name == "delegate" and .effect == "allow"'
d=$(run_activity t2 "DELEGATE t3 PROBE")
check "delegating to an agent outside the permission is denied" not summary_has "$d" 'probe ok'
check "the gateway audited the denial" eventually 10 gw_audit_has '.sub == "agent-t2" and .name == "delegate" and .effect == "deny"'

section "Emergency stop"
check "agent-admin stop-all" agent-admin stop-all
check "the gateway's emergency stop flag is set" grep -q 'stopped: true' /etc/mcp-gateway/policy/agents/_control.yaml
check "agent-admin show, as nobody, reports the emergency stop" json_ok "$(as_nobody /usr/sbin/agent-admin show)" \
	'.control.stopped == true and (.agents | has("t1")) and (.agents | has("t2"))'
for u in agent@t1.socket agent@t1.service agent@t2.socket agent@t2.service agent-schedule@t1_check.timer; do
	check "$u is stopped" not systemctl is-active -q "$u"
done
systemctl start agent@t1.socket
check "an agent started by hand is still denied by the gateway" not runs_with t1 PROBE 'probe ok'
check "agent-admin release-all" agent-admin release-all
check "sockets are back" systemctl is-active -q agent@t1.socket agent@t2.socket
check "and calls are allowed again" eventually 30 runs_with t1 PROBE 'probe ok'

section SELinux
if enforcing; then
	# Every denial that names our domains or types, our programs or our
	# paths, whatever the other side's type (a directory labelled before
	# the module was loaded has a generic one).
	avc=$(ausearch -m AVC,USER_AVC,SELINUX_ERR -ts "$(date -d "@$t0" '+%m/%d/%Y %H:%M:%S')" 2>/dev/null |
		grep -E 'agent_|mcpsrv_agents_t|comm="agent-|name="(agents|api\.sock|t1|t2)"|/run/agents|/var/lib/agents|custos')
	check "no SELinux denials for the agents" test -z "$avc"
	[ -n "$avc" ] && sed 's/^/    | /' <<<"$avc" | head -30
else
	skip "no SELinux denials (SELinux is not enforcing)"
fi

section Removing
check "agent-admin delete t2" agent-admin delete t2
check "agent-admin delete t1" agent-admin delete t1
check "t1's configuration is gone" not test -e /etc/custos/agents/t1
check "t1's gateway data is gone" not test -e /etc/mcp-gateway/policy/agents/t1.yaml
check "t1's state is gone" not test -e /var/lib/agents/t1
check "t1's schedule is gone" not systemctl is-enabled -q 'agent-schedule@t1_check.timer'
rm -f /etc/mcp-gateway/servers.d/custos-test.yaml /etc/mcp-gateway/servers.d/agents.yaml
systemctl stop custos-fakemodel.service >/dev/null 2>&1

# --- results --------------------------------------------------------------------

if [ -n "$results" ]; then
	write_results "$results"
	ausearch -m AVC,USER_AVC,SELINUX_ERR -ts "$(date -d "@$t0" '+%m/%d/%Y %H:%M:%S')" >"$results/avc.txt" 2>&1
	journalctl --no-pager --since "@$t0" -o short-precise >"$results/journal.txt" 2>&1
fi
n_fail=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' '$2 == "fail"' | wc -l)
n_skip=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' '$2 == "skip"' | wc -l)
printf '\n%d checks, %d failed, %d skipped\n' "${#results_list[@]}" "$n_fail" "$n_skip"
exit "$failed"
