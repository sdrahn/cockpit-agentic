---
title: "6. Permissions and autonomy modes"
description: What an agent may do at the gateway, how a permission is written (server, tool, args, read_only, autonomous, category), the three autonomy modes, task categories and promoting one to act without approval within its time windows, and checking what the gateway would decide.
tags: [permissions, autonomy, modes, supervised, bounded, advisory, policy, categories, promote, demote, envelope]
---

# 6. Permissions and autonomy modes

What an agent may do is decided by mcp-gateway from two things in the
agent's gateway data: its **permissions**, the tools it may call at all,
and its **autonomy mode**, whether those calls need a person. Both are
set with `agent-admin` (or the *Agents* tab in Cockpit) and enforced by
the gateway, whatever the agent does.

## The three autonomy modes {#modes}

| Mode | Read-only permissions | Other permissions | Permissions marked `autonomous` |
|---|---|---|---|
| `advisory` | run | denied | denied |
| `supervised` (the default) | run | wait for an approval | wait for an approval |
| `bounded` | run | wait for an approval | run without one |

- **advisory**: the agent can look and advise, never change.
- **supervised**: every change waits for a person. A new agent starts
  here.
- **bounded**: like supervised, but changes an administrator marked
  `autonomous`, usually narrowed by `args`, run without asking.

Set the mode:

```bash
agent-admin mode patch-pilot bounded
```

The gateway applies it within seconds; `agent-admin` returns once it is
in force. A tool list change reaches the agent at its next call.

## Writing permissions {#permissions}

The permissions are a YAML list, replaced as a whole:

```bash
agent-admin permissions patch-pilot < perms.yaml
```

```yaml
# perms.yaml
- {server: zypp, tool: check_updates, read_only: true}
- {server: zypp, tool: confirm_install}
- {server: systemd, tool: change_unit_state, autonomous: true, args: {name: '^nginx\.service$', action: '^restart$'}}
```

| Key | Meaning |
|---|---|
| `server` | the gateway server's name; a glob (`*`) matches several |
| `tool` | the tool's name on that server; a glob, such as `'find_*'` |
| `resource`, `prompt` | instead of `tool`: an MCP resource or prompt of the server |
| `args` | per argument, a regular expression its value must match; the call is refused otherwise |
| `read_only: true` | the tool only reads: it runs in every mode without an approval |
| `autonomous: true` | in `bounded` mode, this change runs without an approval |
| `approval_scopes` | the scopes an approver is offered, as in the gateway's permissions, such as `[once]` so that no standing grant can be given |
| `effect`, `obligations` | as in the gateway's permissions (mcp-gateway user guide, chapter 6), for example an obligation for full audit of the arguments |

Rules:

- **Read-only only where it says so.** Without `read_only: true`, a
  permission counts as a change. An MCP server's own `readOnlyHint` never
  makes a tool read-only.
- **Unknown keys are refused** (`permissions[2]: unknown key "allow_everything"`),
  and each entry needs `server`.
- **An approval of an agent always goes out of band** (the gateway's
  `oob` channel): to the Approvals tab, the gateway's notifications and
  its command line. The agent is never asked.
- **Unlisted tools are denied.** The gateway also hides them from the
  agent.

## Choosing permissions for a task {#examples}

