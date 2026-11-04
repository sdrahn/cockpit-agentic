#!/bin/bash
# The OpenShell backend (CONTRACT.md §3a, §16a) with a real NVIDIA
# OpenShell: an agent created with agent-admin runs in an OpenShell
# sandbox, reaches mcp-gateway's HTTPS listener on the host with an OAuth
# access token that only OpenShell's supervisor holds, and is supervised
# through its relayed socket. CI runs it on a GitHub runner with Docker
# (job openshell); it needs root, systemd, Docker, the openshell CLI with
# a local gateway, opa, the binaries from `make build build-tests`, and an
# mcp-gateway checkout built with `make build` (MCPGW_SRC), and the user
# whose local OpenShell gateway it is (OPENSHELL_GATEWAY_USER).
#
# Usage: MCPGW_SRC=DIR OPENSHELL_GATEWAY_USER=USER test/openshell/run-spike.sh --disposable [--results DIR]
#
# mcp-gateway runs in its development mode (no systemd, no SELinux; the
# exec supervisor) with its own policy, custos's agents module
# and the agents' data from agent-admin, as installed. Its HTTPS listener
# trusts test/vm/testidp; agents/_http.yaml lists that issuer for agents.
# agent-sb is an OAuth client there: OpenShell's gateway mints its tokens
# (subject agent-sb, no local-user claim, 90 s) with the client
# credentials grant and renews them while the agent runs.
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
check() { local d=$1; shift; if "$@"; then pass "$d"; else fail "$d"; return 1; fi; }
section() { printf '\n=== %s\n' "$*"; current_section=$*; }
eventually() { local deadline=$((SECONDS + $1)); shift; until "$@"; do [ "$SECONDS" -ge "$deadline" ] && return 1; sleep 1; done; }
not() { ! "$@"; }
json_ok() { jq -e "$2" >/dev/null 2>&1 <<<"$1"; }
api() { local p=$1; shift; curl -sS --max-time 180 --unix-socket /run/agents/sb/api.sock "http://agent$p" "$@"; }
# gw <path> [curl args...]: the gateway's control API.
gw() { local p=$1; shift; curl -sS --max-time 30 --unix-socket "$work/gw/control.sock" "http://localhost$p" "$@"; }
# gw_audit_has <jq condition>: the gateway's audit records since the start.
gw_audit_has() { journalctl -u osh-mcp-gateway.service -o cat --no-pager --since "@$t0" | grep '"audit":true' | jq -e "select($1)" >/dev/null 2>&1; }
activity() { api "/v1/activities/$1"; }
run_activity() { api /v1/activities -X POST -d "$(jq -nc --arg p "$1" '{prompt: $p, wait: true}')"; }
summary_has() { grep -qF -- "$2" <<<"$(jq -r .summary <<<"$1")"; }
journal_has() { journalctl -o json --no-pager --since "@$t0" AGENT_NAME=sb "AGENT_EVENT=$1" 2>/dev/null | jq -e "select($2)" >/dev/null 2>&1; }
osh() { openshell -g openshell "$@"; }

section Preconditions
check "openshell is installed" command -v openshell
check "the local OpenShell gateway answers" eventually 60 osh status
check "docker runs" bash -c "docker info >/dev/null"
mcpgw=${MCPGW_SRC:-}
check "an mcp-gateway checkout is built (MCPGW_SRC)" test -x "$mcpgw/bin/mcp-gateway" -a -d "$mcpgw/policy/mcp"
check "opa is installed" command -v opa

section Install
# What the package installs (packaging/suse), into this system.
make -s -C "$here" install install-tests DESTDIR= PREFIX=/usr SYSCONFDIR=/etc UNITDIR=/etc/systemd/system \
	SYSUSERSDIR=/usr/lib/sysusers.d TMPFILESDIR=/usr/lib/tmpfiles.d SBINDIR=/usr/sbin >/dev/null
getent group mcp-users >/dev/null || groupadd -r mcp-users
systemd-sysusers /usr/lib/sysusers.d/custos.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/custos.conf
systemctl daemon-reload
check "agent-admin is installed" command -v agent-admin
check "the relay is installed" test -x /usr/libexec/custos/agent-openshell-relay
check "its log source is installed" test -x /usr/libexec/custos/agent-openshell-logs

