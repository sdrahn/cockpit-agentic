# Autonomy modes of Custos agents (CONTRACT.md §6), added to
# mcp-gateway's shipped policy (user guide chapter 12, approach A).
# Installed as /usr/share/mcp-gateway/policy/custos/agents.rego.
#
# An agent's principal is its user agent-<name>. Its permissions come
# only from data.mcp.agents.<name>, which agent-admin writes to
# /etc/mcp-gateway/policy/agents/<name>.yaml (one directory, which OPA
# watches from its start: it does not watch directories made later):
#
#   <name>:
#     mode: supervised        # advisory | supervised | bounded (default supervised)
#     paused: false           # true: every call is denied (Intervene)
#     permissions:            # gateway permissions, plus two flags:
#       - {server: zypp, tool: list_*, read_only: true}
#       - {server: zypp, tool: install_patches}                   # changes the system
#       - {server: systemd, tool: restart_unit, autonomous: true, args: {unit: '^nginx\.service$'}}
#
# A permission is read-only only when it says so; everything else
# changes the system (supervised by default). By mode:
#
#   advisory    read-only permissions; nothing that changes the system
#   supervised  read-only ones allowed; the others need an approval
#   bounded     also the "autonomous" ones without an approval; the
#               others need an approval
#
# Tool annotations (readOnlyHint) are untrusted and never used here.
#
# Over the gateway's HTTPS listener (agents in an OpenShell sandbox,
# CONTRACT.md §16a) an agent is a token with subject agent-<name> from an
# issuer listed in data.mcp.agents._http.issuers (agents/_http.yaml,
# none by default): that issuer issues agent-<name> subjects to agents
# only. Such a subject from any other issuer is denied everything.
# data.mcp.agents._control.stopped (agents/_control.yaml, agent-admin
# stop-all) denies every agent everything: the emergency stop. Agent
# names start with a letter, so _control is no agent's.
package mcp.authz

import rego.v1

agent_principal if {
	input.principal.transport == "unix"
	startswith(input.principal.sub, "agent-")
}

agent_principal if {
	input.principal.transport == "http"
	startswith(input.principal.sub, "agent-")
	agent_issuer
}

agent_issuer if input.principal.iss in object.get(object.get(data.mcp.agents, "_http", {}), "issuers", [])

agent_name := substring(input.principal.sub, 6, -1) if agent_principal

agent_data := data.mcp.agents[agent_name]

agent_mode := object.get(agent_data, "mode", "supervised")

agent_flags := ["read_only", "autonomous"]

perms contains q if {
	some p in agent_data.permissions
	q := agent_permission(p)
}

agent_permission(p) := object.remove(p, agent_flags) if {
	p.read_only == true
} else := object.remove(p, agent_flags) if {
	agent_mode == "bounded"
	p.autonomous == true
} else := object.union(object.remove(p, agent_flags), {"require_approval": true, "approval_channel": "oob"}) if {
	agent_mode in {"supervised", "bounded"}
}

# The agent's only permissions are its agent data: a role bound to an
# agent user would bypass its mode, so it denies everything instead.
denied if {
	agent_principal
	some r in roles
	r != ""
}

# An agent's subject from an issuer not trusted for agents: not an agent,
# and nothing else either.
denied if {
	input.principal.transport == "http"
	startswith(input.principal.sub, "agent-")
	not agent_issuer
}

# An agent without agent data gets nothing.
denied if {
	agent_principal
	not agent_data
}

# An unknown mode gets nothing.
denied if {
	agent_principal
	not agent_mode in {"advisory", "supervised", "bounded"}
}

# Pause and emergency stop. Discovery keeps the tools listed, so the
# agent's tool list survives a pause.
denied if {
	agent_principal
	not input.discovery
	agent_data.paused == true
}

denied if {
	agent_principal
	not input.discovery
	data.mcp.agents._control.stopped == true
}
