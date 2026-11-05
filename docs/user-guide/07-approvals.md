---
title: "7. Approving an agent's calls"
description: How an agent's change waits for a person, who may decide, deciding in Cockpit, on the command line or over the gateway's control API, scopes and grants, timeouts, notifications, and who the record names.
tags: [approvals, approvers, grants, scopes, decline, oob]
---

# 7. Approving an agent's calls

Approvals belong to mcp-gateway: neither an agent nor Custos can grant
or forge one. This chapter is about deciding them for agents. The
gateway's own chapter on approvals (mcp-gateway user guide, chapter 7)
covers its channels, the approval page and notifications in full.

## What happens while a call waits {#flow}

1. The agent calls a tool its permissions allow only with an approval
   ([chapter 6](06-permissions-and-modes.md#modes)). The gateway creates
   a pending approval with an id such as `a-6c88df4c32d09c66…` and holds
   the call.
2. The gateway tells the agent "Waiting for approval of zypp/confirm_install … (id a-…)".
   The agent marks the call and its activity **waiting for approval** and
   writes `approval.pending` to the journal.
3. A person who may decide approves or declines it.
4. The call runs, or comes back declined. The agent writes
   `approval.resolved` and `tool.result`; the gateway records the
   decision and the approver.

The call waits up to the gateway's `approval_timeout` (120 s by default,
in `/etc/mcp-gateway/gateway.yaml`). A call that gave up leaves the
approval pending. Approving it then lets the agent's next identical call
through, which is what happens when an agent tries again.

## Who may decide {#who-may-decide}

The gateway's approver rules, per server, in its role data
([chapter 3](03-gateway.md#approvers)). With the shipped default,
members of `wheel` (the role `admin`) may decide everything. The rule
`self` never lets an agent approve its own call: agent users have no
password, and agents cannot reach the gateway's control socket.

## Deciding in Cockpit {#cockpit}

The *Approvals* tab of the Custos page lists the pending calls, each as
one item with two halves:

| The call, from mcp-gateway | The agent's account (self-asserted) |
|---|---|
| server and tool, the agent's principal (`agent-patch-pilot`, uid, by `SO_PEERCRED`), the policy that asked, who may decide, how it was asked (the channel), when it times out, the exact arguments | the agent's stated reason and impact, who started the activity and its delegation chain, its earlier calls as evidence |

Only the left half is trustworthy: it comes from the gateway. The right
half is what the agent says about itself.
If the agent is no longer waiting for the call (it ended, or the gateway
restarted), the item says so: approving then lets its next identical
call through.

The buttons are the scopes the gateway offers: **Approve once** (this
call only), **Approve for this session**, **Approve for 1 h** (a grant:
later identical calls in that time run without asking), and
**Decline**. *Open activity* shows the whole activity. *Decided today*
lists today's decisions from the journal.

## Deciding on the command line {#command-line}

As a person who may approve, not as root:

```bash
mcp-gateway-admin approvals                       # the pending approvals you may decide
mcp-gateway-admin approvals show a-6c88          # one, with its arguments (an id prefix is enough)
mcp-gateway-admin approvals approve a-6c88 --scope once
mcp-gateway-admin approvals deny a-6c88
mcp-gateway-admin approvals watch                 # as they come
```

Or over the gateway's control API, as that person:

```bash
curl -s --unix-socket /run/mcp-gateway/control.sock http://localhost/v1/approvals | jq .
curl -s --unix-socket /run/mcp-gateway/control.sock http://localhost/v1/approvals/a-6c88df4c32d09c66eb46bc8941b53745 \
  -X POST -d '{"decision": "approve", "scope": "once"}'
```

## Who the record names {#who-is-recorded}

The gateway records the approver it sees on its control socket. The
Custos page therefore always sends decisions through the person's own
Cockpit session, never through administrative access, and adds
`"via": "cockpit"`. The gateway's audit then has, for example:

```json
{"audit": true, "event": "mcp-approval", "id": "a-6c88…", "ok": true, "by": "carol", "scope": "once", "via": "cockpit", "principal": "agent-patch-pilot", "server": "zypp", "target": "confirm_install"}
```

Decide as yourself: a decision made as root (for example `sudo
mcp-gateway-admin approvals approve`) is recorded as root's.

## Grants and revoking them {#grants}

A scope other than `once` creates a grant in the gateway: later calls
it covers run without asking until it expires. Revoke one in
mcp-gateway's own Cockpit page (*MCP Gateway*, *Approvals*, *Grants*),
or with `curl … -X DELETE http://localhost/v1/grants/<id>` on its
control socket. To give no standing grants for a permission, limit its
scopes: `approval_scopes: [once]` ([chapter 6](06-permissions-and-modes.md#permissions)).

## Getting told about pending approvals {#notifications}

The Approvals tab shows a count. For people who are not looking at
Cockpit, the gateway can notify by desktop notification, e-mail or
webhook (mcp-gateway user guide, chapter 7). To make those links open
the Custos page, set the gateway's `approvals.url_template`
([chapter 3](03-gateway.md#approval-links)).

## Following a decision afterwards {#history}

The *Audit* tab shows each state-changing action's history: proposed by
the agent (with its rationale), decided at the gateway (approved or
declined, by whom, with which scope, via what), and executed or not.
From the journal:

```bash
journalctl -o json AGENT_EVENT=approval.resolved AGENT_APPROVAL_ID=a-6c88df4c32d09c66eb46bc8941b53745
journalctl -u mcp-gateway.service -o cat | grep '"event":"mcp-approval"' | grep a-6c88df4c32d09c66eb46bc8941b53745
```

[Chapter 10](10-monitoring-and-audit.md) has more queries.
