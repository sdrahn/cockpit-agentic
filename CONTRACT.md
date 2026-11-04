# Custos — Contract

Status: draft 1 (contract version `1`). The project was named
cockpit-agentic until before its first release; the names and paths
below are Custos's (`custos`, `/etc/custos`, …) and are the ones contract
version 1 keeps.

This contract is the interface between the people who supervise agents
and the agents themselves. It defines what a Cockpit control plane
consumes, so that the control plane does not depend on any particular
agent host (ADK, kit or another).

| Part | Contents | Who must follow it |
|---|---|---|
| **I** | the control-plane contract: identity, activities, the control API, autonomy modes, approvals, intervention, delegation, audit, interaction | every agent host; the Cockpit control plane consumes only this |
| **II** | the reference implementation: an ADK Go launcher run by systemd, with its configuration, sandbox, SELinux and packaging | this repository |
| **III** | requirement traceability and open items | — |

Within version 1, a name, path or field listed here is added to, never
renamed or removed (the same rule as mcp-gateway's D10). A host reports
the version it implements in `GET /v1/status` (`contract`).

---

# Part I — The control-plane contract

## 1. Parts and who enforces what

| Part | Role | Enforces |
|---|---|---|
| **mcp-gateway** (≥ 1.12) | the agents' only tools; OPA policy (autonomy modes, §6); approvals and grants (§7) | *may this agent call this tool with these arguments now* |
| **systemd** | runs, schedules and stops agents; sandboxes them with the `systemd` backend | what an agent process can touch; that a stopped agent stays stopped |
| **SELinux** | confines agents (`systemd` backend: `agent_t`; `openshell` backend: the container runtime's domain) | that an agent reaches the gateway's MCP socket, never its control socket nor another agent's socket |
| **sandbox backend** (§3a) | `systemd` (the default) or `openshell` (NVIDIA OpenShell) | files, processes and network egress of the agent process |
| **journald** | holds the audit record (§10) next to the gateway's | — (it is the record) |
| **agent host** | runs agents; serves the control API (§5); writes audit records | local limits only; not a security boundary |
| **Cockpit control plane** | shows activity, approvals, policy and history; intervenes | — (it shows things; decisions go to the gateway and systemd) |

**Rule:** anything that must hold even if an agent or its host is
compromised is enforced by the kernel, systemd or the gateway. That
covers supervised-by-default, the tool boundaries, approvals and the
emergency stop. Code inside the agent's process enforces none of it.
What the host does itself (limits, pause, trace fields) helps an honest
agent and enriches the record.

## 2. Identity

| Thing | Value |
|---|---|
| agent name | `^[a-z][a-z0-9-]{0,30}[a-z0-9]$`, unique on the host |
| agent user | `agent-<name>`: a static system user without a password, member of `mcp-users` and `agent-clients`. The prefix `agent-` is reserved for agents |
| gateway principal | `sub: agent-<name>`. With the `systemd` backend: `transport: unix`, identified by `SO_PEERCRED`. With the `openshell` backend: `transport: http`, identified by an OAuth bearer token whose subject is `agent-<name>`, from an issuer trusted for agents (§6); the token carries no local-user claim, so the subject stays the principal (§16a). Never by what the agent says |
| journal identifier | `SYSLOG_IDENTIFIER=agent-<name>`, `AGENT_NAME=<name>` |
| activity id | unique on the host; in the journal as `AGENT_INVOCATION` |

The **session user** is the person or source for whom an activity
runs: a local account for chat (taken from `SO_PEERCRED` on the
agent's socket), `system:schedule` or `system:event`, or `agent-<other>`
for delegation. The agent acts at the gateway as `agent-<name>`, never
as the session user.

**People and the control plane.** A control plane acts for the person
using it, under that person's own account (a local or directory user):
the agents' sockets (§3) and the gateway's control API identify them by
`SO_PEERCRED`. It never acts as the gateway's account or an agent's.
Where it can also act as root (Cockpit with administrative access), it
does so only for what needs root, and names the person:

| Done as | What |
|---|---|
| **the person**, always | approval decisions: approve, decline, revoke a grant (§7, §8). The gateway records the approver it sees, so a decision made as root would be root's. Also reading status, activities and the journal, and chat (§11) |
| root | creating and removing agents, modes and permissions (§6), pause and resume, stop, the emergency stop and its release (§8), through `agent-admin` (§17), which records the person (`by`) |

Having administrative access therefore never changes for whom an
approval is recorded. `docs/user-flow.md` walks through the roles and
flows.

## 3. Discovery

An agent is a directory `/run/agents/<name>/` holding `api.sock`, where
`GET /v1/status` answers with `"contract": 1`. That is all a control
plane needs to find agents. `/v1/status` says how the agent is started
and stopped (`lifecycle`, §3a) and names the host implementation
(`implementation`). The ADK launcher reports `custos/adk-go`.
A host that is not socket-activated still creates the socket while it
runs, and names its lifecycle. (`units` is the `systemd` lifecycle's
units, kept for version 1 clients.)

`/run/agents/` is `root:root 0755`. Each socket is `root:agent-clients
0660`. Members of `agent-clients` (supervisors) and root (Cockpit with
administrative access) can reach the sockets. Agents are members too,
but only for delegation through the gateway's bridge (§9): their own
units cannot see `/run/agents`.

## 3a. Sandbox backends and lifecycle

Where an agent process runs, and what starts, sandboxes and stops it, is
its **sandbox backend**. Everything else in Part I (identity at the
gateway, activities, the control API, modes, approvals, audit) is the
same for every backend.

| `lifecycle` in `/v1/status` | Starts / stops | Sandbox | Reaches the gateway |
|---|---|---|---|
| `{"kind": "systemd", "service": "agent@<name>.service", "socket": "agent@<name>.socket"}` | systemd | the unit's sandbox and SELinux `agent_t` (§15, §18) | `/run/mcp-gateway/mcp.sock`, `SO_PEERCRED` |
| `{"kind": "openshell", "sandbox": "agent-<name>", "gateway": "<openshell gateway name>", "service": "agent-openshell@<name>.service"}` | OpenShell (`openshell sandbox start/stop`), with a systemd unit on the host that owns the agent's socket | OpenShell: Landlock, seccomp, an unprivileged user, a network fence; egress only through OpenShell's supervisor and its network policy | mcp-gateway's Streamable HTTP listener at `host.openshell.internal`, with a bearer token the supervisor adds (the agent never holds it) |

Rules for every backend:

- **The gateway stays the only source of tools.** Network policy of
  the backend must allow the gateway and the model endpoint, and nothing
  else, unless an administrator adds it.
- **Approvals stay the gateway's.** A backend's own review of policy
  changes (OpenShell's policy prover) is not an approval of an action.
- **The control API stays a Unix socket on the host**
  (`/run/agents/<name>/api.sock`), owned by a host process that
  identifies callers by `SO_PEERCRED`. For a sandboxed agent, that
  process relays to the agent (Part II, §16a) and passes the caller on
  as `X-Custos-Caller: <uid>:<name>` with a secret the relay and the
  agent share; the agent believes the header only with the secret.
- **Stop works without the agent.** `lifecycle` names what the control
  plane stops (§8); the emergency stop also denies at the gateway.

## 4. Activities

An **activity** is one unit of agent work: a chat turn, a scheduled
run, an event's run, or a task delegated by another agent, with the tool
calls it makes. Activities are what the control plane shows, whoever
started them.

```json
{
  "id": "e-6448…",
  "agent": "patch-pilot",
  "session": "t1",
  "user": "system:schedule",
  "origin": {"kind": "schedule", "by": "agent-schedule@patch-pilot_compliance.timer", "schedule": "compliance"},
  "parent": {"agent": "triage", "activity": "e-12ab…"},
  "root":   {"agent": "triage", "activity": "e-0f00…"},
  "state": "waiting_approval",
  "started": "2026-11-03T06:00:04Z",
  "ended": null,
  "summary": "Installing 1 security patch; nginx restarts.",
  "reason": "",
  "llm_calls": 2,
  "calls": [
    {"call_id": "c1", "tool": "zypp__list_updates", "state": "ok", "args_sha256": "4413…",
     "since": "…", "ended": "…"},
    {"call_id": "c2", "tool": "zypp__install_patches", "state": "waiting_approval", "args_sha256": "6eb8…",
     "approval_id": "a-42", "approval": "pending",
     "rationale": "Installing 1 security patch; nginx restarts."}
  ]
}
```

| Field | Values |
|---|---|
| `origin.kind` | `chat` (a person, through the REST API or AG-UI), `schedule`, `event`, `delegation`, `api` (a program or person through `POST /v1/activities`) |
| `origin.by` | the person, the timer unit, the event source, or the delegating agent's user |
| `parent`, `root` | delegation (§9): the activity that delegated this one, and the first one of the chain. `root` is the activity itself when nothing delegated it |
| `state` | `running`, `waiting_approval` (a call waits for the gateway's approval), `paused`, `finished`, `failed`, `cancelled`, `limit` (a local limit ended it) |
| `calls[].state` | `running`, `waiting_approval`, `ok`, `error` |
| `calls[].approval` | `pending`, `approved`, `declined` |
| `calls[].rationale` | the model's text that came with the call: its stated reason and expected impact. **Self-asserted by the agent**; the control plane labels it so |
| `summary` | the agent's last text, at most 500 characters |

Arguments never appear in activities or the journal (only
`args_sha256`). The gateway shows the arguments of a pending approval
to those who may decide it (§7), and logs them as its policy says.

The host keeps running activities and at least the last 200 ended ones.
The journal (§10) is the full history.

## 5. Control API

HTTP/1.1 on `/run/agents/<name>/api.sock`, JSON. The caller is identified
by `SO_PEERCRED`.

| Request | Who | Meaning |
|---|---|---|
| `GET /v1/status` | supervisors, root | `{"contract":1, "implementation", "lifecycle":{"kind",…} (§3a), "units":{"service","socket"} (systemd only), "name", "version", "model", "paused", "gateway":{"socket","reachable"}, "running", "in_flight":[call…], "started", "limits", "interaction":{"agui","client_tools"}, "schedules":[{"name","on_calendar","prompt"}]}` |
| `GET /v1/activities` | supervisors, root | `{"activities": [activity…]}`, running first, then ended, newest first |
| `GET /v1/activities/{id}` | supervisors, root; an agent only for activities it delegated | one activity |
| `POST /v1/activities` | root (schedules, events, api); supervisors (`api`); agents (`delegation`, §9) | `{"prompt", "session"?, "user"? (root only), "origin"? (root only), "trace"? (agents only), "wait"?}`: `202 {"id","session"}`, or with `wait` the ended activity; `409` while paused |
| `POST /v1/activities/{id}/cancel` | root; the activity's user or originator | ends a running activity: `204`, `409` if not running |
| `POST /v1/pause`, `POST /v1/resume` | root | holds every activity before its next model or tool call and refuses new ones (§8) / lets them go on |

A host may serve more (the ADK launcher also serves the ADK REST API
for chat, Part II). The control plane uses only this table, §11 and
the gateway's control API.

Errors are `4xx`/`5xx` with a plain-text reason. Agents (callers named
`agent-*`) may only use `POST /v1/activities` and
`GET /v1/activities/{id}`.

## 6. Autonomy modes and policy

Each agent has an **autonomy mode**. The gateway enforces it, from data
the host never writes itself:

```yaml
# /etc/mcp-gateway/policy/agents/<name>.yaml  →  data.mcp.agents.<name>
<name>:
  mode: supervised        # advisory | supervised | bounded
  paused: false           # true: the gateway denies every call (§8)
  paused_by: "alice 2026-11-03T12:00:00Z"
  permissions:            # gateway permissions, with two flags
    - {server: zypp, tool: 'list_*', read_only: true}
    - {server: zypp, tool: install_patches}
    - {server: systemd, tool: restart_unit, autonomous: true, args: {unit: '^nginx\.service$'}}
```

| Mode | Read-only permissions | Other permissions | `autonomous: true` ones |
|---|---|---|---|
| `advisory` | allowed | denied | denied |
| `supervised` (default) | allowed | need an approval | need an approval |
| `bounded` | allowed | need an approval | allowed without one |

- **Supervised by default.** A permission is read-only only when it says
  `read_only: true`. A new agent gets `mode: supervised` and no
  permissions, so it can do nothing until an administrator lists what
  it may do.
- **Annotations are not trusted.** An MCP server's `readOnlyHint` never
  allows anything.
- **Agent data is the only source.** A gateway role bound to an agent
  user would bypass its mode, so it denies the agent everything. An
  `agent-*` principal without agent data gets nothing.
- **Agents over HTTPS.** On the gateway's socket an agent is its user
  `agent-<name>`. On the gateway's HTTPS listener (an agent in a sandbox,
  §16a) it is a token whose subject is `agent-<name>` from an issuer
  listed in `/etc/mcp-gateway/policy/agents/_http.yaml`
  (`_http: {issuers: [...]}`, none by default); that issuer gives such
  subjects to agents only. An `agent-*` subject from any other issuer is
  denied everything. Modes, pause and the emergency stop are the same on
  both.
- The rules are a Rego module added to the gateway's shipped policy
  (`mcp.authz`: `perms` and `denied`), installed as
  `/usr/share/mcp-gateway/policy/custos/agents.rego`. The gateway
  reloads the data within seconds of a change and tells agents that
  their tool lists changed.

**Showing the posture.** The control plane reads the agent data file
for the mode, the pause flag and the permissions. It reads the gateway's
role data for the approver rules, and `GET /v1/status` for the host's
local limits. A different host implementation changes none of this.

## 7. Approvals

Approvals are the gateway's (mcp-gateway architecture §5.6.1). Neither
the agent nor its host can grant or forge one.

1. The agent calls a tool. Policy says `ask` (channel `oob`). The
   gateway creates a pending approval and the call waits (gateway
   `approval_timeout`, 120 s by default; the approval itself stays
   pending after the call goes away, and the agent's next identical
   call takes it over).
2. The gateway sends the agent `notifications/message` (`logger:
   "mcp-gateway"`, `level: "notice"`) with the text `Waiting for approval
   of <server>/<tool> <where> (id <approval-id>).` The host assigns the
   approval id to the oldest call of that tool in flight without one. The
   activity's state becomes `waiting_approval`, the host writes
   `approval.pending`, and it emits AG-UI `agent.approval.pending` (§11).
3. A person decides through the gateway: its control API (`POST
   /v1/approvals/{id}`) or its Cockpit page. **Who may decide** is the
   gateway's approver rules. Agent users have no password, so `self`
   never lets an agent approve its own call (gateway roadmap step 40),
   and an agent cannot reach the control socket anyway (§1). Name the
   approvers of servers that agents use explicitly, e.g.
   `approvers: {zypp: ['role:admin', 'group:agent-clients']}`.
4. The call returns. The host writes `approval.resolved`
   (`approved`/`declined`), then `tool.result`. The gateway's own audit
   records the decision and the approver.

**The approval item** the control plane shows joins two sources by
approval id:

| From the gateway (`GET /v1/approvals`) | From the agent (`GET /v1/activities`) |
|---|---|
| the exact call: server, tool, arguments, principal, the scopes offered | the activity: origin, who started it, its delegation chain |
| who may decide; the decision and the approver afterwards | the agent's stated rationale and expected impact (`calls[].rationale`, self-asserted) |
| | the evidence: the activity's earlier calls and their results' state |

Only the gateway's half is trustworthy. The page shows the agent's half
as the agent's claims.

Hosts must not use any confirmation mechanism of their own in place of
the gateway's (such as ADK's tool confirmation). Those ask the session
user, who is not necessarily allowed to approve.

## 8. Intervention

| Control | Scope | How | Enforced by |
|---|---|---|---|
| **Decline** | one pending call | the gateway's approval: deny | gateway |
| **Revoke** | a grant | gateway `DELETE /v1/grants/{id}` | gateway |
| **Cancel** | one running activity | `POST /v1/activities/{id}/cancel` | host |
| **Pause** | one agent | the agent data's `paused: true` (the gateway denies every call) **and** `POST /v1/pause` (the host holds activities and refuses new ones) | gateway; host for the graceful part |
| **Stop** | one agent | by its `lifecycle` (§3a): `systemctl stop agent@<name>.socket agent@<name>.service`, or `openshell sandbox stop agent-<name>` and `systemctl stop agent-openshell@<name>.service` | systemd; OpenShell |
| **Disable a schedule** | one schedule | `systemctl disable --now agent-schedule@<name>_<schedule>.timer` | systemd |
| **Emergency stop** | every agent | `/etc/mcp-gateway/policy/agents/_control.yaml` with `_control: {stopped: true}` (the gateway denies every agent everything, on every backend), then stop every agent by its lifecycle and every schedule timer | gateway, systemd, OpenShell |

`agent-admin` implements pause, resume, `stop-all` and `release-all`
(Part II). The emergency stop works even if an agent's host ignores
everything: the gateway denies the calls, and systemd stops the
processes. Every intervention is journaled (`agent.paused`,
`agent.resumed`, `activity.cancel`, the gateway's policy change
records, systemd's unit records).

## 9. Delegation

Agents delegate to each other **through the gateway**. Delegation is
then a governed tool call, `agents__delegate {agent, task}`, allowed
only where the delegating agent's permissions say so. For example,
`{server: agents, tool: delegate, args: {agent: '^patch-pilot$'}}`
allows delegating to `patch-pilot` only. Under supervised mode a
delegation itself needs an approval unless it is marked `read_only`.

1. The delegating host adds a trace to the call's arguments:
   `_trace: {root_agent, root_activity, parent_agent, parent_activity}`.
2. The gateway runs the `agents` server (`agent-delegate`) **as the
   delegating agent's user** (`run_as: principal`). It posts the task to
   the callee's `POST /v1/activities` and waits for the result.
3. The callee sees the caller as `agent-<parent>` by `SO_PEERCRED`. It
   records `origin: {kind: delegation, by: agent-<parent>}`. It believes
   the trace only when `parent_agent` is that caller, and otherwise
   records the activity without a parent.
4. The callee works as **its own principal**, under its own mode,
   permissions and approvals. Delegation never lends one agent's rights
   to another.

**Tracing a chain:** every activity's journal records carry
`ROOT_AGENT`/`ROOT_ACTIVITY`, and delegated ones also carry
`PARENT_AGENT`/`PARENT_ACTIVITY`. The control plane builds the tree
from these. The gateway's audit records of `agents__delegate` (by
`sub=agent-<parent>`) confirm each link independently of what agents
wrote.

Agents cannot reach each other directly. Their units hide
`/run/agents`, SELinux allows the agents' sockets to `mcpsrv_agents_t`
(the bridge) but not to `agent_t`, and the bridge only exists as a
gateway server.

## 10. Audit record

Hosts write journal records with these fields (journald's native
protocol; without a journal, JSON lines on stderr with lower-case keys
and without the `AGENT_` prefix):

| Field | Value |
|---|---|
| `MESSAGE`, `MESSAGE_ID`, `PRIORITY`, `SYSLOG_IDENTIFIER=agent-<name>` | journald's |
| `AGENT_CONTRACT` | `1` |
| `AGENT_NAME`, `AGENT_EVENT` | the agent; the event (table below) |
| `AGENT_SESSION`, `AGENT_INVOCATION`, `AGENT_USER` | session, activity id, session user |
| `AGENT_ORIGIN`, `AGENT_ORIGIN_BY`, `AGENT_SCHEDULE` | §4 (`invocation.start`) |
| `AGENT_PARENT_AGENT`, `AGENT_PARENT_ACTIVITY`, `AGENT_ROOT_AGENT`, `AGENT_ROOT_ACTIVITY` | §9 (`invocation.start`, `invocation.end`) |
| `AGENT_TOOL`, `AGENT_SERVER`, `AGENT_CALL_ID`, `AGENT_ARGS_SHA256` | tool events. SHA-256 of the arguments' JSON with sorted keys. Arguments are never logged |
| `AGENT_RATIONALE` | `tool.call`: the model's stated reason (self-asserted) |
| `AGENT_APPROVAL_ID` | approval events, and `tool.result` of an approved or declined call |
| `AGENT_OUTCOME`, `AGENT_REASON` | `ok`, `error`, `denied`, `approved`, `declined`, `cancelled`, `limit`; why |
| `AGENT_DURATION_MS`, `AGENT_LLM_CALLS`, `AGENT_TOOL_CALLS`, `AGENT_MODEL`, `AGENT_TOKENS_IN`, `AGENT_TOKENS_OUT` | end events, `model.call` |

| `AGENT_EVENT` | `MESSAGE_ID` |
|---|---|
| `agent.start` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a601` |
| `agent.stop` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a602` |
| `config.invalid` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a603` |
| `agent.paused` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a604` |
| `agent.resumed` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a605` |
| `invocation.start` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a610` |
| `invocation.end` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a611` |
| `activity.cancel` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a612` |
| `model.call` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a620` |
| `tool.call` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a630` |
| `tool.result` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a631` |
| `tool.denied.local` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a632` |
| `approval.pending` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a640` |
| `approval.resolved` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a641` |
| `limit.exceeded` | `6f3c0e1a9b2d4c58a7e1f2d3c4b5a650` |

**History of an action** (proposed → approved/declined → executed):
`tool.call` (with rationale) → `approval.pending` → the gateway's
approval decision record (`op=mcp-approval`, with the approver) →
`approval.resolved` → `tool.result`, joined by `AGENT_CALL_ID` and
`AGENT_APPROVAL_ID`. The control plane reads them with
`journalctl -o json AGENT_NAME=<name>` and
`journalctl -o json -u mcp-gateway.service` (gateway records have
`"audit":true`, `sub`, `server`, `name`). Retention is journald's.

## 11. Interaction (chat as one mode)

Conversation with an agent is one mode of the control plane, for
driver-style tasks. It is not its basis. Hosts that offer it serve
**AG-UI** at `POST /agui` on the agent's socket:

- **Request:** AG-UI's `RunAgentInput` (`threadId`, `runId`, `messages`,
  `tools`, `state`, `context`, `forwardedProps`). The response is
  `text/event-stream` with one AG-UI event per `data:` line.
- **Threads are the caller's.** `threadId` is a session among the
  caller's own (by `SO_PEERCRED`); another user's thread id simply names
  a new thread of the caller's.
- **History is the host's.** Only the last `user` message (or the
  trailing `tool` messages, below) is new input. A frontend cannot
  rewrite history.
- `state` is merged into the session state under keys prefixed `ui:`.
- The run is an activity with `origin.kind: chat`.

| Host event | AG-UI event |
|---|---|
| run start / end / failure | `RUN_STARTED`, then `STATE_SNAPSHOT` (state without `temp:` keys) / `RUN_FINISHED` / `RUN_ERROR` |
| model text (streamed or whole) | `TEXT_MESSAGE_START`, `TEXT_MESSAGE_CONTENT`, `TEXT_MESSAGE_END` |
| tool call | `TOOL_CALL_START`, `TOOL_CALL_ARGS` (one JSON delta), `TOOL_CALL_END` |
| tool result | `TOOL_CALL_RESULT` |
| state change | `STATE_DELTA` (JSON Patch, one `add` per key) |
| §7 step 2 | `CUSTOM` `agent.approval.pending` `{"approval_id","tool","call_id","approval_url"}` |
| §7 step 4 | `CUSTOM` `agent.approval.resolved` `{"approval_id","tool","call_id","outcome"}` |
| local limit | `CUSTOM` `agent.limit.exceeded` `{"limit","value"}` |

**Client tools** (`RunAgentInput.tools`) run in the frontend. They are
offered to the model only if the host's configuration allows them by
name; tools whose names contain `__` are refused, so they cannot pose
as gateway tools. Refused ones are journaled as `tool.denied.local`.
When the model calls one, the stream ends after `TOOL_CALL_END`; the
frontend answers with a `role: tool` message on the same thread. Client
tools never reach the gateway and must not be used for anything an
approval would guard.

The stream is ephemeral and reaches only whoever is connected.
Approvals (§7), intervention (§8) and the audit record (§10) never
depend on it.

## 12. Scope: one system, fleet not precluded

Version 1 is **single-system**: every interface is local to the host,
and fleet scope is out of scope for SLES 16.2. Nothing here precludes it:

- every interface is reachable through Cockpit's bridge on that host, so
  Cockpit's host switcher already gives per-host supervision of many
  systems;
- identifiers are unique per host, and a fleet view keys everything by
  `(host, agent, activity)`;
- delegation chains across hosts would need a cross-host transport for
  `agents__delegate` (the gateway's Streamable HTTP with OAuth). The
  trace fields already carry the agent names, and would gain a host
  part (open item O7).

---

# Part II — Reference implementation (ADK Go)

## 13. Files

| Path | Owner, mode | Contents |
|---|---|---|
| `/usr/libexec/custos/agent-launcher` | root 0755 | the host: one ADK agent per process |
| `/usr/libexec/custos/agent-delegate` | root 0755 | the gateway's `agents` server (§9) |
| `/usr/sbin/agent-admin` (and a link in the libexec directory) | root 0755 | administration (§17) |
| `/usr/lib/systemd/system/agent@.{service,socket}`, `agent-schedule@.{service,timer}` | root 0644 | unit templates |
| `/usr/share/mcp-gateway/policy/custos/agents.rego` | root 0644 | the autonomy modes (§6) |
| `/usr/share/custos/gateway/servers.d/agents.yaml` | root 0644 | the `agents` server definition; copy to `/etc/mcp-gateway/servers.d/` to allow delegation |
| `/etc/custos/agents/<name>/agent.yaml` | root:agent-`<name>` 0640 | configuration (§14) |
| `/etc/sysusers.d/agent-<name>.conf` | root 0644 | the agent's user |
| `/etc/mcp-gateway/policy/agents/<name>.yaml` | root 0644 | mode, pause, permissions (§6). All agents' files, the emergency stop's `_control.yaml` and the agents' token issuers' `_http.yaml` (§6; both shipped by the package) are in this one directory: mcp-gateway's OPA (`--watch`) watches only directories it loaded a file from when it started |
| `/etc/credstore.encrypted/agent-<name>.<cred>` | root 0600 | secrets (`systemd-creds encrypt`) |
| `/etc/systemd/system/agent@<name>.service.d/` | root | sandbox changes; Cockpit writes `50-cockpit.conf` only |
| `/etc/systemd/system/agent-schedule@<name>_<schedule>.timer.d/schedule.conf` | root | a schedule's `OnCalendar=` |
| `/var/lib/agents/<name>/sessions.db` | agent-`<name>` 0600 | ADK sessions (SQLite) |

## 14. Agent configuration (`agent.yaml`)

```yaml
version: 1
name: patch-pilot                     # = directory = unit instance
description: Keeps the host patched.
model:
  provider: openai                    # openai (any OpenAI-compatible server) | gemini | vertex
  name: qwen3-32b
  endpoint: http://127.0.0.1:8000/v1  # openai only
  credential: model-key               # optional: $CREDENTIALS_DIRECTORY/agent-<name>.model-key
  temperature: 0.2
instruction: "…"                      # or instruction_file: instruction.md
gateway:
  socket: /run/mcp-gateway/mcp.sock   # default
  servers: [zypp, systemd]            # whose tools the model is shown
  tools: []                           # optional narrower list of <server>__<tool>
  # OpenShell agents (§16a): url: https://host.openshell.internal:8443/mcp, and
  # oauth: {token_url: https://idp/…/token, client_id: agent-<name>, scopes: [mcp],
  #         refresh_before_seconds: 60}   # optional; without it a static token
limits: {max_llm_calls: 25, max_tool_calls: 40, max_invocation_seconds: 900}
interaction:
  agui: true
  client_tools: deny                  # or {allow: [confirm_window]}
schedules:
  - {name: compliance, on_calendar: "Mon..Fri 06:00", prompt: "List pending security patches; install nothing."}
sessions: {retention_days: 30}
sandbox: {kind: systemd}             # or {kind: openshell, openshell_gateway: openshell} (§3a, §16a)
```

Unknown keys are errors. `servers` and `tools` only decide what the
model is *shown*; what the agent *may* do is §6. With `provider: openai`
and a local model server, an agent needs nothing outside the host, so
it works air-gapped.

## 15. Units and sandbox

`agent@.socket` listens on `/run/agents/%i/api.sock`. `agent@.service`
runs `agent-launcher serve` with:

- `User=agent-%i`, `SupplementaryGroups=mcp-users`;
- `ConfigurationDirectory=custos/agents/%i`, `StateDirectory=agents/%i`;
- `ImportCredential=agent-%i.*`;
- `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`,
  `PrivateDevices=yes`, `NoNewPrivileges=yes`, `CapabilityBoundingSet=`,
  `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6`,
  `SystemCallFilter=@system-service ~@privileged @resources`,
  `MemoryDenyWriteExecute=yes`, `ProtectKernel*=yes`, `ProtectProc=invisible`
  and the rest of the template;
- `InaccessiblePaths=-/run/mcp-gateway/control.sock` and
  `TemporaryFileSystem=/run/agents:ro` (no control socket, no other
  agents).

The launcher exits after 10 minutes without requests or running
activities, and the socket starts it again. `agent-schedule@<name>_<schedule>.timer`
runs `agent-admin trigger <name>_<schedule>`, which posts the
schedule's prompt as root with `origin.kind: schedule`. (Not
`agent-launcher`: systemd would run its program in `agent_t`, which may
not reach agents' sockets.)

## 16. The launcher

- **Tools:** ADK's MCP toolset on the gateway's aggregated endpoint,
  reconnecting per connection. The client declares no `elicitation`
  capability, so the gateway never asks the agent to approve its own
  call. `clientInfo.name` is `custos/<name>`. No other tools,
  apart from AG-UI client tools that the configuration allows.
- **Governance plugin** (ADK plugin): activities (§4), pause, cancel,
  local limits, journal records (§10), approval notices (§7), the
  delegation trace (§9), the calls' rationale.
- **Socket:** the control API (§5), AG-UI (§11) and ADK's REST API for
  chat. For the REST API, only the caller's own sessions are reachable
  (root: all), and only `/health`, `/version`, `/list-apps`,
  `/apps/<name>/users/<caller>/sessions…`, `/run` and `/run_sse`; ADK's
  builder and debug endpoints answer 404.
- **Models:** `openai` (`internal/openai`: chat completions with tools),
  `gemini`, `vertex`.

## 16a. The OpenShell backend (draft, O10)

An agent with `sandbox: {kind: openshell}` runs under NVIDIA OpenShell
instead of `agent@.service`:

| Piece | What |
|---|---|
| image | `packaging/openshell/Containerfile`: Leap 16 with `agent-launcher` (`CUSTOS_OPENSHELL_IMAGE`, default `localhost/custos-agent:latest`) |
| sandbox `agent-<name>` | created by `agent-admin` with `--policy`, `--provider agent-<name>-gateway` and the environment: the configuration (`CUSTOS_CONFIG`, base64), the relay secret (`CUSTOS_RELAY_SECRET`), the gateway token's placeholder (`AGENT_GATEWAY_TOKEN`, set by OpenShell). The agent runs as OpenShell's `sandbox` user and serves its control API on `127.0.0.1:<port>` inside the sandbox (port 47000–47999, stable per name) |
| sandbox policy | `/etc/custos/agents/<name>/openshell-policy.yaml`: Landlock as a hard requirement; the image's system read-only; the network only to the model endpoint, for `agent-launcher` only (the gateway's rule comes with the provider). The host's loopback is `host.openshell.internal` (in the policy and in the configuration the sandbox gets) |
| provider profile | `custos-gateway-<name>` (`openshell-profile.yaml`): the bearer token `AGENT_GATEWAY_TOKEN`, valid for the gateway's endpoint only, which attaching the provider adds to the sandbox's network policy (`protocol: mcp`, every known MCP method: the tools are the gateway's decision; the protocol revisions `2025-06-18` and `2025-11-25`, since OpenShell refuses the others; for `agent-launcher` only). The agent sends `Authorization: Bearer $AGENT_GATEWAY_TOKEN` (`gateway.token_env`); the supervisor swaps the placeholder for the token on the way out |
| relay secret | in the sandbox's environment, which OpenShell flags as readable by the agent. That is intended: it proves to the agent that a request comes from the relay, so the agent must know it; host users who cannot reach the agent's socket must not (`relay.secret` is root's, 0600) |
| relay | `agent-openshell@<name>.socket` owns `/run/agents/<name>/api.sock`; `agent-openshell@<name>.service` starts the sandbox and `openshell forward`, and runs `agent-openshell-relay` (SELinux `agent_relay_t`): callers by `SO_PEERCRED`, passed on as `X-Custos-Caller` with `X-Custos-Relay-Secret` (the agent's `--listen-tcp` mode believes nothing else); and the agent's records from its container's output (`agent-openshell-logs`: `docker logs` or `podman logs` of the sandbox's container; `openshell logs` has only the supervisor's lines), written to the journal with their `AGENT_*` fields |
| openshell CLI | `agent-admin` and the relay unit run it as root with `HOME=/root`: the OpenShell gateway named in `sandbox.openshell_gateway` is registered for root (`openshell gateway add ... --name <gateway>`), and the CLI keeps its forwards there |
| gateway | mcp-gateway's HTTPS listener (`http:` in its configuration): the agent's `gateway.url` is its audience, e.g. `https://host.openshell.internal:8443/mcp`, since the sandbox reaches the host by that name. The certificate names that host. OpenShell's proxy terminates the agent's TLS (to inspect MCP and swap the token in) and verifies the gateway itself, in the sandbox's supervisor, not with the image's trust store: a CA no public root signs goes into the OpenShell gateway's configuration (`[openshell.supervisor.network] additional_ca_bundle = "<ca.pem>"` in its `gateway.toml`, OpenShell 0.1.7 or later), which hands it to every sandbox's supervisor |
| container runtime | Docker, or rootless Podman (OpenShell's RPM default: the OpenShell gateway is a `systemctl --user` service of an ordinary user, whose Podman holds the sandboxes' containers). With rootless Podman, `CUSTOS_OPENSHELL_USER` names that user for `agent-admin`: it goes to `openshell.env` (`OPENSHELL_USER`), the log source runs `podman logs` as that user (`setpriv`; SELinux: `agent_relay_t` → `container_runtime_t`), and a drop-in `agent-openshell@<name>.service.d/openshell-user.conf` makes only the user's container storage and runtime directory writable for it. The OpenShell gateway is registered for root as well (§16a, openshell CLI). The system delegates the cgroup controllers to user sessions (a `user@.service` drop-in with `Delegate=cpu cpuset io memory pids`), without which rootless Podman cannot set the sandboxes' limits, and the user has subordinate IDs and lingering |
| token | With `gateway.oauth` (the agent is an OAuth client of the issuer): `agent-admin set-credential <name> gateway-client-secret < secret` creates the provider with runtime credentials, gives OpenShell's gateway the client (`openshell provider refresh configure … --strategy oauth2-client-credentials`, the secret through the environment, never a command line), mints the first token (`provider refresh rotate`; a refused client fails here) and creates the sandbox. OpenShell's gateway then renews the token before it expires (the profile's `refresh`: token URL, scopes, `refresh_before`); the sandbox's placeholder resolves to the current token per request, so renewals need no restart of the agent. The issuer's client configuration gives the tokens the subject `agent-<name>` and the gateway's audience (the grant sends no audience). Without `gateway.oauth`: `set-credential <name> gateway-token < token` with a static token, which OpenShell does not renew. Either way the token is an OAuth access token for the gateway's audience, subject `agent-<name>`, from an issuer in `_http.yaml` (§6). It carries no claim of the gateway's `local_user_claim`: the gateway maps a token to a local user only for accounts of persons (UID_MIN..UID_MAX) and refuses system accounts, which agents' users are, so the subject stays the principal |

`stop-all` stops the relay units and every OpenShell agent's sandbox by
name; the gateway's `agents_control` flag denies them like any agent.

## 17. `agent-admin`

Run as root, by hand or by Cockpit with administrative access:

| Command | Does |
|---|---|
| `create NAME --from FILE` | validates; writes the sysusers.d file and runs `systemd-sysusers`; installs `agent.yaml`; writes the gateway agent data (supervised, no permissions); writes the schedules' timer drop-ins; enables the socket and timers |
| `edit NAME --from FILE` | validates, replaces `agent.yaml`, syncs schedules, `try-restart` |
| `check NAME\|FILE` | validates only |
| `mode NAME MODE`, `permissions NAME < list.yaml` | the gateway agent data (§6). Like `pause`, `resume`, `stop-all` and `release-all`, it returns once the gateway's OPA has loaded the change (it reads it back on `/run/mcp-gateway/opa.sock`, for up to 20 s), so the change is in force when the command returns |
| `set-credential NAME CRED < secret` | `systemd-creds encrypt`; for OpenShell agents `gateway-client-secret` or `gateway-token` (§16a) |
| `pause NAME`, `resume NAME` | §8 (gateway data and `/v1/pause`) |
| `stop-all`, `release-all` | the emergency stop (§8) and its end |
| `delete NAME [--keep-state]` | disables and stops units; removes configuration, gateway data, credentials, drop-ins, timers and, unless kept, state. The user stays, since uids are not reused while files may still belong to them |
| `trigger NAME_SCHEDULE` | starts the schedule's activity (run by `agent-schedule@.service`) |
| `list` | the agents |

## 18. SELinux (`custos` module)

SLES 16 and Leap 16 run SELinux enforcing, so every program this
repository ships runs in a domain the policy accounts for:

| Program | Started by | Domain |
|---|---|---|
| `agent-launcher` | systemd (`agent@.service`) | `agent_t` |
| `agent-delegate` | mcp-gateway (the `agents` server) | `mcpsrv_agents_t` |
| `agent-admin` | an administrator, Cockpit, `agent-schedule@.service` | the caller's (`unconfined_t`, `unconfined_service_t`) |
| `fakemodel`, `testsrv` (`-tests`) | the functional tests; mcp-gateway | `unconfined_service_t`; `mcpsrv_generic_t` |

CI builds the module and runs the functional tests on Leap 16 with SELinux
enforcing; they fail on any denial for the module's types.

`agent_exec_t` → `agent_t` (`init_daemon_domain`), with `agent_conf_t`,
`agent_var_lib_t`, `agent_runtime_t` and `agent_sock_t`. `agent_t` gets
`mcp_gateway_client` (the MCP socket) but not
`mcp_gateway_control_client`. It may connect to HTTP ports for models
(boolean `agent_connect_any` for others). It cannot connect to
`agent_sock_t`. `mcpsrv_agents_t` (from the gateway's backend template)
may connect to `agent_sock_t`, and so may Cockpit's bridge
(`unconfined_t`).

## 19. Packaging

OBS packages for SLES 16.0/16.1 and openSUSE Leap 16.0/16.1
(`packaging/suse`): `custos` (requires `mcp-gateway` ≥ 1.12)
and `custos-selinux` (required where `selinux-policy-targeted`
is installed). There is no Cockpit page package yet.

---

# Part III — Traceability and open items

## 20. Requirement: Cockpit control plane for governed agentic AI (SLES 16.2)

| Requirement | Contract | State in this repository |
|---|---|---|
| See activity not started in chat (schedules, events), what runs, waits, ran | §3, §4, §5, §10 | host side done and tested (scheduled and API activities, states, history); Cockpit page to do |
| …including delegation chains, traceable to the origin | §4 `parent`/`root`, §9, §10 | done and tested (trace verified by `SO_PEERCRED`); bridge `agent-delegate` built, untested against a real gateway |
| Pending action as an approval item with actions, evidence, impact; nothing state-changing without approval | §6 supervised default, §7 | enforcement done in gateway policy (tested with OPA against the gateway's policy); approval flow tested end to end with a fake gateway; impact and evidence are the agent's self-asserted rationale plus the activity (O6) |
| Outcome after execution; proposed → approved/rejected → executed history | §4, §10 "History of an action" | done (journal and activity API) |
| Chat as one mode | §11 | AG-UI and ADK REST, tested |
| See autonomy mode and policy; adjust where permitted | §6 | gateway data + `agent-admin mode/permissions`; Cockpit page to do |
| Stop / pause / override in-flight or scheduled activity; emergency stop | §8 | cancel, pause (host and gateway), stop-all done; tested on the host side and in policy |
| On-premises, air-gapped | §14 (`provider: openai` with a local server), §19 (OBS) | done; no external service needed |
| Independent of kit / any host | Part I is host-independent; §3 discovery by contract only | contract done; a second host (kit adapter or a mock) to demonstrate (O8) |
| Fleet scope addressed | §12 | out of scope for 16.2, not precluded |

## 21. Open items

- **O1 — owner as approver.** Gateway approver rules are per server. A
  rule "the owner of agent X approves its calls" needs a group per
  owner, or a gateway rule such as `owner-of:<principal>`.
- **O2 — notice to call matching.** The gateway's approval notice names
  the tool, not the request. With two calls of one tool in flight at
  once, approval ids can be assigned crosswise. The gateway could put
  the JSON-RPC request id into the notice's data.
- **O3 — AG-UI versions.** Unknown `RunAgentInput` fields are ignored.
  Newer event types are emitted only once this contract adds them.
- **O4 — remote frontends.** AG-UI from other hosts needs
  authentication in front of the socket (Cockpit as the only entry, or
  an authenticating proxy). Not in version 1.
- **O5 — protocol revision 2026-07-28.** For "modern" clients the
  gateway forwards log messages only at a level the request asks for.
  The approval notice reaches today's (2025-11-25) MCP clients. A
  modern client must ask for `notice`.
- **O6 — proposals.** The approval item's rationale and impact come
  from the model's text and are self-asserted. A structured proposal
  (planned actions, expected impact) that the gateway shows with the
  approval would need a gateway field, e.g. in the call's `_meta`.
- **O7 — fleet.** A cross-host delegation transport, a host part in the
  trace, and an aggregated view.
- **O8 — second host.** Demonstrating host independence: a mock host
  (or a kit adapter) serving §5, §10 and §11 for the Cockpit page to run
  against.
- **O10 — OpenShell backend.** §3a and §16a are a draft. CI runs the
  backend with a real OpenShell on Docker (`test/openshell/run-spike.sh`,
  job `openshell`), against mcp-gateway's own HTTPS listener (in its
  development mode) with the gateway's policy and the agents module,
  and a test token issuer (`test/vm/testidp`). Settled by the spike: the
  token stays with the supervisor; the control API is relayed with the
  caller; the agent's records reach the host journal through
  the relay's bridge, from the sandbox container's output (OpenShell's
  own log stream has only the supervisor's lines); the agent is
  `agent-<name>` over HTTPS too, with its mode and approvals. Still open:
  - the issuer of the agents' tokens in production: the site's IdP with
    a client per agent whose tokens have the subject `agent-<name>` and
    the gateway's audience (in Keycloak a service-account client with
    mappers), or a local issuer. Their renewal is settled: OpenShell's
    gateway mints and renews them with the client credentials grant, and
    the spike sees a call succeed on a renewed token after the first
    expired, without restarting the agent;
  - SLES 16 / Leap 16 with Podman and SELinux enforcing: settled. CI's
    `openshell-podman` job runs the backend in the Leap 16 VM with the
    installed packages, rootless Podman and SELinux enforcing
    (`test/vm/run-vm.sh` with `SUITE=openshell`): the whole spike passes
    with no denial since boot, and the job fails on a denial for the
    repository's domains. What it needs is in §16a (the OpenShell user,
    the relay's drop-in, the controllers delegated to user sessions);
  - OpenShell's packaging for SLES and Leap, and its maturity (0.1.x).
- **O9 — Cockpit page.** The control plane itself: the views of the
  mockup (`docs/mockup.png`), built only on Part I and the gateway's
  control API.