| The agent should | Permissions |
|---|---|
| report on updates | `{server: zypp, tool: check_updates, read_only: true}` |
| install updates, each with approval | the above, and `{server: zypp, tool: confirm_install}` |
| restart one service on its own (bounded mode) | `{server: systemd, tool: change_unit_state, autonomous: true, args: {name: '^nginx\.service$', action: '^restart$'}}` |
| hand work to another agent | `{server: agents, tool: delegate, read_only: true, args: {agent: '^patch-pilot$'}}` ([chapter 3](03-gateway.md#delegation)) |

Narrow `args` with anchored expressions (`^…$`): `nginx\.service` alone
would also match `my-nginx.service.bak`.

## Pausing an agent at the gateway {#pause}

`paused: true` in the agent's data makes the gateway deny the agent
every call, in every mode. Set it with `agent-admin pause NAME`
([chapter 9](09-administration.md#pause)). The emergency stop does the
same for all agents ([chapter 9](09-administration.md#emergency-stop)).

## Seeing what an agent may do {#posture}

```bash
agent-admin show patch-pilot | jq '.agents["patch-pilot"]'
```

prints the mode, whether it is paused (and by whom) and the permissions.
`agent-admin show` needs no root: the files are readable by everyone.

In Cockpit:

- *Agents*, the agent's **Tool boundaries**: each permission, what it is
  declared as, its argument rules, and its **effect now** in the current
  mode (allowed, needs approval, allowed without approval, denied).
- *Policy*, **Posture**: per agent, how many permissions run, wait or run
  autonomously, and who may approve them.
- *Policy*, **What would the gateway decide?**: pick an agent, type
  `server__tool`, and see the decision and which permission makes it.
  It does not check `args`. The gateway's own decision is the one that
  counts.

## Task categories {#categories}

A task category names a kind of work, such as `patch-errata`. An
agent's autonomy is promoted one category at a time, never for the whole
agent: promoting `patch-errata` changes nothing for `config-lifecycle`.
The catalog ships these categories:

| Category | For |
|---|---|
| `diagnostics` | reading host state, logs and metrics, and reporting: read-only permissions only, never promoted ([below](#diagnostics-only)) |
| `patch-errata` | installing patches and errata, refreshing repositories |
| `security-response` | responding to a security finding: isolating a service, rotating a credential, blocking an address |
| `config-lifecycle` | changing configuration files; starting, stopping or restarting services |
| `compliance-audit` | checking the system against a compliance profile and applying its remediations |

`agent-admin categories` lists the catalog. To add a category, put a
file in `/etc/custos/categories.d/`, for example `backups.yaml` with
`backups: {title: Backups}`. A file with a shipped name
(`patch-errata.yaml`) replaces that one, and an empty one disables it.

A permission joins a category with `category:`:

```yaml
- {server: zypp, tool: check_updates, read_only: true}
- {server: zypp, tool: patch, category: patch-errata}
- {server: systemd, tool: change_unit_state, category: config-lifecycle, args: {name: '^nginx\.service$', action: '^restart$'}}
```

A permission in a category is supervised until its category is
promoted, whatever the agent's mode. `advisory` still caps everything.
A permission without a category keeps the agent's mode and its
`autonomous` flag, and `autonomous` cannot be combined with `category`.

## Diagnostics-only hosts and enabling categories {#diagnostics-only}

A host installed with the bundled health-check and diagnostics agent is
**diagnostics-only**: only the `diagnostics` category can be used. The
other categories are installed but disabled, and so are permissions
without a category. The gateway enforces it: a call of a disabled
category is denied in every mode, and the agent is not even shown its
tools. `agent-admin` refuses to give an agent such a permission, to
promote a disabled category, or to create an agent from a template that
needs one; each refusal is in the journal (`category.refused`, with who
tried).

```bash
agent-admin show | jq .enablement     # the state: diagnostics-only, or the enabled categories
agent-admin enable patch-errata       # enable one category (or several)
agent-admin enable --all              # every category: the full capability
```

`enable` is the one action out of diagnostics-only. It needs root (sudo
or Cockpit's administrative access) and is recorded with who ran it
(`categories.enabled`). An enabled category starts supervised, as on a
host installed with the full capability: promote it later on the path
below. What the diagnostics agent did until then is the first evidence
for that. There is no way back to diagnostics-only.

The state is `/etc/mcp-gateway/policy/agents/_enablement.yaml`. Change it
only with `agent-admin enable`: `agent-admin show` compares it with its
record in the journal and reports a state changed by hand
(`"tampered": "the state differs from its record n-…"`). A host without
the file (every host installed with the full capability) has every
category enabled.

`agent-admin diagnostics-only` makes a freshly installed host
diagnostics-only; the installer's setup runs it. It refuses a host that
has an enablement state already, or an agent with permissions outside
`diagnostics`.

## Is a category ready to promote? {#readiness}

While a category is supervised, every proposal of it is an approval
item, and the journal keeps how approvers decided. `agent-admin
readiness` measures that per category, over the last 30 days:

```bash
agent-admin readiness patch-pilot patch-errata | jq '.categories["patch-errata"]'
```

| Field | Meaning |
|---|---|
| `proposals` | the category's approval items that an approver decided (expired ones do not count) |
| `approved`, `declined`, `cancelled` | approved; declined; approved but then cancelled before the call ran |
| `reviewed`, `review_overrides` | autonomous actions [reviewed](#review) since a promotion; of those, judged `incident` |
| `overrides`, `override_rate` | `declined` + `cancelled` + `review_overrides`, and their share of `proposals` + `reviewed` |
| `incidents`, `incident_rate` | approved calls that failed, hit a limit, or had the agent paused during their activity; their share of the approved calls that ran |
| `tools`, `distinct_calls` | how varied the proposals were |
| `observed_days`, `first_proposal` | since the first proposal, and when it was |
| `ready`, `unmet` | whether every threshold is met, and which are not |

The thresholds are 14 days, 50 proposals, at most 5 % overrides and at
most 2 % incidents. Set your own in `/etc/custos/promotion.yaml`, for
example:

```yaml
proposals: 30
override_rate: 0.03
```

The keys are `observation_days`, `proposals`, `override_rate`,
`incident_rate` (fractions: `0.05` is 5 %) and `window_days`. The
command reads the agent's and the gateway's journal, so run it as root
or as a member of `systemd-journal`. It counts a call in a category by
the agent's current permissions.

### The diagnostics history {#diagnostics-evidence}

`agent-admin readiness` (and the Task categories card in Cockpit) also
shows what the agent's diagnosis reports showed over the same window:
how many reports, how many of their quotes Custos verified, how many you
gave feedback on, how many hypotheses you confirmed, rejected or were
unsure of, and what you said of the plans
([chapter 9](09-administration.md#reports)). On a host that started
diagnostics-only, this is the first evidence for a category you enable
later. It is shown next to the readiness, not counted in it. Only
feedback given as root (`agent-admin feedback`, or Cockpit with
administrative access) counts.

## Promoting a category {#promote}

A promoted category acts without approval, but only within its
**envelope**: the time windows in which that is allowed. Outside them
its calls need an approval again. Write the envelope to a file:

```yaml
# window.yaml: Saturdays 02:00 to 04:00, Berlin time
windows:
  - {days: [sat], from: '02:00', to: '04:00', tz: Europe/Berlin}
```

Each window has `days` (`mon` to `sun`; every day if left out), `from`
and `to` (`HH:MM`; `from` before `to`, so a window across midnight is
two windows) and `tz` (an IANA zone; `UTC` if left out). Then promote:

```bash
agent-admin promote patch-pilot patch-errata --envelope window.yaml
```

A category that is not [ready](#readiness) is refused, and the message
says what is not met. To promote it anyway, give a reason; it is
recorded with the promotion:

```bash
agent-admin promote patch-pilot patch-errata --envelope window.yaml \
    --despite-readiness --reason "patched by hand for a year, the journal only keeps a week"
```

`promote` also refuses:

- a category that is not in the catalog, or in which the agent has no
  permission;
- an agent in `advisory` mode;
- `rate` in the envelope (not enforced yet), and `hosts`
  or `blast_radius` (they need a fleet). It refuses them rather than
  ignore them;
- an envelope without `windows`, or with a key it does not know;
- `--despite-readiness` without `--reason`;
- the same envelope the category already has while it is bounded.

It returns once the gateway has the change, and writes a
`category.promoted` record with the readiness measures at that moment ([chapter 10](10-monitoring-and-audit.md#promotion-records)).
Promoting a promoted category again with another envelope replaces it.
`agent-admin show NAME` lists the agent's categories with their mode,
envelope and who promoted them.

## Demoting a category {#demote}

```bash
agent-admin demote patch-pilot patch-errata --reason "a patch broke nginx"
```

The category is supervised again, so its calls need an approval. Its
envelope stays for a later promotion. The record is `category.demoted`.
Pause and the emergency stop still stop everything, promoted or not.

## Reviewing what a promoted category did {#review}

Inside its windows a promoted category acts without asking, so nobody
decided its actions beforehand. Review them afterwards:

```bash
agent-admin review patch-pilot patch-errata | jq '.categories["patch-errata"].pending'
agent-admin review patch-pilot patch-errata e-3f2a… c1 ok
agent-admin review patch-pilot patch-errata e-41b7… c1 incident --reason "patched the database host during business hours"
```

The list holds the calls of the category that ran without an approval
since its promotion, in the last week: all of them while there are at
most 20 in the week, otherwise the newest unreviewed ones up to 20
reviews. `ok` says it was fine. `incident` says it should not have run:
it needs a reason, counts as an override in the
[readiness](#readiness) (`reviewed`, `review_overrides`), and demotes
the category at once. Each verdict is a `category.review` record with
who reviewed it. In Cockpit, the agent's *Task categories* card lists
them under *To review*, with **Fine** and **Should not have run**.
Listing the actions to review needs root or the group
`systemd-journal`; a verdict needs root (or Cockpit's administrative
access).

## Automatic demotion {#auto-demote}

The demotion evaluator, `custos-demote.timer`, checks every bounded
category every 5 minutes, as root and outside any agent. `promote`
enables the timer. It demotes a category when:

- its override rate since the promotion (at most the last 7 days) is
  above the threshold. Outside its windows the category still asks for
  approvals, and a decline counts;
- a **rollback** condition of its envelope fires;
- a [review](#review) found an action that should not have run.

```yaml
windows:
  - {days: [sat], from: '02:00', to: '04:00', tz: Europe/Berlin}
rollback:
  on: [tool_error, limit_exceeded, health_check_failed]
  health_check: 'systemctl is-active --quiet nginx.service'
```

| Condition | Fires when |
|---|---|
| `tool_error` | a call of the category that ran without an approval since the promotion failed |
| `limit_exceeded` | an activity with such a call reached one of the agent's limits |
| `health_check_failed` | `health_check` (run as root with `sh -c`, at most 60 seconds) exits non-zero |

`health_check` goes with `health_check_failed`, and only with it. The
demotion is recorded as `category.demoted` by `custos-demote`, with the
trigger as the reason. Run the evaluator by hand with `agent-admin
evaluate`, which prints each demotion. A demotion stops what follows; it
does not undo what ran (for that, a snapshot, say, is the tool's).

## How the gateway applies modes {#how-enforced}

The module `agents.rego` (`/usr/share/mcp-gateway/policy/custos/agents.rego`)
turns each permission of an agent principal into a gateway permission.
Read-only ones are kept as they are. Autonomous ones are kept as they are
in `bounded` mode, and so are the permissions of a promoted category
within one of its windows. Every other one gets `require_approval: true` with the
channel `oob` in `supervised` and `bounded` mode, and is left out in
`advisory` mode. It denies an agent everything when:

- the agent has no data, or its mode is none of the three, or a
  category's mode is neither `supervised` nor `bounded`;
- it is paused, or the emergency stop is on;
- a gateway role is bound to its user;
- it comes over HTTPS with a token from an issuer not listed for agents
  ([chapter 11](11-openshell.md#issuers)).

The gateway's audit records each decision with the principal
`agent-<name>`.