section "Model on the host"
systemd-run --unit=osh-fakemodel /usr/libexec/custos/tests/fakemodel --listen 0.0.0.0:8008 >/dev/null

section "mcp-gateway on the host (HTTPS)"
# The agent reaches the host as host.openshell.internal (OpenShell's
# Docker driver: the host's loopback), so that is the certificate's name
# and the gateway's audience.
aud=https://host.openshell.internal:18443/mcp
issuer=http://127.0.0.1:18445
mkdir -p "$work/gw/servers.d" "$work/gw/rbac" "$work/tls" /run/mcp-gateway
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj /CN=custos-test-ca \
	-addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign \
	-keyout "$work/tls/ca.key" -out "$work/tls/ca.pem" 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=host.openshell.internal \
	-keyout "$work/tls/key.pem" -out "$work/tls/req.pem" 2>/dev/null
printf 'subjectAltName=DNS:host.openshell.internal,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' >"$work/tls/ext.cnf"
openssl x509 -req -in "$work/tls/req.pem" -CA "$work/tls/ca.pem" -CAkey "$work/tls/ca.key" -CAcreateserial -days 2 \
	-extfile "$work/tls/ext.cnf" -out "$work/tls/cert.pem" 2>/dev/null
check "the gateway's certificate names host.openshell.internal" bash -c "openssl x509 -in '$work/tls/cert.pem' -noout -ext subjectAltName | grep -q DNS:host.openshell.internal"

# agent-sb's OAuth client; its tokens live 90 s, so the agent outlives one.
head -c 24 /dev/urandom | base64 | tr -d '/+=' >"$work/client-secret"
systemd-run --unit=osh-testidp /usr/libexec/custos/tests/testidp --listen 127.0.0.1:18445 --ttl 90 \
	--client "agent-sb:$(cat "$work/client-secret"):agent-sb:$aud" >/dev/null
check "the token issuer answers" eventually 20 curl -sf -o /dev/null "$issuer/.well-known/openid-configuration"
# Its subjects agent-<name> are agents (agents/_http.yaml, CONTRACT.md §16a).
printf '_http:\n  issuers: ["%s"]\n' "$issuer" >/etc/mcp-gateway/policy/agents/_http.yaml

# OPA as mcp-opa.service runs it: the gateway's policy, the agents module,
# the agents' data (agent-admin) and the gateway's default role data.
cp "$mcpgw/policy/mcp/rbac/data.yaml" "$work/gw/rbac/data.yaml"
systemd-run --unit=osh-opa opa run --server --addr unix:///run/mcp-gateway/opa.sock --watch --log-format json \
	--ignore rbac --ignore '*.json' "$mcpgw/policy" /usr/share/mcp-gateway/policy/custos \
	mcp:/etc/mcp-gateway/policy "mcp.rbac:$work/gw/rbac" >/dev/null
check "OPA has the agents' issuer" eventually 30 bash -c "curl -s --unix-socket /run/mcp-gateway/opa.sock http://opa/v1/data/mcp/agents/_http/issuers | grep -qF '$issuer'"

printf '%s\n' 'version: 1' 'name: custos-test' 'command: ["/usr/libexec/custos/tests/testsrv"]' 'run_as: gateway' \
	>"$work/gw/servers.d/custos-test.yaml"
printf '%s\n' "socket: $work/gw/mcp.sock" "servers_dir: $work/gw/servers.d" "state_dir: $work/gw/state" \
	"approvals:" "  control_socket: $work/gw/control.sock" \
	"policy:" "  opa_socket: /run/mcp-gateway/opa.sock" \
	"supervisor:" "  mode: exec" \
	"http:" '  listen: ":18443"' "  cert_file: $work/tls/cert.pem" "  key_file: $work/tls/key.pem" \
	"  issuer: $issuer" "  audience: $aud" >"$work/gw/gateway.yaml"
