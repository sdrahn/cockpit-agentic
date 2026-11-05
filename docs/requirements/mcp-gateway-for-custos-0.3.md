# mcp-gateway requirements from Custos 0.3

Status: proposed. For an mcp-gateway session to implement; Custos
follows once a gateway release has them.

Custos (this repository) runs governed AI agents on SLES 16 and Leap 16.
mcp-gateway decides every call an agent makes: Custos writes each
agent's permissions as gateway data (`/etc/mcp-gateway/policy/agents/`)
and adds its own Rego modules (`/usr/share/mcp-gateway/policy/custos/`:
`agents.rego` in `package mcp.authz`, `owners.rego` in
`package mcp.approvals`). Custos's contract (`CONTRACT.md`) requires
that security properties be enforced by mcp-gateway, systemd and
SELinux, never only inside the agent's process.

This document lists four things Custos needs from mcp-gateway. Each
has the need, what the gateway does today (as of mcp-gateway `main` at
bfcefc8), the requirement, a proposed interface, the tests that show it
is done, and what Custos changes once it is there. The interfaces are
proposals: the gateway session may choose others that meet the
requirement.

## General constraints

- **Nothing Custos-specific in the gateway.** Custos is one user of
  these features. Names, fields and endpoints are generic (a principal,
  a server, a tool; "agent" only where the gateway already uses it).
  Custos's own concepts (task categories, promotion, demotion) stay in
  Custos's data and Rego modules.
- **Policy decides.** Who sees, decides or is notified of anything is
  answered by the gateway's policy (`data.mcp.approvals` and friends),
  so that sites and modules such as Custos's can extend it as they
  extend `allow` and `notify` today.
- **Compatible.** Audit records gain fields and events; existing ones
  keep their names and meaning. Control API endpoints are added, not
  changed. Without the new data or policy, the gateway behaves as
  today.
- **Audit records stay trustworthy.** They are written by the gateway,
  never copied from what a client claims, except where a field says so
  (such as the progress token below, which only correlates).

## R1. A reason for a denial, given by a policy module

**Need.** On a host that started diagnostics-only (Custos `CONTRACT.md`
§6b), a permission of a task category that is not enabled is no
permission: `agents.rego` leaves it out of the agent's permissions, so
the call is denied. The gateway's audit then says
`"reason": "no matching permission"`, the same as for a call no one
ever permitted. An administrator reading the audit cannot tell "the
category is not enabled on this host" from "the agent was never given
this". The same holds for other denials Custos causes (`denied` rules
in `agents.rego`: paused, emergency stop, an unknown mode, a role bound
to an agent user): they all read `"denied by policy"`.

**Today.** `policy/mcp/authz.rego`: `default decision := {"effect":
"deny", "reason": "no matching permission"}`, and `{"effect": "deny",
"reason": "denied by policy"}` when `denied`. The decision's `reason`
reaches the audit record (`internal/audit`, field `reason`) and the
kernel audit message. A module that adds to `denied` or leaves a
permission out cannot add to the reason.

**Requirement.**

1. A policy module can give the reason for a denial it causes, without
   replacing `decision`.
2. A module can also explain a request that matches no permission
   (the default denial): "this would have been permitted, but …".
3. When several modules give reasons, the decision carries all of
   them, in a stable order.
4. The reason reaches the audit record (`reason`), the kernel audit
   message, and the error the client receives, as today's reasons do.
5. Without such reasons, decisions and records are as today.

**Proposed interface.** Two partial sets in `package mcp.authz`, which
`decision` consults:

```rego
# A module that adds to `denied` also says why:
deny_reasons contains "the agent is paused" if { … }

# A request that matches no permission, explained:
unmatched_reasons contains sprintf("task category %s is not enabled on this host", [c]) if { … }
```

- `decision` when `denied`: `"reason": concat("; ", sort(deny_reasons))`
  if `deny_reasons` is not empty, else `"denied by policy"`.
- The default decision: `"reason": concat("; ", sort(unmatched_reasons))`
  if `unmatched_reasons` is not empty, else `"no matching permission"`.
- Discovery (`data.mcp.filter`) is unchanged.

**Done when** (Rego tests in `policy/mcp`, and a functional test):

- a module's `deny_reasons` appears as the decision's reason and in
  the audit record of the denial;
- a module's `unmatched_reasons` replaces "no matching permission" for
  a request that matches no permission, and only then;
- two reasons from two modules are both in the reason, sorted;
- without the sets, every existing test passes unchanged.

**Then Custos** adds to `agents.rego`:

