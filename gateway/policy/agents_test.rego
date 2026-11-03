package mcp.authz_agents_test

import data.mcp.authz
import rego.v1

pp := {"sub": "agent-patch-pilot", "uid": 990, "groups": ["agent-patch-pilot", "mcp-users"], "transport": "unix", "session_id": "s1"}

call(principal, server, tool, args) := {
	"principal": principal,
	"action": "tools.call",
	"resource": {"server": server, "kind": "tool", "name": tool, "annotations": {"readOnlyHint": true}},
	"args": args,
	"grants": [],
}

agents(mode) := {"patch-pilot": {"mode": mode, "permissions": [
	{"server": "zypp", "tool": "list_*", "read_only": true},
	{"server": "zypp", "tool": "install_patches"},
	{"server": "systemd", "tool": "restart_unit", "autonomous": true, "args": {"unit": "^nginx\\.service$"}},
]}}

effect(mode, server, tool, args) := e if {
	e := authz.decision.effect with input as call(pp, server, tool, args)
		with data.mcp.agents as agents(mode)
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

test_advisory if {
	effect("advisory", "zypp", "list_updates", {}) == "allow"
	effect("advisory", "zypp", "install_patches", {}) == "deny"
	effect("advisory", "systemd", "restart_unit", {"unit": "nginx.service"}) == "deny"
}

test_supervised if {
	effect("supervised", "zypp", "list_updates", {}) == "allow"
	effect("supervised", "zypp", "install_patches", {}) == "ask"
	effect("supervised", "systemd", "restart_unit", {"unit": "nginx.service"}) == "ask"
	effect("supervised", "fs", "read_file", {}) == "deny"
}

test_supervised_asks_out_of_band if {
	d := authz.decision with input as call(pp, "zypp", "install_patches", {})
		with data.mcp.agents as agents("supervised")
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
	d.ask.channel == "oob"
}

test_default_mode_is_supervised if {
	a := {"patch-pilot": {"permissions": [{"server": "zypp", "tool": "install_patches"}]}}
	authz.decision.effect == "ask" with input as call(pp, "zypp", "install_patches", {})
		with data.mcp.agents as a
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

test_bounded if {
	effect("bounded", "systemd", "restart_unit", {"unit": "nginx.service"}) == "allow"
	effect("bounded", "systemd", "restart_unit", {"unit": "sshd.service"}) != "allow"
	effect("bounded", "zypp", "install_patches", {}) == "ask"
}

test_annotations_never_allow if {
	# The call's annotations say read-only; the permission does not.
	effect("advisory", "zypp", "install_patches", {}) == "deny"
}

test_paused_and_stopped if {
	a := {"patch-pilot": object.union(agents("bounded")["patch-pilot"], {"paused": true})}
	authz.decision.effect == "deny" with input as call(pp, "zypp", "list_updates", {})
		with data.mcp.agents as a
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
	authz.decision.effect == "deny" with input as call(pp, "zypp", "list_updates", {})
		with data.mcp.agents as agents("bounded")
		with data.mcp.agents._control as {"stopped": true}
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

test_bound_role_denies if {
	authz.decision.effect == "deny" with input as call(pp, "zypp", "list_updates", {})
		with data.mcp.agents as agents("bounded")
		with data.mcp.rbac.bindings as {"users": {"agent-patch-pilot": ["admin"]}, "groups": {}}
}

test_agent_without_data_denied if {
	authz.decision.effect == "deny" with input as call(pp, "zypp", "list_updates", {})
		with data.mcp.agents as {}
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

test_humans_unaffected if {
	alice := {"sub": "alice", "groups": ["wheel"], "transport": "unix", "session_id": "s1"}
	authz.decision.effect == "allow" with input as call(alice, "zypp", "install_patches", {})
		with data.mcp.agents as agents("advisory")
		with data.mcp.agents._control as {"stopped": true}
}
