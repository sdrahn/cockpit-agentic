package mcp.approvals_owners_test

import data.mcp.approvals
import rego.v1

req := {"principal": {"sub": "agent-patch-pilot", "uid": 981}, "server": "zypp", "name": "patch", "action": "tools.call"}

other := {"principal": {"sub": "agent-triage", "uid": 982}, "server": "zypp", "name": "patch", "action": "tools.call"}

alice := {"name": "alice", "uid": 1001, "groups": ["users"]}

bob := {"name": "bob", "uid": 1002, "groups": ["users", "web-team"]}

carol := {"name": "carol", "uid": 1003, "groups": ["users"]}

# The servers' own rules name only the admins.
rbac := {"approvers": {"default": ["role:admin"]}, "bindings": {"users": {}, "groups": {"wheel": ["admin"]}}}

agents := {"patch-pilot": {"mode": "supervised", "permissions": [], "owners": ["user:alice", "group:web-team", "self"]}}

allowed(approver, request) if {
	approvals.allow with input as {"approver": approver, "request": request}
		with data.mcp.rbac as rbac
		with data.mcp.agents as agents
}

test_owners_approve_their_agent if {
	allowed(alice, req)
	allowed(bob, req)
	not allowed(carol, req)
}

test_owners_approve_only_their_agent if {
	not allowed(alice, other)
	not allowed(bob, other)
}

# Not by "self": the agent's own uid grants nothing.
test_self_is_no_owner if {
	not allowed({"name": "agent-patch-pilot", "uid": 981, "groups": []}, req)
}

# An owner decides without a password over the control socket: the rule
# is not "self".
test_owner_without_self if {
	approvals.allow_without_self with input as {"approver": alice, "request": req}
		with data.mcp.rbac as rbac
		with data.mcp.agents as agents
}

test_owners_notified if {
	n := approvals.notify with input as {"request": req}
		with data.mcp.rbac as rbac
		with data.mcp.agents as agents
	"user:alice" in n
	"group:web-team" in n
}

# Without owners, the servers' rules alone, as before.
test_no_owners if {
	not approvals.allow with input as {"approver": alice, "request": req}
		with data.mcp.rbac as rbac
		with data.mcp.agents as {"patch-pilot": {"mode": "supervised", "permissions": []}}
	approvals.allow with input as {"approver": {"name": "dave", "uid": 1004, "groups": ["wheel"]}, "request": req}
		with data.mcp.rbac as rbac
		with data.mcp.agents as agents
}