systemd-run --unit=osh-mcp-gateway "$mcpgw/bin/mcp-gateway" --config "$work/gw/gateway.yaml" >/dev/null
check "the gateway's HTTPS listener refuses a request without a token (401)" eventually 30 bash -c \
	"curl -s -o /dev/null -w '%{http_code}' --cacert '$work/tls/ca.pem' --resolve host.openshell.internal:18443:127.0.0.1 -X POST $aud | grep -qx 401" ||
	journalctl -u osh-mcp-gateway.service -o cat --no-pager --since "@$t0" | tail -n 15 | sed 's/^/    | gateway: /'
check "the gateway takes the test server" eventually 30 bash -c "curl -s --unix-socket '$work/gw/control.sock' http://localhost/v1/servers | grep -q custos-test"
check "the issuer gives agent-sb's client a token" bash -c \
	"curl -sf -X POST -d grant_type=client_credentials -d client_id=agent-sb --data-urlencode client_secret=\$(cat '$work/client-secret') '$issuer/token' | jq -e '.access_token != null' >/dev/null"
minted() { curl -s "$issuer/minted" | jq -r '."agent-sb" // 0'; }

section "OpenShell trusts the gateway's CA"
# OpenShell's proxy verifies the gateway in each sandbox's supervisor,
# not with the image's trust store: the CA goes into the OpenShell
# gateway's configuration (CONTRACT.md §16a), here the local gateway of
# OPENSHELL_GATEWAY_USER (systemctl --user, ~/.config/openshell).
osh_user=${OPENSHELL_GATEWAY_USER:-}
osh_home=$(getent passwd "$osh_user" | cut -d: -f6)
osh_toml=$osh_home/.config/openshell/gateway.toml
check "the OpenShell gateway's user is known (OPENSHELL_GATEWAY_USER)" test -n "$osh_user" -a -d "$osh_home/.config/openshell"
install -m 0644 -o "$osh_user" "$work/tls/ca.pem" "$osh_home/.config/openshell/mcp-gateway-ca.pem"
# A gateway.toml must say its format (OpenShell 0.1.7: version 2); the
# package does not ship one for a local gateway.
[ -s "$osh_toml" ] || { printf '[openshell]\nversion = 2\n' >"$osh_toml" && chown "$osh_user" "$osh_toml"; }
if grep -q '^\[openshell\.supervisor\.network\]' "$osh_toml"; then
	sed -i "/^\[openshell\.supervisor\.network\]/a additional_ca_bundle = \"$osh_home/.config/openshell/mcp-gateway-ca.pem\"" "$osh_toml"
else
	printf '\n[openshell.supervisor.network]\nadditional_ca_bundle = "%s"\n' "$osh_home/.config/openshell/mcp-gateway-ca.pem" >>"$osh_toml"
fi
check "OpenShell's configuration check takes it" runuser -u "$osh_user" -- env HOME="$osh_home" XDG_CONFIG_HOME="$osh_home/.config" openshell-gateway config preflight ||
	{
		runuser -u "$osh_user" -- env HOME="$osh_home" XDG_CONFIG_HOME="$osh_home/.config" openshell-gateway config preflight 2>&1 | tail -n 15 | sed 's/^/    | preflight: /'
		sed 's/^/    | gateway.toml: /' "$osh_toml"
	}
check "the OpenShell gateway restarts with the CA" systemctl --user --machine="$osh_user@" restart openshell-gateway ||
	systemctl --user --machine="$osh_user@" status --no-pager openshell-gateway 2>&1 | tail -n 15 | sed 's/^/    | /'
check "the OpenShell gateway answers again" eventually 90 osh status

section "Agent image"
img=localhost/custos-agent:ci
check "the agent image builds" docker build -q -t "$img" -f "$here/packaging/openshell/Containerfile" "$here"

section "Creating the agent"
cat >"$work/sb.yaml" <<EOF
version: 1
name: sb
description: OpenShell spike agent
model: {provider: openai, name: fake, endpoint: "http://127.0.0.1:8008/v1"}
instruction: You are a test agent.
gateway:
  url: "$aud"
  servers: [custos-test]
  oauth: {token_url: "$issuer/token", client_id: agent-sb, refresh_before_seconds: 30}
