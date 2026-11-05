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
# Usage: run-tests.sh --disposable [--results DIR] [--model URL NAME]
#
# It runs as root on a system that is there for testing: it creates
# agents (t1, t2) and a user (custos-tester), registers MCP servers
# with the gateway, triggers the emergency stop, and does not undo all
# of it. --disposable says that this is fine.
#   --results DIR  writes results.tap (TAP) and results.xml (JUnit) there
#   --model URL NAME  also runs an agent on a real model: an
#                  OpenAI-compatible server at URL (such as
#                  http://127.0.0.1:11434/v1) serving NAME. The air-gapped
#                  gate (test/airgap) gives it a local model on a system
#                  without network.
# Every check prints PASS, FAIL or SKIP; the script exits 1 if any failed,
# 2 on wrong usage.
set -u
dir=$(cd "$(dirname "$0")" && pwd)
failed=0
disposable= results= model_url= model_name=
usage() {
	echo "usage: $0 --disposable [--results DIR] [--model URL NAME]" >&2
	exit 2
}
while [ $# -gt 0 ]; do
	case $1 in
	--disposable) disposable=1 ;;
	--results) results=${2:?}; shift ;;
	--model) model_url=${2:?}; model_name=${3:?}; shift 2 ;;
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

section Documentation
if rpm -q custos-docs >/dev/null 2>&1; then
	check "the user guide is installed" test -f /usr/share/custos/docs/user-guide/README.md
	check "the contract is installed beside it" test -f /usr/share/custos/docs/CONTRACT.md
	check "the mcp-docs collection file names the guide" \
		grep -qx 'root: /usr/share/custos/docs' /usr/share/mcp-docs/collections.d/custos.yaml
	if command -v mcp-docs >/dev/null; then
		check "mcp-docs serves the collection custos" bash -c \
			"timeout 10 mcp-docs </dev/null 2>&1 >/dev/null | grep -q 'serving .*custos=/usr/share/custos/docs'"
	else
		skip "mcp-docs serves the collection custos (mcp-docs is not installed)"
	fi
else
	skip "the user guide is installed (custos-docs is not installed)"
fi

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
# As the Cockpit page does: the configuration on stdin, checked without root.
check "agent-admin check - (stdin), as nobody, accepts it" bash -c "$(declare -f as_nobody); as_nobody /usr/sbin/agent-admin check - <'$f'"
check "agent-admin check - refuses an unknown key" not bash -c "agent-admin check - <'$work/bad.yaml'"
agent-admin delete t1 >/dev/null 2>&1
check "agent-admin create t1 --from - (stdin)" bash -c "agent-admin create t1 --from - <'$f'"
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
# Editing, as the Cockpit page does: the new configuration on stdin.
sed -e 's/^description: .*/description: edited test agent t1/' -e 's/03:00/04:00/' "$f" >"$work/t1-edited.yaml"
check "agent-admin edit t1 --from - (stdin)" bash -c "agent-admin edit t1 --from - <'$work/t1-edited.yaml'"
check "the edited configuration is installed" grep -q 'description: edited test agent t1' /etc/custos/agents/t1/agent.yaml
check "it is still root:agent-t1 0640" stat_is /etc/custos/agents/t1/agent.yaml root:agent-t1:640
check "the schedule's timer has the new calendar" grep -q 'OnCalendar=\*-\*-\* 04:00' /etc/systemd/system/agent-schedule@t1_check.timer.d/schedule.conf
check "agent-admin edit refuses a configuration for another agent" \
	not bash -c "sed 's/^name: t1/name: t9/' '$f' | agent-admin edit t1 --from -"
check "the socket unit is still active" systemctl is-active -q agent@t1.socket
agent-admin edit t1 --from "$f" >/dev/null

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
	# The history of the action (§10) as the Cockpit page's Audit tab joins
	# it: the gateway's decision record and the result, by approval id.
	ok=$([ "$decision" = approve ] && echo true || echo false)
	check "the gateway's audit: mcp-approval for $aid, $want" eventually 10 \
		gw_audit_has ".event == \"mcp-approval\" and .id == \"$aid\" and .principal == \"agent-t1\" and .ok == $ok and .by != null"
	check "journal: tool.result names approval $aid" eventually 10 journal_has t1 tool.result ".AGENT_APPROVAL_ID == \"$aid\""
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

