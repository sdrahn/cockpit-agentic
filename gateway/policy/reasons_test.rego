# The reasons of the agents module's denials (mcp-gateway's deny_reasons
# and unmatched_reasons, its requirement R1): what the agent, the audit
# and the administrator read instead of "denied by policy" or "no
# matching permission".
package mcp.authz_reasons_test

import data.mcp.authz
import data.mcp.authz_agents_test as t
import rego.v1

no_bindings := {"users": {}, "groups": {}}

reason(agents, principal, server, tool) := r if {
	d := authz.decision with input as t.call(principal, server, tool, {})
		with data.mcp.agents as agents
		with data.mcp.rbac.bindings as no_bindings
	d.effect == "deny"
	r := d.reason
}

test_paused if {
	a := {"patch-pilot": object.union(t.agents("bounded")["patch-pilot"], {"paused": true})}
	reason(a, t.pp, "zypp", "list_updates") == "the agent is paused (agent-admin resume)"
}

test_emergency_stop_and_paused_both_named if {
	a := object.union({"patch-pilot": object.union(t.agents("bounded")["patch-pilot"], {"paused": true})}, {"_control": {"stopped": true}})
	reason(a, t.pp, "zypp", "list_updates") == "the agent is paused (agent-admin resume); the emergency stop is in effect (agent-admin resume-all)"
}

test_no_agent_data if {
	reason({}, t.pp, "zypp", "list_updates") == "the agent has no agent data (agent-admin create)"
}

test_unknown_modes if {
	reason({"patch-pilot": {"mode": "yolo", "permissions": []}}, t.pp, "zypp", "list_updates") == "the agent's mode yolo is unknown"
	a := {"patch-pilot": {"mode": "supervised", "permissions": [], "categories": {"patch-errata": {"mode": "free"}}}}
	reason(a, t.pp, "zypp", "list_updates") == "the mode free of task category patch-errata is unknown"
}

test_bound_role if {
	r := authz.decision.reason with input as t.call(t.pp, "zypp", "list_updates", {})
		with data.mcp.agents as t.agents("bounded")
		with data.mcp.rbac.bindings as {"users": {"agent-patch-pilot": ["admin"]}, "groups": {}}
	r == "a role is bound to the agent's user: an agent's permissions are only its agent data"
}

test_untrusted_issuer if {
	a := object.union(t.agents("bounded"), {"_http": {"issuers": ["https://agents.example"]}})
	reason(a, t.pp_http("https://idp.example"), "zypp", "list_updates") == "an agent's subject from an issuer not trusted for agents"
}

# Diagnostics-only (§6b): a call a permission of a disabled category
# names says so; one no permission names stays "no matching permission".
test_category_not_enabled if {
	a := object.union(t.diag_agents, {"_enablement": {"state": "diagnostics-only", "enabled": ["diagnostics"]}})
	reason(a, t.pp, "zypp", "patch") == "task category patch-errata is not enabled on this host (diagnostics-only): agent-admin enable patch-errata"
	reason(a, t.pp, "systemd", "restart_unit") == "task category config-lifecycle is not enabled on this host (diagnostics-only): agent-admin enable config-lifecycle"
	reason(a, t.pp, "zypp", "list_updates") == "permissions without a task category are not enabled on this host (diagnostics-only)"
	reason(a, t.pp, "fs", "read_file") == "no matching permission"
}

test_category_enabled_since if {
	a := object.union(t.diag_agents, {"_enablement": {"state": "categories", "enabled": ["diagnostics", "patch-errata"]}})
	reason(a, t.pp, "systemd", "restart_unit") == "task category config-lifecycle is not enabled on this host (categories): agent-admin enable config-lifecycle"
	authz.decision.effect == "ask" with input as t.call(t.pp, "zypp", "patch", {})
		with data.mcp.agents as a
		with data.mcp.rbac.bindings as no_bindings
}

test_diagnostics_that_changes if {
	reason(t.diag_agents, t.pp, "systemd", "get_unit_log") == "a diagnostics permission that is not read-only is no permission (diagnostics only reads)"
}

test_advisory if {
	reason(t.agents("advisory"), t.pp, "zypp", "install_patches") == "the agent is advisory: it only reads (agent-admin mode)"
	reason(t.agents("advisory"), t.pp, "fs", "read_file") == "no matching permission"
}

# People are not agents: their denials read as before.
test_humans_unaffected if {
	alice := {"sub": "alice", "groups": [], "transport": "unix", "session_id": "s1"}
	a := object.union(t.agents("advisory"), {"_control": {"stopped": true}, "_enablement": {"state": "diagnostics-only"}})
	reason(a, alice, "zypp", "install_patches") == "no matching permission"
}