limits: {max_invocation_seconds: 300}
interaction: {agui: true, client_tools: deny}
sandbox: {kind: openshell, openshell_gateway: openshell}
EOF
agent-admin delete sb >/dev/null 2>&1
export CUSTOS_OPENSHELL_IMAGE=$img
check "agent-admin create sb (openshell)" agent-admin create sb --from "$work/sb.yaml"
printf '%s\n' '- {server: custos-test, tool: probe, read_only: true}' '- {server: custos-test, tool: change}' >"$work/perms.yaml"
check "agent-admin permissions sb (supervised: change needs an approval)" agent-admin permissions sb <"$work/perms.yaml"
check "the sandbox policy is written" test -s /etc/custos/agents/sb/openshell-policy.yaml
check "the provider profile is imported" eventually 10 bash -c 'openshell -g openshell profile list -o json 2>/dev/null | grep -q custos-gateway-sb'
n0=$(minted)
check "agent-admin set-credential sb gateway-client-secret (OpenShell mints the token, creates the sandbox)" \
	agent-admin set-credential sb gateway-client-secret <"$work/client-secret"
check "OpenShell's gateway minted agent-sb's first token" test "$(minted)" -gt "$n0"
check "OpenShell renews it (oauth2_client_credentials)" bash -c "openshell -g openshell provider refresh status agent-sb-gateway 2>&1 | grep -qi client.credentials" ||
	openshell -g openshell provider refresh status agent-sb-gateway 2>&1 | tail -n 10 | sed 's/^/    | /'
t_first=$SECONDS
check "the sandbox exists" eventually 120 bash -c 'openshell -g openshell sandbox get agent-sb >/dev/null 2>&1'
check "the relay socket is active" systemctl is-active -q agent-openshell@sb.socket

section "Supervising through the relay"
diagnose() {
	# Each step writes to a file, with a timeout and no stdin: a process a
	# step leaves running (forward start -d) must not hold a pipe open.
	local d=$work/diagnose port
	mkdir -p "$d"
	port=$(sed -n 's/^FORWARD_PORT=//p' /etc/custos/agents/sb/openshell.env)
	step() { # step <title> <command...>
		echo "--- $1"
		shift
		timeout -k 5 40 "$@" </dev/null >"$d/out" 2>&1
		tail -n 25 "$d/out"
	}
	{
		step "relay units" systemctl status --no-pager agent-openshell@sb.socket agent-openshell@sb.service
		step "relay journal" bash -c "journalctl --no-pager -o short-precise --since @$t0 -u agent-openshell@sb.service | grep -v 'proxy error'"
		step "openshell forward start $port agent-sb -d (by hand)" openshell -g openshell forward start "$port" agent-sb -d
		step "the agent, started by hand in the sandbox for 10 s" openshell -g openshell sandbox exec -n agent-sb -- /bin/sh -c \
			'timeout 10 /usr/libexec/custos/agent-launcher serve --config-env CUSTOS_CONFIG --listen-tcp 127.0.0.1:47999 --idle-exit 0; echo "exit status $?"'
		step "openshell sandbox get agent-sb" openshell -g openshell sandbox get agent-sb
		step "openshell forward list" openshell -g openshell forward list
		step "openshell logs agent-sb" openshell -g openshell logs agent-sb
		step "containers (the relay's log source)" bash -c "docker ps --no-trunc --format '{{.ID}} {{.Names}} {{.Image}}'"
		step "agent-openshell-logs agent-sb" timeout 5 /usr/libexec/custos/agent-openshell-logs agent-sb
		step "listening on the host" bash -c "ss -ltnp | grep -E ':47[0-9]{3}\\b'"
	} >"$d/all" 2>&1
	echo "    | --- why the relay gets no answer"
	sed 's/^/    | /' "$d/all"
}
if eventually 180 bash -c 'curl -sf --max-time 10 --unix-socket /run/agents/sb/api.sock http://agent/v1/status >/dev/null'; then
	pass "the agent answers through the relay"
	relay_ok=1
else
	fail "the agent answers through the relay"
	diagnose
	relay_ok=