section "Task categories"
# A change of the category waits for an approval, which is then declined.
asks_and_deny() {
	local id aid
	id=$(start_activity t1 CHANGE)
	eventually 30 activity_state_is t1 "$id" waiting_approval || return 1
	aid=$(approval_of t1 "$id")
	gw "/v1/approvals/$aid" -X POST -d '{"decision":"deny","scope":"once"}' >/dev/null
	eventually 30 activity_state_is t1 "$id" finished
}
check "the catalog has patch-errata" bash -c "agent-admin categories | jq -e '.\"patch-errata\".title' >/dev/null"
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change, category: patch-errata}' | agent-admin permissions t1
check "a category starts supervised: its change waits for an approval" asks_and_deny
# Readiness (§6a) from the agent's records and the gateway's decision.
check "readiness counts the declined proposal, from the gateway's decision" eventually 30 bash -c \
	"agent-admin readiness t1 patch-errata | jq -e '.categories[\"patch-errata\"] | .proposals >= 1 and .declined >= 1 and .override_rate > 0 and .ready == false' >/dev/null"
check "promote says why the category is not ready" bash -c "printf 'windows: [{from: \"00:00\", to: \"23:59\"}]\n' | agent-admin promote t1 patch-errata --envelope - 2>&1 | grep -q 'is not ready (.*proposals of 50'"
# Today, all day (UTC); not in the last minutes of a day.
[ "$(date -u +%H%M)" -ge 2357 ] && sleep 180
today=$(date -u +%a | tr '[:upper:]' '[:lower:]')
other=$(date -u -d tomorrow +%a | tr '[:upper:]' '[:lower:]')
printf 'windows:\n  - {days: [%s], from: "00:00", to: "23:59", tz: UTC}\n' "$today" >"$work/now.yaml"
printf 'windows:\n  - {days: [%s], from: "00:00", to: "23:59", tz: UTC}\n' "$other" >"$work/other.yaml"
check "promote refuses without readiness" not agent-admin promote t1 patch-errata --envelope "$work/now.yaml"
check "promote refuses a rate (not enforced yet)" not bash -c "{ cat '$work/now.yaml'; echo 'rate: {calls: 1, per: 1h}'; } | agent-admin promote t1 patch-errata --envelope - --despite-readiness --reason test"
check "agent-admin promote t1 patch-errata" agent-admin promote t1 patch-errata --envelope "$work/now.yaml" --despite-readiness --reason "functional test"
check "show: patch-errata is bounded" bash -c "agent-admin show t1 | jq -e '.agents.t1.categories[\"patch-errata\"].mode == \"bounded\"' >/dev/null"
n0=$(count t1 approval.pending)
check "within its window, the category's change runs without an approval" eventually 30 runs_with t1 CHANGE 'changed test'
check "no approval was asked" test "$(count t1 approval.pending)" = "$n0"
check "journal: category.promoted by root, with the reason" eventually 10 journal_has t1 category.promoted '.SYSLOG_IDENTIFIER == "custos-promotion" and .AGENT_CATEGORY == "patch-errata" and .AGENT_MODE_TO == "bounded" and .AGENT_REASON == "functional test"'
# The after-the-fact review (§6a) of what the promoted category did.
check "review lists the change made without an approval" bash -c \
	"agent-admin review t1 patch-errata | jq -e '.categories[\"patch-errata\"].pending | map(select(.tool == \"custos-test__change\")) | length >= 1' >/dev/null"
act=$(agent-admin review t1 patch-errata | jq -r '.categories["patch-errata"].pending[-1] | "\(.activity) \(.call)"')
# shellcheck disable=SC2086 # activity and call
check "review: ok keeps the category bounded" bash -c "agent-admin review t1 patch-errata $act ok >/dev/null && agent-admin show t1 | jq -e '.agents.t1.categories[\"patch-errata\"].mode == \"bounded\"' >/dev/null"
# shellcheck disable=SC2086
check "review: incident (should not have run) demotes it" bash -c "agent-admin review t1 patch-errata $act incident --reason 'functional test' >/dev/null && agent-admin show t1 | jq -e '.agents.t1.categories[\"patch-errata\"].mode == \"supervised\"' >/dev/null"
check "journal: category.review, by root, with the verdict" eventually 10 journal_has t1 category.review '.AGENT_VERDICT == "incident" and .AGENT_TOOL == "custos-test__change" and .AGENT_REASON == "functional test"'
check "readiness counts the review as an override" bash -c "agent-admin readiness t1 patch-errata | jq -e '.categories[\"patch-errata\"] | .reviewed >= 1 and .review_overrides >= 1' >/dev/null"
# Only root's reviews count. A later verdict on the same action replaces
# an earlier one, so an agent that could write a review would turn root's
# incident into "ok" and lower its override rate: one written into the
# journal as the agent's user, with agent-admin's identifier, is none
# (journald's trusted _UID).
check "journal: the review is root's (_UID 0)" journal_has t1 category.review '._UID == "0" and .AGENT_VERDICT == "incident"'
overrides() { agent-admin readiness t1 patch-errata | jq '.categories["patch-errata"].review_overrides'; }
o0=$(overrides)
read -r act_id act_call <<<"$act"
runuser -u agent-t1 -- logger --journald <<EOF
MESSAGE=forged review
SYSTEMD_UNIT=agent@t1.service
SYSLOG_IDENTIFIER=custos-promotion
AGENT_CONTRACT=1
AGENT_NAME=t1
AGENT_EVENT=category.review
AGENT_CATEGORY=patch-errata
AGENT_INVOCATION=$act_id
AGENT_CALL_ID=$act_call
AGENT_VERDICT=ok
AGENT_BY=root
EOF
check "a review the agent's user wrote is in the journal, not as root's" eventually 10 journal_has t1 category.review \
	".AGENT_INVOCATION == \"$act_id\" and .AGENT_VERDICT == \"ok\" and ._UID != \"0\""
