# mcp-gateway requirements from Custos 0.4

Status: proposed. For an mcp-gateway session to implement; Custos
follows once a gateway release has it. The general constraints of
`mcp-gateway-for-custos-0.3.md` apply: nothing Custos-specific in the
gateway, policy decides who sees what, changes only add, and the
gateway's own records stay trustworthy.

## R5. A proposal with the call, shown with the approval

**Need.** A person who decides on a pending call sees, on the gateway's
side, the exact call: principal, server, tool, arguments. Why the agent
makes it, what it expects it to change, and what came before is only on
Custos's side: the activity API (`calls[].rationale`,
`calls[].suspected_injection`, `calls[].preceding`; Custos `CONTRACT.md`
§4, §7), which Cockpit's Approvals tab joins to the gateway's item by
approval id. Who decides on the gateway's approval page, with
`mcp-gateway-admin approvals`, from a desktop notification or a mail
sees none of it. Custos `CONTRACT.md` O6 asks for a structured proposal
that the gateway shows with the approval.

**Today** (mcp-gateway `main` at 6f19ded). A pending approval
(`internal/broker`, `Pending`) has the call, the policy's prompt, the
scopes and times. The router reads `_meta.progressToken` of a
`tools/call` (`internal/router/keys.go`) and names the call in its
approval notice (`io.github.sdrahn.mcp-gateway/approval`); nothing else
the client says about the call reaches the approval.

**Requirement.**

1. A client may send a **proposal** with a `tools/call`, in its `_meta`
   under one documented key. The gateway keeps it with the pending
   approval the call raises, and with the review item a call becomes
   (R4).
2. A proposal has: a summary (one line, the client's reason for the
   call), an expected impact, optional planned steps (a short list of
   lines), optional context (key and value pairs of plain text, such as
   the activity and who started it), and optional warnings (a short
   list of lines, such as the signs of a suspected prompt injection).
   All plain text; each field and the whole bounded in size. A proposal
   that is malformed or too large is dropped, not the call: the gateway
   records that it dropped one.
3. It is shown wherever the approval is: `GET /v1/approvals` and
   `/v1/events` (a new field), the approval page, `mcp-gateway-admin
   approvals`, the mail and the desktop notification (the summary and
   any warnings). Everywhere it is marked as **the client's claim**,
   apart from the gateway's own facts (principal, call, arguments,
   policy).
4. It is never forwarded to the MCP server, and it changes no decision:
   policy does not see it (a client could say anything in it).
5. The gateway's audit of the approval (`mcp-approval`) records a digest
   of the proposal, so a decision can be tied to what the approver was
   shown; the proposal itself only as the policy's `audit` obligation
   lets arguments be recorded.
6. A grant made for a session or a time (approve with a scope) covers
   later identical calls as today; their proposals are not shown to
   anyone, as no one decides on them.

**Proposed interface.**

```json
{"method": "tools/call", "params": {"name": "zypp__patch", "arguments": {…},
  "_meta": {"progressToken": "custos:e-…/call_3",
    "io.github.sdrahn.mcp-gateway/proposal": {
      "summary": "Install the 2 security patches for openssl; nginx restarts.",
      "impact": "nginx is unavailable for about 5 s.",
      "steps": ["zypper patch --cve CVE-2026-1234", "systemctl restart nginx"],
      "context": {"activity": "e-…", "started_by": "schedule morning-check", "agent": "patch-pilot"},
      "warnings": ["suspected prompt injection: the output of zypp__list_updates tells the model to ignore its instructions"]}}}}
```

`GET /v1/approvals` adds `"proposal": {…, "client_supplied": true}` to
an item that has one. Limits, for instance: summary 300 bytes, impact
1000, 10 steps and 10 warnings of 300 bytes each, 10 context pairs, 4 KiB
in all.

**Done when:**

- a call with a proposal raises an approval whose `GET /v1/approvals`
  item, approval page, `mcp-gateway-admin approvals` output, mail and
  desktop notification show it, marked as the client's;
- a call without one shows as today;
- a malformed or oversized proposal is dropped and the call still waits
  for its approval, with the drop recorded;