fi
check "the relay unit's openshell knows the gateway (HOME=/root)" not bash -c "journalctl --no-pager -o cat --since @$t0 -u agent-openshell@sb.service | grep -q 'Unknown gateway'"
if [ -n "$relay_ok" ]; then
	st=$(api /v1/status)
	check "status through the relay says lifecycle openshell" json_ok "$st" '.lifecycle.kind == "openshell" and .lifecycle.sandbox == "agent-sb"'
	check "the relay passed root on (status is for supervisors)" json_ok "$st" '.contract == 1'
	a=$(run_activity PROBE)
	check "a read-only call runs" summary_has "$a" "probe ok" ||
		{
			echo "    | the agent answered: $(jq -c '{state, summary, reason, calls: [.calls[]? | {tool, state, reason}]}' <<<"$a" 2>/dev/null || head -c 400 <<<"$a")"
			journalctl --no-pager -o cat --since "@$t0" AGENT_NAME=sb 2>/dev/null | tail -n 15 | sed 's/^/    | agent: /'
			# Where OpenShell's proxy takes the gateway's CA from.
			echo "    | OpenShell's gateway.toml: $(grep -A2 '^\[openshell\.supervisor\.network\]' "$osh_toml" 2>&1 | tr '\n' ' ')"
			journalctl --user --machine="$osh_user@" -u openshell-gateway --no-pager -o cat --since "@$t0" 2>/dev/null | grep -iE 'additional|ca bundle|trust' | tail -n 5 | sed 's/^/    | openshell-gateway: /'
			timeout 5 /usr/libexec/custos/agent-openshell-logs agent-sb </dev/null 2>&1 | grep -iE '18443|certificate|tls|trust' | tail -n 6 | sed 's/^/    | supervisor: /'
		}
	check "the gateway audited the call as agent-sb over HTTPS" eventually 10 gw_audit_has '.sub == "agent-sb" and .name == "probe" and .effect == "allow"' ||
		journalctl -u osh-mcp-gateway.service -o cat --no-pager --since "@$t0" | tail -n 15 | sed 's/^/    | gateway: /'
	check "and refused no request of the agent's (placeholder or no token)" not bash -c \
		"journalctl -u osh-mcp-gateway.service -o cat --no-pager --since @$t0 | grep -iE 'invalid_token|missing bearer|no subject'"
	check "the sandbox's environment has no client secret" bash -c "! openshell -g openshell sandbox exec -n agent-sb -- env 2>/dev/null | grep -qF \"\$(cat $work/client-secret)\""

	section "Token renewal"
	# The first token expires 90 s after it was minted; OpenShell renews
	# 30 s before. A call after that runs on a renewed token, without
	# restarting the agent.
	m1=$(minted)
	starts() { journalctl -o cat --no-pager --since "@$t0" AGENT_NAME=sb AGENT_EVENT=agent.start 2>/dev/null | wc -l; }
	s1=$(starts)
	check "OpenShell mints a new token before the first expires" eventually 120 bash -c "[ \$(curl -s $issuer/minted | jq -r '.\"agent-sb\" // 0') -gt $m1 ]"
	while [ $((SECONDS - t_first)) -lt 95 ]; do sleep 5; done
	a=$(run_activity PROBE)
	check "a call after the first token expired runs (renewed token)" summary_has "$a" "probe ok" || {
		echo "    | the agent answered: $(jq -c '{state, summary, reason}' <<<"$a" 2>/dev/null || head -c 300 <<<"$a")"
		# Who answered 500: mcp-gateway, or OpenShell's proxy?
		journalctl -u osh-mcp-gateway.service -o cat --no-pager --since "-120s" | grep -v '"audit":true' | tail -n 12 | sed 's/^/    | gateway: /'
		timeout 5 /usr/libexec/custos/agent-openshell-logs agent-sb </dev/null 2>&1 | grep -iE '18443|credential|placeholder|refresh|resolve|500' | tail -n 12 | sed 's/^/    | supervisor: /'
		timeout 20 openshell -g openshell provider refresh status agent-sb-gateway </dev/null 2>&1 | tail -n 12 | sed 's/^/    | refresh: /'
		journalctl --user --machine="$osh_user@" -u openshell-gateway --no-pager -o cat --since "-300s" 2>/dev/null | grep -iE 'refresh|mint|credential|error|warn' | tail -n 10 | sed 's/^/    | openshell-gateway: /'
	}
	check "the agent was not restarted for it (no new agent.start)" test "$(starts)" -eq "$s1"
	check "and the gateway refused none of its requests" not bash -c \
		"journalctl -u osh-mcp-gateway.service -o cat --no-pager --since @$t0 | grep -iE 'invalid_token|token is expired|token_expired'"

	section "Approval"
	id=$(api /v1/activities -X POST -d '{"prompt":"CHANGE"}' | jq -r .id)
	check "the change waits for an approval" eventually 60 bash -c "curl -s --unix-socket /run/agents/sb/api.sock http://agent/v1/activities/$id | jq -e '.state == \"waiting_approval\"' >/dev/null"
	aid=$(activity "$id" | jq -r '[.calls[]? | select(.approval_id != null and .approval_id != "")][0].approval_id // empty')
	check "its call carries the gateway's approval id" test -n "$aid"
	check "the gateway lists the approval for agent-sb" json_ok "$(gw /v1/approvals)" ".[] | select(.id == \"$aid\" and .principal.sub == \"agent-sb\")"
	check "nothing ran before the decision" not gw_audit_has '.sub == "agent-sb" and .name == "change" and .effect == "allow"'
	gw "/v1/approvals/$aid" -X POST -d '{"decision":"approve","scope":"once"}' >/dev/null
	check "the activity finishes after the approval" eventually 60 bash -c "curl -s --unix-socket /run/agents/sb/api.sock http://agent/v1/activities/$id | jq -e '.state == \"finished\" and (.summary | contains(\"changed test\"))' >/dev/null"
	check "journal (from the sandbox's output): approval.pending" eventually 60 journal_has approval.pending ".AGENT_APPROVAL_ID == \"$aid\"" || {
		# Where the relay's bridge reads the agent's records from.
		echo "    | relay: $(journalctl --no-pager -o cat --since "@$t0" -u agent-openshell@sb.service 2>&1 | grep -v 'proxy error' | tail -n 8 | tr '\n' '|')"
		echo "    | containers: $(timeout 10 docker ps --format '{{.Names}} ({{.Image}})' </dev/null 2>&1 | tr '\n' ' ')"
		echo "    | helper: $(timeout 5 /usr/libexec/custos/agent-openshell-logs agent-sb </dev/null 2>&1 | tail -n 5 | tr '\n' '|')"
		echo "    | journal AGENT_NAME=sb: $(journalctl --no-pager -o cat --since "@$t0" AGENT_NAME=sb 2>&1 | wc -l) records"
	}
	check "journal: approval.resolved approved" eventually 60 journal_has approval.resolved '.AGENT_OUTCOME == "approved"'

	section "AG-UI through the relay"
	out=$(api /agui -N -X POST -d '{"threadId":"osh-1","messages":[{"role":"user","content":"PROBE"}]}')
	check "the stream comes through the relay" grep -q '"type":"RUN_FINISHED"' <<<"$out"