check "readiness keeps root's incident: the agent's own review does not replace it" test "$(overrides)" = "$o0" -a "$o0" -ge 1
check "promote with a window of another day" agent-admin promote t1 patch-errata --envelope "$work/other.yaml" --despite-readiness --reason "functional test"
check "outside its window, the change waits for an approval" asks_and_deny
check "agent-admin demote t1 patch-errata" agent-admin demote t1 patch-errata --reason "functional test"
check "demoted: the change waits for an approval" asks_and_deny
check "journal: category.demoted" eventually 10 journal_has t1 category.demoted '.AGENT_CATEGORY == "patch-errata" and .AGENT_MODE_TO == "supervised"'
# The demotion evaluator (§6a), on a rollback health check.
touch /run/custos-test-healthy
printf 'windows:\n  - {days: [%s], from: "00:00", to: "23:59", tz: UTC}\nrollback: {on: [health_check_failed], health_check: "test -e /run/custos-test-healthy"}\n' "$today" >"$work/rollback.yaml"
check "promote with a rollback health check" agent-admin promote t1 patch-errata --envelope "$work/rollback.yaml" --despite-readiness --reason "functional test"
check "promote enabled custos-demote.timer" systemctl is-enabled -q custos-demote.timer
check "evaluate leaves a healthy category bounded" bash -c \
	"agent-admin evaluate >/dev/null && agent-admin show t1 | jq -e '.agents.t1.categories[\"patch-errata\"].mode == \"bounded\"' >/dev/null"
rm -f /run/custos-test-healthy
check "evaluate demotes it when its health check fails" bash -c "agent-admin evaluate | grep -q 'category patch-errata demoted: rollback health_check_failed'"
check "the category is supervised again" bash -c "agent-admin show t1 | jq -e '.agents.t1.categories[\"patch-errata\"].mode == \"supervised\"' >/dev/null"
check "journal: category.demoted by custos-demote, with the reason" eventually 10 journal_has t1 category.demoted \
	'.AGENT_BY == "custos-demote" and (.AGENT_REASON | startswith("rollback health_check_failed"))'
check "custos-demote.service runs the evaluator" systemctl start custos-demote.service
systemctl disable --now custos-demote.timer >/dev/null 2>&1
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
# What an administrator disabled stays so: release-all starts what is
# enabled, and edit enables only new schedules.
systemctl disable --now agent-schedule@t1_check.timer >/dev/null 2>&1
check "agent-admin stop-all (again)" agent-admin stop-all
check "agent-admin release-all (again)" agent-admin release-all
check "release-all leaves a disabled schedule's timer stopped" not systemctl is-active -q agent-schedule@t1_check.timer
check "and the enabled sockets are back" systemctl is-active -q agent@t1.socket agent@t2.socket
check "agent-admin edit t1 leaves the disabled schedule disabled" bash -c \
	"cp /etc/custos/agents/t1/agent.yaml '$work/t1-same.yaml' && agent-admin edit t1 --from '$work/t1-same.yaml' && ! systemctl is-enabled -q agent-schedule@t1_check.timer && ! systemctl is-active -q agent-schedule@t1_check.timer"
systemctl enable --now agent-schedule@t1_check.timer >/dev/null 2>&1

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

section Templates
tdir=/usr/share/custos/templates
check "the templates are installed" test -f "$tdir/triage/template.yaml" -a -f "$tdir/exec.d-disk-usage.yaml"
check "agent-admin templates, as nobody, lists them all, none broken" bash -c \
	"$(declare -f as_nobody); as_nobody /usr/sbin/agent-admin templates | jq -e '(.templates | length) >= 7 and ([.templates[] | select(.error)] | length) == 0' >/dev/null"
if findmnt -no OPTIONS / | tr , '\n' | grep -qx ro; then want=true; else want=false; fi
check "agent-admin templates says whether the system is transactional ($want)" bash -c \
	"agent-admin templates | jq -e '.transactional == $want' >/dev/null"
