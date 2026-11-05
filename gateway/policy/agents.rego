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
#       - {server: zypp, tool: patch, category: patch-errata}      # a task category (§6a)
#     categories:             # written by agent-admin promote / demote
#       patch-errata:
#         mode: bounded       # supervised | bounded
#         envelope: {windows: [{days: [sat], from: '02:00', to: '04:00', tz: Europe/Berlin}]}
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
# stop-all) denies every agent everything: the emergency stop.
# data.mcp.agents._enablement (agents/_enablement.yaml, agent-admin
# enable) disables task categories (below). Agent names start with a
# letter, so _control and _enablement are no agent's.
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

agent_flags := ["read_only", "autonomous", "category"]

perms contains q if {
	some p in agent_data.permissions
	permission_enabled(p)
	q := agent_permission(p)
}

# Category enablement (CONTRACT.md §6b): data.mcp.agents._enablement
# (agents/_enablement.yaml, agent-admin enable) says which task
# categories this host may use at all. A permission of a disabled
# category is no permission: its calls are denied in every mode, and
# discovery does not show its tools. Without the data every category is
# enabled; an unknown state enables none.
enablement := object.get(data.mcp.agents, "_enablement", {})

enablement_state := object.get(enablement, "state", "")

permission_enabled(p) if {
	not misdeclared_diagnostics(p)
	category_usable(p)
}

# diagnostics only reads: a permission in it that is not read-only is
# none.
misdeclared_diagnostics(p) if {
	p.category == "diagnostics"
	not p.read_only == true
}

category_usable(_) if enablement_state == ""

category_usable(p) if {
	enablement_state == "diagnostics-only"
	p.category == "diagnostics"
}

category_usable(p) if {
	enablement_state == "categories"
	not has_category(p)
}

category_usable(p) if {
	enablement_state == "categories"
	p.category == "diagnostics"
}

category_usable(p) if {
	enablement_state == "categories"
	p.category in object.get(enablement, "enabled", [])
}

agent_permission(p) := object.remove(p, agent_flags) if {
	p.read_only == true
} else := object.remove(p, agent_flags) if {
	autonomous_now(p)
} else := object.union(object.remove(p, agent_flags), {"require_approval": true, "approval_channel": "oob"}) if {
	permission_mode(p) in {"supervised", "bounded"}
}

# Task categories (CONTRACT.md §6a). A permission with a category takes
# its category's mode: bounded only once agent-admin promote has given
# the category an entry, otherwise supervised. advisory on the agent
# caps every category. A permission without one keeps the agent's mode,
# with version 1's autonomous flag.
agent_categories := object.get(agent_data, "categories", {})

permission_mode(p) := "advisory" if {
	agent_mode == "advisory"
} else := object.get(object.get(agent_categories, p.category, {}), "mode", "supervised") if {
	has_category(p)
} else := agent_mode

# A helper, so that "not has_category(p)" holds for a permission without
# the key (not is_string(p.category) does not).
has_category(p) if is_string(p.category)

autonomous_now(p) if {
	not has_category(p)
	agent_mode == "bounded"
	p.autonomous == true
}

autonomous_now(p) if {
	has_category(p)
	permission_mode(p) == "bounded"
	in_envelope(agent_categories[p.category])
}

# The envelope's windows (§6a): outside them a bounded category acts as
# supervised. Each window: days (mon … sun; all if absent), from and to
# ("HH:MM", from before to) in tz (an IANA zone; UTC if absent).
in_envelope(c) if {
	count(object.get(object.get(c, "envelope", {}), "windows", [])) == 0
}

in_envelope(c) if {
	some w in c.envelope.windows
	in_window(w, time.now_ns())
}

in_window(w, ns) if {
	tz := object.get(w, "tz", "UTC")
	day := lower(substring(time.weekday([ns, tz]), 0, 3))
	day in object.get(w, "days", ["mon", "tue", "wed", "thu", "fri", "sat", "sun"])
	[h, m, _] := time.clock([ns, tz])
	now := (h * 60) + m
	minutes(w.from) <= now
	now < minutes(w.to)
}

minutes(hhmm) := (to_number(substring(hhmm, 0, 2)) * 60) + to_number(substring(hhmm, 3, 2))

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

# So does an unknown mode of a category.
denied if {
	agent_principal
	some c in agent_categories
	not c.mode in {"supervised", "bounded"}
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
