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
- {server: zypp, tool: 'list_*', read_only: true}
- {server: zypp, tool: install_patches}
- {server: systemd, tool: restart_unit, autonomous: true, args: {unit: '^nginx\.service$'}}
```

| Key | Meaning |
|---|---|
| `server` | the gateway server's name; a glob (`*`) matches several |
| `tool` | the tool's name on that server; a glob, such as `'list_*'` |
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
| report on updates | `{server: zypp, tool: 'list_*', read_only: true}` |
| install patches, each with approval | the above, and `{server: zypp, tool: install_patches}` |
| restart one service on its own (bounded mode) | `{server: systemd, tool: restart_unit, autonomous: true, args: {unit: '^nginx\.service$'}}` |
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
The catalog ships three categories:

| Category | For |
|---|---|
| `patch-errata` | installing patches and errata, refreshing repositories |
| `security-response` | responding to a security finding: isolating a service, rotating a credential, blocking an address |
| `config-lifecycle` | changing configuration files; starting, stopping or restarting services |

`agent-admin categories` lists the catalog. To add a category, put a
file in `/etc/custos/categories.d/`, for example `backups.yaml` with
`backups: {title: Backups}`. A file with a shipped name
(`patch-errata.yaml`) replaces that one, and an empty one disables it.

A permission joins a category with `category:`:

```yaml
- {server: zypp, tool: 'list_*', read_only: true}
- {server: zypp, tool: patch, category: patch-errata}
- {server: systemd, tool: restart_unit, category: config-lifecycle, args: {unit: '^nginx\.service$'}}
```

A permission in a category is supervised until its category is
promoted, whatever the agent's mode. `advisory` still caps everything.
A permission without a category keeps the agent's mode and its
`autonomous` flag, and `autonomous` cannot be combined with `category`.

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
agent-admin promote patch-pilot patch-errata --envelope window.yaml \
    --despite-readiness --reason "patched by hand for a month without overrides"
```

`--despite-readiness` and a reason are required for now: Custos does
not measure readiness (how often approvers overrode the category's
proposals) yet, so the reason is the record of why you promoted.
`promote` refuses:

- a category that is not in the catalog, or in which the agent has no
  permission;
- an agent in `advisory` mode;
- `rate` and `rollback` in the envelope (not enforced yet), and `hosts`
  or `blast_radius` (they need a fleet). It refuses them rather than
  ignore them.

It returns once the gateway has the change, and writes a
`category.promoted` record ([chapter 10](10-monitoring-and-audit.md#promotion-records)).
Promoting a promoted category again replaces its envelope.
`agent-admin show NAME` lists the agent's categories with their mode,
envelope and who promoted them.

## Demoting a category {#demote}

```bash
agent-admin demote patch-pilot patch-errata --reason "a patch broke nginx"
```

The category is supervised again, so its calls need an approval. Its
envelope stays for a later promotion. The record is `category.demoted`.
Pause and the emergency stop still stop everything, promoted or not.

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