agent-admin delete tt1 >/dev/null 2>&1
agent-admin delete tt2 >/dev/null 2>&1
# Model defaults (§14b): the fake model, for new agents.
cp -a /etc/custos/defaults.yaml "$work/defaults.yaml.saved" 2>/dev/null
check "agent-admin defaults refuses a model without a name" not bash -c "printf 'model: {provider: openai}\n' | agent-admin defaults --from -"
check "agent-admin defaults --from - sets the model for new agents" bash -c \
	"printf 'model: {provider: openai, name: fake-default, endpoint: \"http://127.0.0.1:8008/v1\"}\n' | agent-admin defaults --from -"
check "the defaults are root 0644" stat_is /etc/custos/defaults.yaml root:root:644
check "agent-admin defaults, as nobody, shows them" bash -c \
	"$(declare -f as_nobody); as_nobody /usr/sbin/agent-admin defaults | jq -e '.model.name == \"fake-default\"' >/dev/null"
check "agent-admin draft, as nobody, has the template with the defaults' model" bash -c \
	"$(declare -f as_nobody); as_nobody /usr/sbin/agent-admin draft x1 --template patch-pilot | grep -q 'name: fake-default' && agent-admin draft x1 --template patch-pilot | agent-admin check -"
check "agent-admin create tt1 --template triage" agent-admin create tt1 --template triage
check "tt1's configuration is the template's, named tt1" bash -c \
	"grep -qx 'name: tt1' /etc/custos/agents/tt1/agent.yaml && grep -q '^# Agent template triage' /etc/custos/agents/tt1/agent.yaml"
check "tt1's configuration has the defaults' model" grep -q 'name: fake-default' /etc/custos/agents/tt1/agent.yaml
check "tt1 is advisory, as the template says" bash -c "agent-admin show tt1 | jq -e '.agents.tt1.mode == \"advisory\"' >/dev/null"
check "tt1 has the template's permissions" bash -c \
	"test \"\$(agent-admin show tt1 | jq '.agents.tt1.permissions | length')\" = \"\$(grep -c '^- ' $tdir/triage/permissions.yaml)\""
# As the Cockpit page does: the edited configuration on stdin.
check "agent-admin create tt2 --template service-doctor --from - (stdin)" bash -c \
	"sed -e 's/^name: .*/name: tt2/' -e 's/qwen3-32b/edited-model/' $tdir/service-doctor/agent.yaml | agent-admin create tt2 --template service-doctor --from -"
check "tt2 has the edited configuration" grep -q edited-model /etc/custos/agents/tt2/agent.yaml
check "tt2 is supervised and its categories are not promoted" bash -c \
	"agent-admin show tt2 | jq -e '.agents.tt2.mode == \"supervised\" and ((.agents.tt2.categories // {}) | length) == 0 and ([.agents.tt2.permissions[] | select(.category == \"config-lifecycle\")] | length) == 1' >/dev/null"
check "agent-admin refuses an unknown template" not agent-admin create tt3 --template no-such
check "agent-admin defaults --clear" agent-admin defaults --clear
check "tt1 keeps its model when the defaults go" grep -q 'name: fake-default' /etc/custos/agents/tt1/agent.yaml
[ -e "$work/defaults.yaml.saved" ] && cp -a "$work/defaults.yaml.saved" /etc/custos/defaults.yaml
check "agent-admin delete tt1 tt2" bash -c "agent-admin delete tt1 && agent-admin delete tt2"

section "Model credentials"
# A model credential, as agent-admin set-credential stores it: sealed to
# the TPM as well where the host has one, which systemd needs while it
# sets up the agent's credentials (DeviceAllow= in agent@.service).
if systemd-analyze has-tpm2 -q 2>/dev/null; then tpm="sealed to the TPM"; else tpm="no TPM: the host key only"; fi
agent-admin delete t3 >/dev/null 2>&1
f=$(config t3 custos-test)
sed 's|^model: .*|model: {provider: openai, name: fake, endpoint: "http://127.0.0.1:8008/v1", credential: sk-litellm-local}|' "$f" > "$f.key"
check "agent-admin refuses a key given as the credential name, without repeating it" bash -c \
	"out=\$(agent-admin create t3 --from '$f.key' 2>&1); [ \$? -ne 0 ] && grep -q 'looks like a key' <<<\"\$out\" && ! grep -q litellm <<<\"\$out\" && ! test -e /etc/custos/agents/t3"
rm -f "$f.key"
sed -i 's|^model: .*|model: {provider: openai, name: fake, endpoint: "http://127.0.0.1:8008/v1", credential: model-key}|' "$f"
check "agent-admin create t3 says its credential is not stored yet" bash -c \
	"agent-admin create t3 --from '$f' | grep -q 'agent-admin set-credential t3 model-key'"