- the MCP server never receives the key; policy input does not hold it;
- `mcp-approval` carries the proposal's digest;
- an approved call with a session grant: the next identical call runs
  without anyone seeing its proposal;
- the review item of a call with a proposal (R4) shows it.

**Then Custos** sends a proposal with every call it makes through the
gateway: the summary is the model's rationale (`calls[].rationale`),
the context the activity, its origin and its delegation chain, the
warnings the suspected-injection signs (`calls[].suspected_injection`).
Approvers then see Custos's account of the call on the gateway's own
page and in mails, not only in Cockpit. Planned steps and impact follow
when the agent's model states them (a convention for the model's text,
or the report tool's structure, §4a), which is Custos's part.
(Custos `CONTRACT.md` §7, O6.)

## R6. A call over a rate limit asks for an approval

**Need.** A promoted (bounded) task category acts without approval
within its envelope; the envelope's `rate` caps how often (Custos
`CONTRACT.md` §6a, O12). Custos enforces `rate` today with the gateway's
`rate_limit` obligation, so a call over it is **denied** ("rate limit
exceeded"). What the requirement owner wants is what the envelope means:
beyond the rate the category is supervised again, so a call over it
**waits for an approval** and a person decides whether the agent may go
on. A denial stops work that may be right (the twenty-first patch of a
large errata), and the agent can only give up or retry.

**Today** (mcp-gateway `main` at 68a90eb). The policy decides first
(`allow`, `ask` or `deny`); for `allow`, the router compiles the
decision's obligations and then asks the limiter (`internal/pep`,
`Limiter.Allow`, keyed per principal, action, server and target); over a
rate it denies (`internal/router/session.go`). The counter is consulted
only after the decision, so the policy cannot turn "over the rate" into
`ask`, and the gateway never asks for an approval for an allowed call.

**Requirement.**

1. A new obligation, **`approval_over_rate`**: `N/s`, `N/m` or `N/h`, one
   or a list, counted as `rate_limit` is (per principal, action, server
   and target; a sliding window; in memory). A call of an `allow`
   decision that the rates still permit runs, and is counted. A call
   over one of them is not denied: it **raises an approval**, as an
   `ask` decision of the policy would, with the policy's input for
   that call.
