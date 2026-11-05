# Which calls the gateway's approvers review after they ran (mcp-gateway's
# review obligation, R4): a bounded category's autonomous calls, no others.
package mcp.authz_review_test

import data.mcp.authz
import rego.v1

pp := {"sub": "agent-patch-pilot", "uid": 990, "groups": ["agent-patch-pilot"], "transport": "unix", "session_id": "s1"}

call(principal, server, tool) := {
	"principal": principal, "action": "tools.call",
	"resource": {"server": server, "kind": "tool", "name": tool}, "args": {}, "grants": [],
}

agents(cat_mode) := {"patch-pilot": {
	"mode": "supervised",
	"permissions": [
		{"server": "zypp", "tool": "list_*", "read_only": true, "category": "patch-errata"},
		{"server": "zypp", "tool": "patch", "category": "patch-errata"},
		{"server": "systemd", "tool": "restart_unit", "category": "config-lifecycle"},
		{"server": "fs", "tool": "write_file"},
	],
	"categories": {"patch-errata": {"mode": cat_mode}},
}}

review(agents, principal, server, tool) := d.obligations.review if {
	d := authz.decision with input as call(principal, server, tool)
		with data.mcp.agents as agents
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

decision(agents, server, tool) := d if {
	d := authz.decision with input as call(pp, server, tool)
		with data.mcp.agents as agents
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
}

test_bounded_autonomous_call_is_reviewed if {
	d := decision(agents("bounded"), "zypp", "patch")
	d.effect == "allow"
	d.obligations.review == true
}

test_others_are_not if {
	# Read-only, asked for (supervised category, other category), and
	# without a category.
	not decision(agents("bounded"), "zypp", "list_updates").obligations.review
	not decision(agents("supervised"), "zypp", "patch").obligations.review
	not decision(agents("bounded"), "systemd", "restart_unit").obligations.review
	not decision(agents("bounded"), "fs", "write_file").obligations.review
}

test_outside_the_window_asks_and_is_not_reviewed if {
	a := {"patch-pilot": object.union(agents("bounded")["patch-pilot"], {"categories": {"patch-errata": {
		"mode": "bounded",
		"envelope": {"windows": [{"days": ["sat"], "from": "02:00", "to": "04:00"}]},
	}}})}
	d := authz.decision with input as call(pp, "zypp", "patch")
		with data.mcp.agents as a
		with data.mcp.rbac.bindings as {"users": {}, "groups": {}}
		with time.now_ns as time.parse_rfc3339_ns("2026-11-04T10:00:00Z") # a Wednesday
	d.effect == "ask"
	not d.obligations.review
}

test_people_are_not_reviewed if {
	alice := {"sub": "alice", "groups": ["wheel"], "transport": "unix", "session_id": "s1"}
	not review(agents("bounded"), alice, "zypp", "patch")
}