check "without the credential, t3 says how to store it" eventually 30 bash -c \
	"systemctl start agent@t3.service >/dev/null 2>&1; journalctl -u agent@t3.service --since '@$t0' -o cat --no-pager | grep -q 'model.credential model-key is not stored for agent t3: agent-admin set-credential t3 model-key'"
check "agent-admin credentials t3: model-key is missing" bash -c \
	"agent-admin credentials t3 | jq -e '.t3.missing == [\"model-key\"] and (.t3.credentials[0] | .name == \"model-key\" and .stored == false and .used_by == [\"model\"])' >/dev/null"
check "agent-admin set-credential t3 model-key ($tpm)" bash -c "printf 'test-key' | agent-admin set-credential t3 model-key"
check "agent-admin credentials t3: model-key is stored, its value not listed" bash -c \
	"out=\$(agent-admin credentials t3) && jq -e '.t3.missing == [] and .t3.credentials[0].stored and (.t3.credentials[0].stored_at | length > 0)' <<<\"\$out\" >/dev/null && ! grep -q test-key <<<\"\$out\""
check "agent-admin remove-credential refuses the model's credential without --force" not agent-admin remove-credential t3 model-key
check "agent-admin remove-credential removes an unused one" bash -c \
	"printf spare | agent-admin set-credential t3 spare && agent-admin remove-credential t3 spare && ! test -e /etc/credstore.encrypted/agent-t3.spare"
echo "  credential: $(stat -c '%U:%G %a %C' /etc/credstore.encrypted/agent-t3.model-key 2>&1)"
check "the credential is stored encrypted, root's and not readable by others" bash -c \
	"[ \"\$(stat -c %U:%G /etc/credstore.encrypted/agent-t3.model-key)\" = root:root ] && [ \$(( 0\$(stat -c %a /etc/credstore.encrypted/agent-t3.model-key) & 077 )) -eq 0 ]"
t3_t0=$(date +%s)
systemctl reset-failed agent@t3.service >/dev/null 2>&1
check "with it, t3 starts ($tpm)" bash -c "systemctl restart agent@t3.service && sleep 2 && systemctl is-active -q agent@t3.service"
check "systemd set up t3's credentials (no CREDENTIALS failure)" not bash -c \
	"journalctl -u agent@t3.service --since '@$t0' -o cat --no-pager | grep -q 'Failed to set up credentials'"
check "t3 answers on its socket" bash -c "$(declare -f api); api t3 /v1/status | jq -e '.contract == 1' >/dev/null"
check "no ConfigurationDirectory mode warning" not bash -c \
	"journalctl -u agent@t3.service --since '@$t0' -o cat --no-pager | grep -q 'ConfigurationDirectory.*mode is different'"
# SELinux on the credential (the SELinux section above ran before this
# one). The base policy does not audit every denial here: that t3 starts
# and answers is the real check.
t3_avc=$(ausearch -m AVC,USER_AVC,SELINUX_ERR -ts "$(date -d "@$t3_t0" '+%x %T')" 2>/dev/null | grep -E 'denied|SELINUX_ERR' || true)
[ -n "$t3_avc" ] && printf '  %s\n' "$t3_avc" | head -20
check "no SELinux denial while t3 reads its credential" test -z "$t3_avc"
check "agent-admin delete t3" agent-admin delete t3

section "A real model"
# An agent on a real model (--model): it must choose the tools itself. A
# small model does not always: each request is tried up to three times.
if [ -z "$model_url" ]; then
	skip "an agent on a real model (no --model)"
else
	agent-admin delete tm >/dev/null 2>&1
	cat >"$work/tm.yaml" <<EOF
version: 1
name: tm
description: functional test agent on a real model
model: {provider: openai, name: "$model_name", endpoint: "$model_url", temperature: 0}
instruction: |
  You test a system. When asked to use a tool, call exactly that tool once, then say in one line what it returned. /no_think