- `unmatched_reasons`: "task category X is not enabled on this host
  (diagnostics-only)" for a call to a tool that a permission of a
  disabled category names;
- `deny_reasons`: "the agent is paused", "emergency stop", "unknown
  mode", "a role is bound to an agent user", "subject from an issuer
  not trusted for agents".

The user guide's troubleshooting table then names the audit reason for
each. (Custos `CONTRACT.md` §6b, O16.)

## R2. The outcome of each call in the audit

**Need.** Custos demotes a promoted task category when one of its
autonomous calls fails (`rollback: {on: [tool_error]}`), and its
promotion readiness counts incidents (approved calls whose result is
an error). Today both read the agent's own `tool.result` journal
records. An agent writes those itself: a compromised or misbehaving
agent could leave out its errors and keep a category promoted. The
gateway forwards every call and its result, so it knows the outcome
without trusting the agent.

**Today.** The gateway audits the decision (`"msg": "mcp"`, with
`effect` allow, deny or ask) before forwarding a call, and approval
decisions (`"event": "mcp-approval"`). It does not record whether the
forwarded call succeeded.

**Requirement.**

1. For every `tools/call` the gateway forwards, it writes one audit
   record when the result (or an error) comes back, or when the call
   ends without one (cancelled, timed out, the backend gone).
2. The record has: the session, `sub`, `server`, `name` (the tool), the
   `decision_id` of the decision that let the call through, the
   approval id when the call was approved, the grant id when a grant
   allowed it, the outcome, and the duration.
3. Outcome: `ok`; `tool_error` (the result had `isError: true`);
   `error` (a JSON-RPC error from the backend, or the gateway could not
   reach it); `cancelled`; `timeout`. With `tool_error` and `error`, a
   short error text from the result, bounded in length, and only as the
   policy's `audit` obligation allows (the digest mode logs none).
4. The client's `_meta.progressToken` for the call, if it sent one, as
   `progress_token`, marked in the documentation as client-supplied
   (for correlation only).
5. The record is written by the gateway whatever the client does with
   the result.

**Proposed interface.** One JSON line per call, as the existing audit
records:

```json
{"audit": true, "event": "mcp-result", "session": "…", "sub": "agent-patch-pilot",
 "server": "zypp", "name": "patch", "decision_id": "…", "approval_id": "a-…",
 "outcome": "tool_error", "error": "zypper exited 4", "duration_ms": 8123,
 "progress_token": "custos:e-…/call_3"}
```

**Done when** (Go tests and a functional test with a test backend):

- a call whose result has `isError: true` is recorded with
  `outcome: tool_error`, its decision id and duration;
- a successful call: `outcome: ok`; a backend that dies mid-call:
  `error`; a call the client cancels: `cancelled`;
- an approved call's record names the approval id; a grant's names the
  grant;
- the error text is absent in digest mode and bounded otherwise;
- a client that never reads the result still gets its call recorded.

**Then Custos** reads `event == "mcp-result"` records with
`sub == "agent-<name>"` for the rollback condition `tool_error` and the
incident rate, maps server and tool to a task category from the agent
data, and keeps the agent's `tool.result` only as a cross-check.
(Custos `CONTRACT.md` §6a.)

## R3. Notices to approvers from a trusted local service

**Need.** Custos's demotion evaluator (root, `custos-demote.timer`)
sets a promoted task category back to supervised when its override
rate rises or a rollback condition holds. The people who approve that
agent's calls should hear of it where they hear of pending approvals:
the gateway's mail and desktop notifications, and the approval page.
Today Custos only shows the demotions in its own Cockpit page.

**Today.** The gateway notifies of pending approvals only: mail to the
`notify` rules of `package mcp.approvals` (`internal/notify`), desktop
notifications through `mcp-gateway-notify` (the control socket's
`/v1/events`, `"type": "pending"`), and the approval page.

**Requirement.**

1. A local service with root privileges can post a notice about a
   principal through the control API. Others are refused (a policy
   rule decides who may post; by default root only).
2. A notice has a title, a text (plain, bounded in length), a
   severity (`info`, `warning`), the principal it concerns, and
   optionally the servers it concerns.
3. Its audience is decided by policy, as for a pending request of that
   principal on those servers: whoever `allow` would let decide, and
   the `notify` rules for mail. So the servers' approvers and an
   agent's owners (Custos's `owners.rego`) both get it.
4. It is delivered through the channels a pending approval uses: mail
   to the `notify` recipients, `/v1/events` (a new event type) to those
   who may see it, and a list on the approval page. It asks for no
   decision. A reader may dismiss it for themself.
