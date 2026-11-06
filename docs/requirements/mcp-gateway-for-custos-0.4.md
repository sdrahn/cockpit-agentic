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