gateway: {servers: [custos-test]}
limits: {max_llm_calls: 6, max_tool_calls: 3, max_invocation_seconds: 300}
interaction: {agui: true, client_tools: deny}
EOF
	check "agent-admin create tm (model $model_name at $model_url)" agent-admin create tm --from "$work/tm.yaml"
	printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change}' |
		agent-admin permissions tm >/dev/null
	check "the model server answers" curl -sf -m 30 "${model_url%/}/models"
	# real_call <tool>: an activity in which the model called the tool
	# (its tool.call record), up to three tries; prints the activity.
	real_call() {
		local i id
		for i in 1 2 3; do
			id=$(start_activity tm "Use the $1 tool now.")
			[ -n "$id" ] || continue
			eventually 240 bash -c "$(declare -f api activity); s=\$(activity tm $id | jq -r .state); [ \"\$s\" != running ]" || true
			if journal_has tm tool.call ".AGENT_INVOCATION == \"$id\" and .AGENT_TOOL == \"custos-test__$1\""; then
				echo "$id"
				return 0
			fi
			echo "    | try $i: the model did not call $1: $(activity tm "$id" | jq -c '{state, summary, reason}')" >&2
		done
		return 1
	}
	if probe_id=$(real_call probe); then
		check "the model calls the read-only tool, and it runs without an approval" \
			journal_has tm tool.result ".AGENT_INVOCATION == \"$probe_id\" and .AGENT_TOOL == \"custos-test__probe\" and .AGENT_OUTCOME == \"ok\""
		check "the activity finishes" eventually 120 activity_state_is tm "$probe_id" finished
	else
		check "the model calls the read-only tool" false
	fi
	# The change: up to three tries until the model asks for it.
	change_id=
	for try in 1 2 3; do
		id=$(start_activity tm "Use the change tool now with value airgap.")
		if [ -n "$id" ] && eventually 240 bash -c "$(declare -f api activity); s=\$(activity tm $id | jq -r .state); [ \"\$s\" != running ]" &&
			activity_state_is tm "$id" waiting_approval; then
			change_id=$id
			break
		fi
		echo "    | try $try: the model did not ask for the change: $(activity tm "${id:-none}" 2>/dev/null | jq -c '{state, summary, reason}')"
	done
	if [ -n "$change_id" ]; then
		check "the model's change waits for an approval" true
		aid=$(approval_of tm "$change_id")
		gw "/v1/approvals/$aid" -X POST -d '{"decision":"approve","scope":"once"}' >/dev/null
		check "approved, the change runs and the activity finishes" eventually 240 activity_state_is tm "$change_id" finished
		check "the change ran" journal_has tm tool.result ".AGENT_INVOCATION == \"$change_id\" and .AGENT_TOOL == \"custos-test__change\" and .AGENT_OUTCOME == \"ok\""
	else
		check "the model's change waits for an approval" false
	fi
	check "agent-admin delete tm" agent-admin delete tm
fi

section "Diagnostic playbooks"
# CONTRACT.md §14c: published playbooks; an activity runs one through the
# gateway, refused when the gateway does not offer a step's tool.
check "agent-admin playbooks lists the shipped set, none broken" bash -c \
	"agent-admin playbooks | jq -e '(keys | length) == 11 and .\"failed-units\".title and ([.[] | select(.error)] | length == 0)' >/dev/null"
if [ -x /usr/libexec/mcp-servers/mcp-server-exec ]; then
	check "the shipped diagnostic commands pass mcp-server-exec --check" \
		/usr/libexec/mcp-servers/mcp-server-exec --check --commands /usr/share/custos/templates/exec.d-diagnostics.yaml
else
	skip "the shipped diagnostic commands pass mcp-server-exec --check (mcp-gateway-exec-server is not installed)"
fi
mkdir -p /etc/custos/playbooks
cat >/etc/custos/playbooks/probe-check.yaml <<'EOF'
probe-check:
  title: PROBE REPORT check
  description: The test server's probe.
  steps:
    - {tool: custos-test__probe, why: "The probe."}
  look_for: probe ok.
EOF
cat >/etc/custos/playbooks/change-check.yaml <<'EOF'
change-check:
  title: Change check
  steps:
    - {tool: custos-test__change, why: "Not offered to pb."}
  look_for: nothing.
EOF
agent-admin delete pb >/dev/null 2>&1
check "agent-admin create pb, with a schedule that runs a playbook" agent-admin create pb --from \
	"$(config pb custos-test 'schedules: [{name: probe, on_calendar: "*-*-* 03:30", playbook: probe-check}]')"
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' | agent-admin permissions pb
pb_run() { api pb /v1/activities -X POST -d "$(jq -nc --arg p "$1" '{playbook: $p, wait: true}')"; }
check "an activity runs the playbook: its probe is called" eventually 30 bash -c \
	"$(declare -f api pb_run); pb_run probe-check | jq -e '.playbook == \"probe-check\" and .state == \"finished\" and (.calls | map(.tool) | index(\"custos-test__probe\"))' >/dev/null"
check "journal: invocation.start names the playbook" eventually 10 journal_has pb invocation.start '.AGENT_PLAYBOOK == "probe-check"'
# The diagnosis report (§4a): the host verifies each quote against the
# output of the tool it names; feedback is recorded outside the agent.
pb_act=$(api pb /v1/activities | jq -r '[.activities[] | select(.playbook == "probe-check" and .report)][0].id // empty')
check "the activity has the playbook's diagnosis report" test -n "$pb_act"
check "the host verified the quote that is in the probe's output, and only that one" bash -c \
	"$(declare -f api); api pb /v1/activities/$pb_act | jq -e '.report | .kind == \"diagnosis\" and .playbook == \"probe-check\" and .hypotheses[0].confidence == \"high\" and
		(.evidence[0] | .verified and (.call_id | length > 0)) and (.evidence[1].verified | not)' >/dev/null"