5. Notices expire (default 7 days) and are audited when posted
   (`"event": "mcp-notice"`, with who posted it and the audience).

**Proposed interface.**

```
POST /v1/notices   {"principal": "agent-patch-pilot", "servers": ["zypp"],
                    "severity": "warning", "title": "patch-errata demoted",
                    "text": "Override rate 7.1 % above 5.0 %: back to supervised."}
GET  /v1/notices   the notices the caller may see
GET  /v1/events    adds {"type": "notice", "notice": {…}}
```

Policy: `data.mcp.approvals.post_notice` (who may post; default: uid
0), and the audience from `allow`/`notify` with
`input.request = {"principal": …, "servers": …}`.

**Done when:**

- root posts a notice; a non-root caller is refused;
- an approver of the named server and an owner (by an added `allow`
  rule) see it in `GET /v1/notices` and on `/v1/events`; another user
  does not;
- the `notify` recipients receive one mail;
- `mcp-gateway-notify` shows it as a desktop notification;
- the approval page lists it until dismissed or expired;
- the post is audited.

**Then Custos**'s demotion evaluator posts a notice for each automatic
demotion (title, trigger, the agent and category), and its Cockpit
page links to the gateway's page. (Custos `CONTRACT.md` §6a, O11.)

## R4. Reviews of autonomous actions by the gateway's approvers

**Need.** A promoted ("bounded") task category acts without an
approval inside its envelope. Its override rate cannot be measured
from approvals there, so Custos `CONTRACT.md` §6a has approvers review
a sample of its autonomous actions afterwards: "fine" or "should not
have run" (an incident, which counts as an override and can demote the
category). Today `agent-admin review` records the verdict, as root.
The people who should review are the agent's approvers, and the
gateway already authenticates them (kernel identity on the control
socket, sign-in on the approval page) and decides who may approve the
agent's calls.

**Today.** The gateway has approvals before a call. It has nothing
for a decision about a call that already ran.

**Requirement.**

1. Policy can mark an allowed call for review: an obligation on the
   decision (such as `"review": true`), so that Custos's module decides
   which calls (its autonomous ones, sampled) and the gateway stays
   generic.
2. After such a call ran (R2's record), the gateway keeps a review
   item: the call as an approval item shows it (principal, server,
   tool, arguments as the policy's audit obligation allows, time), its
   outcome, and the decision id.
3. Who may review is decided by policy as for approvals (`allow` with
   the item as `input.request`), so the servers' approvers and owners
   can.
4. A reviewer gives a verdict, `ok` or `incident`, with a reason
   (required for `incident`). A later verdict on the same item
   replaces the earlier one. Self-review follows the same rule as
   self-approval (`allow_without_self`).
5. Review items are offered where approvals are: the control API, the
   approval page, `/v1/events`, `mcp-gateway-admin`. They expire
   (default 7 days) without a verdict.
6. Each verdict is audited (`"event": "mcp-review"`, with the decision
   id, the reviewer as `by`, `verdict`, `reason`).

**Proposed interface.**

```
obligation:  {"review": true}
GET  /v1/reviews          the items the caller may review
POST /v1/reviews/{id}     {"verdict": "incident", "reason": "restarted the wrong unit"}
audit:       {"audit": true, "event": "mcp-review", "id": "r-…", "decision_id": "…",
              "sub": "agent-patch-pilot", "server": "systemd", "name": "restart_unit",
              "verdict": "incident", "reason": "…", "by": "alice"}
```

**Done when:**

- a call allowed with the review obligation becomes a review item
  once it ran; one without does not;
- an approver of its server and an owner may review it; another user
  may not; the principal itself may not without the password, as for
  approvals;
- `incident` without a reason is refused;
- a second verdict replaces the first, and both are audited;
- the item appears in `/v1/reviews`, on the approval page and on
  `/v1/events`, and expires.

**Then Custos**: `agents.rego` adds the review obligation to a sample
of a bounded category's autonomous calls (the rate from the category's
data). Readiness and the demotion evaluator count `mcp-review`
verdicts as they count `category.review` today. `agent-admin review`
stays for root, and Cockpit's review queue links to the gateway's.
(Custos `CONTRACT.md` §6a, O11.)

## Order

R1 and R2 are small and independent; R2 closes a trust gap (the
rollback condition and incident rate rest on the agent's own records).
R3 builds on the existing notification paths. R4 builds on R2 (the
outcome) and on the approval machinery, and is the largest.