else
	skip "the checks through the relay (the agent did not answer through it)"
fi
section "Emergency stop"
check "agent-admin stop-all" agent-admin stop-all
check "the relay is stopped" not systemctl is-active -q agent-openshell@sb.socket
# The state, wherever OpenShell's JSON has it (a string or an object).
check "the sandbox is stopped" eventually 60 bash -c 'openshell -g openshell sandbox get agent-sb -o json 2>/dev/null | jq -e "[.status, .state, .phase] | tostring | ascii_downcase | test(\"stop|exit|terminat\")" >/dev/null' ||
	echo "    | OpenShell says: $(timeout 20 openshell -g openshell sandbox get agent-sb -o json </dev/null 2>&1 | jq -c 'del(.spec, .policy, .network_policies)' 2>&1 | head -c 600)"
check "agent-admin release-all" agent-admin release-all
check "the relay is back" systemctl is-active -q agent-openshell@sb.socket

section Removing
check "agent-admin delete sb" agent-admin delete sb
check "the sandbox is gone" eventually 60 not osh sandbox get agent-sb
systemctl stop osh-fakemodel osh-mcp-gateway osh-opa osh-testidp >/dev/null 2>&1

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
	openshell -g openshell logs agent-sb >"$results/sandbox.log" 2>&1 || true
fi
n_fail=$(printf '%s\n' "${results_list[@]}" | awk -F'\t' '$2 == "fail"' | wc -l)
printf '\n%d checks, %d failed\n' "${#results_list[@]}" "$n_fail"
exit "$failed"