check "journal: diagnosis.report with the verified evidence" eventually 10 journal_has pb diagnosis.report \
	".AGENT_INVOCATION == \"$pb_act\" and (.AGENT_REPORT | fromjson | .evidence[0].verified)"
check "agent-admin feedback on the report" agent-admin feedback pb "$pb_act" --hypothesis h1=confirmed --plan useful --executed --comment "functional test"
check "journal: diagnosis.feedback by root" eventually 10 bash -c \
	"journalctl -o json --no-pager --since '@$t0' SYSLOG_IDENTIFIER=custos-promotion AGENT_EVENT=diagnosis.feedback AGENT_NAME=pb | jq -e 'select(.AGENT_INVOCATION == \"$pb_act\" and .AGENT_BY == \"root\" and (.AGENT_FEEDBACK | fromjson | .hypotheses.h1 == \"confirmed\"))' >/dev/null"
check "agent-admin feedback refuses a hypothesis the report does not have" not agent-admin feedback pb "$pb_act" --hypothesis h9=confirmed
check "a playbook whose tool the gateway does not offer is refused (422)" code_is 422 api pb /v1/activities -X POST -d '{"playbook":"change-check"}'
check "an unknown playbook is refused (404)" code_is 404 api pb /v1/activities -X POST -d '{"playbook":"no-such"}'
n0=$(journal pb invocation.start | jq -s 'map(select(.AGENT_ORIGIN == "schedule")) | length')
check "the schedule runs its playbook" bash -c "agent-admin trigger pb_probe"
check "journal: the scheduled activity ran the playbook" eventually 30 bash -c \
	"[ \$(journalctl -o json --no-pager --since '@$t0' AGENT_NAME=pb AGENT_EVENT=invocation.start | jq -s 'map(select(.AGENT_ORIGIN == \"schedule\" and .AGENT_PLAYBOOK == \"probe-check\")) | length') -gt $n0 ]"
check "agent-admin delete pb" agent-admin delete pb
rm -f /etc/custos/playbooks/probe-check.yaml /etc/custos/playbooks/change-check.yaml

section Removing
check "agent-admin delete t2" agent-admin delete t2
check "agent-admin delete t1" agent-admin delete t1
check "t1's configuration is gone" not test -e /etc/custos/agents/t1
check "t1's gateway data is gone" not test -e /etc/mcp-gateway/policy/agents/t1.yaml
check "t1's state is gone" not test -e /var/lib/agents/t1
check "t1's socket directory is gone (the Cockpit page lists /run/agents)" not test -e /run/agents/t1
check "t1's schedule is gone" not systemctl is-enabled -q 'agent-schedule@t1_check.timer'

section "Diagnostics-only (release gate)"
# CONTRACT.md §6b: on a diagnostics-only host only the diagnostics
# category is usable, enforced by the gateway; enable is the one action
# out of it, and its result is the broader install's.
# opa_effect <agent> <tool>: the decision of the gateway's OPA for a call
# of custos-test's tool by the agent (the input the gateway gives it).
opa_effect() {
	jq -nc --arg a "agent-$1" --arg t "$2" '{input: {principal: {sub: $a, uid: 990, groups: [$a, "mcp-users"], transport: "unix", session_id: "s"},
		action: "tools.call", resource: {server: "custos-test", kind: "tool", name: $t}, args: {}, grants: []}}' |
		curl -sS --max-time 10 --unix-socket /run/mcp-gateway/opa.sock http://opa/v1/data/mcp/authz/decision -d @- | jq -r '.result.effect // "none"'
}
effect_is() { [ "$(opa_effect "$1" "$2")" = "$3" ]; }
# change_asks <agent>: a CHANGE waits for an approval, which is declined.
change_asks() {
	local id aid
	id=$(start_activity "$1" CHANGE)
	eventually 30 activity_state_is "$1" "$id" waiting_approval || return 1
	aid=$(approval_of "$1" "$id")
	gw "/v1/approvals/$aid" -X POST -d '{"decision":"deny","scope":"once"}' >/dev/null
	eventually 30 activity_state_is "$1" "$id" finished
}
dg_perms() { printf '%s\n' '- {server: custos-test, tool: probe, read_only: true, category: diagnostics}' "$@" | agent-admin permissions dg; }
enablement_is() { agent-admin show | jq -e ".enablement | $1" >/dev/null; }
promotion_record() { journalctl -o json --no-pager --since "@$t0" SYSLOG_IDENTIFIER=custos-promotion "AGENT_EVENT=$1" | jq -e "select($2)" >/dev/null; }
enablement=/etc/mcp-gateway/policy/agents/_enablement.yaml
dgdata=/etc/mcp-gateway/policy/agents/dg.yaml
check "the catalog has diagnostics and compliance-audit" bash -c "agent-admin categories | jq -e '.diagnostics.title and .\"compliance-audit\".title' >/dev/null"
check "no enablement state before: every category enabled" not test -e "$enablement"
agent-admin delete dg >/dev/null 2>&1
check "agent-admin create dg" agent-admin create dg --from "$(config dg custos-test)"
check "diagnostics refuses a permission that is not read-only" not dg_perms '- {server: custos-test, tool: change, category: diagnostics}'
check "dg gets a diagnostics permission" dg_perms
check "agent-admin diagnostics-only" agent-admin diagnostics-only
check "show: diagnostics-only, recorded, not tampered" enablement_is \
	'.state == "diagnostics-only" and .enabled == ["diagnostics"] and (.record | startswith("n-")) and (has("tampered") | not)'