2. Such an approval:
   - goes to whoever may decide the principal's calls on that server
     (the approvals policy, as for any approval), through the
     out-of-band channel (`oob`): notifications, the approval page,
     `/v1/approvals`, `mcp-gateway-admin approvals`;
   - offers the scope `once` only. A grant for a session or a time would
     lift the rate for later calls, which the obligation is there to
     prevent;
   - says why it is asked: its prompt names the rate that was reached
     (for example "rate 20/h reached for agent-patch-pilot on zypp
     patch"), and its item carries the rate as a field
     (`over_rate: "20/h"`), apart from any proposal (R5);
   - waits up to `approval_timeout`, as any approval; declined or timed
     out, the call is denied as today.
3. A call approved this way runs and is **not counted**: a person
   decided on it. Its `mcp-result` names the approval as for any
   approved call.
4. Audit: the decision record of a call over the rate carries
   `over_rate` (the rate reached), and so does the approval's
   `mcp-approval`. A denial by `rate_limit` stays as it is ("rate limit
   exceeded").
5. `rate_limit` is unchanged. The two may be combined: a permission
   with both denies over `rate_limit` first (the hard cap) and asks
   over `approval_over_rate`. Merged across matching permissions as
   `rate_limit` is (the lists add up).
6. The policy input does not change: the policy still decides without
   the counters. The shipped policy merges the new key from matching
   permissions as it merges `rate_limit` (`obligation_sets` in
   `policy/mcp/authz.rego`). An older gateway drops a key it does not
   know (its policy merges known keys only), so a module cannot tell
   from the decision whether it is enforced: the release that has it
   says so in its changelog, and Custos requires that release before it
   writes the key (below).

**Proposed interface.**

```yaml
# in a permission (or a module's decision)
obligations:
  approval_over_rate: [20/h]
```

`GET /v1/approvals` item: `"over_rate": "20/h"`, `"scopes": ["once"]`.
Audit: `{"msg": "mcp", "audit": true, …, "effect": "ask", "over_rate": "20/h"}`
and `{"event": "mcp-approval", …, "over_rate": "20/h"}`.

**Done when:**

- a permission with `approval_over_rate: 2/m`: the first two calls in a
  minute run without an approval, the third waits for one;
- that approval offers `once` only, names the rate in its prompt and
  item, and reaches the server's approvers out of band;
- approved, the call runs and is not counted; declined or timed out, it
  is denied;
- the decision and `mcp-approval` records carry `over_rate`;
- with `rate_limit` and `approval_over_rate` together, the hard cap
  denies;
- a permission's `approval_over_rate` reaches the decision through the
  shipped policy, merged across matching permissions.

**Then Custos** writes the envelope's `rate` as `approval_over_rate`
instead of `rate_limit` (`gateway/policy/agents.rego`), and the package
requires the gateway release that has it (until then Custos keeps
`rate_limit`, which denies); the demotion evaluator counts the
category's approvals over its rate (`over_rate` in `mcp-approval`, or
in the decision record) for "rate cap reached repeatedly" (three times
in a day); `CONTRACT.md` §6a says again that a call over the rate needs
an approval, and O12 closes but for a counter across a category's tools.

## R7. A server that sees the host's network, without network access

**Need.** Custos's diagnostic playbook `network-config` reads the host's
interfaces, routes and listening ports with three commands of the exec
server (`exec.d-diagnostics.yaml`: `ip -brief address`, `ip route show`,
`ss -tulnH`). In the exec server they cannot see them: without
`network: true` an instance has a private network namespace and only
`AF_UNIX` sockets. `ip` fails (no `AF_NETLINK`), and `ss` falls back to
`/proc/net` of the empty namespace and reports **no** listening sockets,
a wrong answer that looks right. Custos's functional tests found it
(custos#96). `network: true` would let the commands reach the network,
which a read-only diagnostic must not.

**Today** (mcp-gateway `main`). `internal/supervisor/systemd.go`
`sandboxProperties`: `PrivateNetwork=!network`, and without network
`RestrictAddressFamilies=AF_UNIX`. User guide chapter 4, "Definition
keys" (`network`) and "The sandbox".

**Requirement.**

1. A definition key that lets an instance **see** the host's network
   without **using** it, for instance `network_view: host` (default
   `none`, today's behaviour), valid only with `network: false`:
   - no private network namespace: the instance shares the host's, so
     interfaces, addresses, routes and sockets are the host's;
   - sockets of `AF_UNIX` and `AF_NETLINK` only (netlink reads the
     interfaces, routes and sockets; without capabilities it changes
     none of them);
   - `IPAddressDeny=any`, so that no IP traffic goes in or out, should
     a socket family be added later;
   - everything else of the sandbox as with `network: false`.
2. A definition with `network_view: host` and `network: true` is
   refused (`mcp-gateway --check`, the reload), as is an unknown value.
3. `mcp-gateway-admin doctor` and `GET /v1/servers` show the setting
   (`network_view`), and the user guide says what it opens: what the
   host's network looks like to the server, never a connection.
4. An instance's SELinux domain still decides what it may do with
   netlink; the gateway's shipped domains gain nothing.

**Done when:**

- an exec instance of a definition with `network_view: host` runs
  `ip -brief address`, `ip route show` and `ss -tulnH` and sees the
  host's interfaces, routes and listening sockets;
- the same instance cannot open an `AF_INET` or `AF_INET6` socket, nor
  send IP traffic;
- without the key, nothing changes;
- `network_view: host` with `network: true`, or an unknown value, is
  refused.

**Then Custos** ships a server definition of its own for the network
diagnostics, running `mcp-server-exec` with those three commands (in
`mcpsrv_exec_t`, its module adding the netlink rules and `ip`'s label,
`ifconfig_exec_t`), with `network_view: host`; the playbook
`network-config` and the template `health-check` use its tools; the
package requires the gateway release that has R7; and the functional
tests check that the commands report the host's interfaces, routes and
sockets.