check "journal: categories.enabled by root" eventually 10 promotion_record categories.enabled '.AGENT_BY == "root" and (.AGENT_ENABLEMENT_TO | contains("diagnostics-only"))'
check "diagnostics: PROBE runs" eventually 30 runs_with dg PROBE 'probe ok'
check "agent-admin refuses a permission of a disabled category" not dg_perms '- {server: custos-test, tool: change, category: patch-errata}'
check "agent-admin refuses a permission without a category" not dg_perms '- {server: custos-test, tool: change}'
check "journal: category.refused, with the person" eventually 10 promotion_record category.refused '.AGENT_NAME == "dg" and .AGENT_CATEGORY == "patch-errata" and .AGENT_BY == "root"'
check "agent-admin refuses a template of a disabled category" bash -c "agent-admin create pp --template patch-pilot 2>&1 | grep -q 'patch-errata.* not enabled'"
check "no agent pp was created" not test -e /etc/custos/agents/pp
# The bundled diagnostics agent's template is what a diagnostics-only host
# can create (§20b): read-only, in diagnostics, with its schedules.
agent-admin delete hc >/dev/null 2>&1
check "agent-admin create hc --template health-check on a diagnostics-only host" agent-admin create hc --template health-check
check "hc: supervised, every permission read-only in diagnostics" bash -c \
	"agent-admin show hc | jq -e '.agents.hc | .mode == \"supervised\" and (.permissions | length > 0) and all(.permissions[]; .read_only == true and .category == \"diagnostics\")' >/dev/null"
check "hc's schedules run playbooks: their timers are enabled" bash -c \
	"for s in failed-units disk-pressure journal-errors update-status; do systemctl is-enabled -q agent-schedule@hc_\$s.timer || exit 1; done"
check "agent-admin delete hc" agent-admin delete hc
# The gate: the gateway refuses the category even with its permission in
# the agent's data, written past agent-admin as an administrator might.
cp "$dgdata" "$work/dg.yaml"
printf '%s\n' 'dg:' '  mode: supervised' '  permissions:' '    - {server: custos-test, tool: probe, read_only: true, category: diagnostics}' \
	'    - {server: custos-test, tool: change, category: patch-errata}' >"$dgdata"
check "the gateway's OPA denies the disabled category's call" eventually 30 effect_is dg change deny
check "the gateway's OPA allows the diagnostics call" effect_is dg probe allow
check "the gateway hides the disabled category's tool from the agent" eventually 30 runs_with dg CHANGE 'not offered: custos-test__change'
# Tampering shows.
cp "$enablement" "$work/enablement.yaml"
sed -i 's/state: diagnostics-only/state: categories/' "$enablement"
check "show reports an enablement state changed by hand" enablement_is '.tampered | length > 0'
cp "$work/enablement.yaml" "$enablement"
check "show: the restored state matches its record" enablement_is 'has("tampered") | not'
# Enabling: one action, recorded; the category starts supervised.
check "agent-admin enable patch-errata" agent-admin enable patch-errata
check "journal: categories.enabled from diagnostics-only" eventually 10 promotion_record categories.enabled '.AGENT_ENABLEMENT_FROM | contains("diagnostics-only")'
check "the gateway's OPA asks for the enabled category's call" eventually 30 effect_is dg change ask
check "enabled, the category starts supervised: its change waits for an approval" change_asks dg
# Every category enabled decides as a host without enablement state (the
# broader install).
check "agent-admin enable --all" agent-admin enable --all
all="$(opa_effect dg probe) $(opa_effect dg change)"
rm -f "$enablement"
broader() { [ "$(opa_effect dg probe) $(opa_effect dg change)" = "$all" ] && [ "$all" = "allow ask" ]; }
sleep 2
check "every category enabled decides as the broader install: $all" eventually 30 broader
cp "$work/dg.yaml" "$dgdata"
check "agent-admin delete dg" agent-admin delete dg

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
