---
title: "8. Using the Custos page in Cockpit"
description: "Who logs in, the roles (supervisor, approver, administrator), what the person's and the root session are used for, and each tab of the page: Activity, Approvals, Agents, Policy, Audit, chat and the emergency stop."
tags: [cockpit, web, ui, roles, activity, approvals, agents, policy, audit, chat]
---

# 8. Using the Custos page in Cockpit

The Custos page (package `custos-cockpit`) is where people watch the
agents, approve their calls and intervene. Open Cockpit at
`https://<host>:9090`, log in, and choose *Custos* in the menu.

## Who logs in {#who-logs-in}

Everyone logs in with **their own account**: a local user, or a
directory account (SSSD, IPA, Active Directory) that resolves to one.
Everything the page does goes through Cockpit's session for that person,
so the agents' sockets and mcp-gateway see who it is.

Not for logging in:

- `mcp-gateway`: the gateway's system account.
- `agent-<name>`: the agents' accounts, without passwords.
- `root`: it works for administration, but an approval made as root is
  recorded as root's, and who decided is lost.

## Roles {#roles}

| Role | The account needs | Can |
|---|---|---|
| supervisor | membership in `agent-clients` | see the agents, their activities and journal records; chat with an agent |
| approver | the above, and a place in the gateway's approver rules ([chapter 3](03-gateway.md#approvers)) | decide pending calls; revoke grants |
| administrator | the above, and sudo rights (by default `wheel`) for Cockpit's *Administrative access* | create, change and delete agents; modes and permissions; pause, stop, schedules; the emergency stop |

An approver need not be an administrator. Reading the journal (the
*Audit* tab, *Decided today*) needs journal access, as for Cockpit's
*Logs* page (`systemd-journal` or `wheel`).

## Two sessions in one page {#two-sessions}

With *Administrative access* off, the page uses one session: the
person's. With it on, Cockpit adds a root session, and the page uses
each for one kind of action:

| Action | Session | Why |
|---|---|---|
| approve, decline, revoke | **the person's**, always | mcp-gateway records the approver it sees |
| status, activities, the journal, chat | the person's | the sockets and the journal answer as the person may see |
| pause, resume, stop, start, mode, permissions, schedules, create, edit, delete, the emergency stop | **root** | only root changes agents and units; `agent-admin` records the person |

So turning on administrative access never changes for whom an approval
is recorded. Without it, an administrative button says it needs
administrative access.

## Activity tab {#activity}

The cards count what runs now (and how much of it was not started in a
chat), what waits for an approval (and since when), the next scheduled
run and what ended in the last 24 hours.

*Activities* lists running activities first, then ended ones, newest
first. A delegated activity sits under the one that delegated it. Each
row shows the agent, the state, the origin (`schedule · morning-check`,
`chat · alice`, `api · root`, `delegated by triage`), the agent's last
text, and the number of tool calls. The filters show all, only running
and waiting, only those not from a chat, or only chats.

Selecting an activity shows who started it, as a chain back to its
origin, and its tool calls with their states and the agent's stated
rationale. A call waiting for approval links to the Approvals tab.
**Cancel activity** ends a running activity. The page cancels as the
person first, which works for their own activities, and else through
administrative access.

## Approvals tab {#approvals}

The pending calls, each with the gateway's half and the agent's half,
and the buttons for the scopes the gateway offers
([chapter 7](07-approvals.md#cockpit)). *Decided today* lists the day's
decisions from the journal.

## Agents tab {#agents}

*Agents on this host* lists every agent: those with a socket in
`/run/agents`, and those that have gateway data but are stopped. Each
shows its state, autonomy mode, host and model, schedules and current
work. **Create agent** opens a form for a new agent's configuration
([chapter 9](09-administration.md#create)), blank or **Start from** one
of the shipped templates, which also gives the agent the template's
permissions and mode ([chapter 9](09-administration.md#templates)). **Model defaults** sets the model new
agents start with ([chapter 5](05-configuration.md#model-defaults)).

Selecting an agent shows:

- its state and mode, its units, with **Pause**/**Resume**,
  **Restart**, **Stop**/**Start**, **Logs** and **Delete…**. **Restart**
  restarts the agent's service, which ends its running activities.
  **Logs** opens Cockpit's *Logs* page for the agent's unit: the unit's
  messages and the agent's own records ([chapter 10](10-monitoring-and-audit.md#journal));
- **Autonomy mode**: advisory, supervised or bounded, one click each;
- **Tool boundaries**: its permissions and their effect now, and **Edit
  the permissions** as a YAML list;
- **Task categories**: each category its permissions name, with its
  mode and windows, its readiness (ready or not, and what is not met;
  the override and incident rates against their limits; the proposals),
  **Demote** for a bounded one, the actions a promoted category took
  without an approval to review (**Fine**, **Should not have run**), and
  **Promote…**, which opens a form with
  the envelope. A category that is not ready is refused unless you
  promote it despite its readiness, with a reason. Readiness and the
  actions need administrative access
  ([chapter 6](06-permissions-and-modes.md#readiness));
- **Schedules**: each with its calendar, next run and **Enable**/**Disable**;
- **Configuration**: **Edit configuration** loads `agent.yaml`, **Check**
  validates it, **Save and apply** runs `agent-admin edit`;
- **Chat**: your own thread with the agent over AG-UI.

## Chat {#chat}

The chat in an agent's view is the person's own thread: the agent knows
the person by their account, and each message is an activity with the
origin `chat · <person>`. The agent's answer streams in, with its tool
calls. A call waiting for approval shows as a notice with **Review in
Approvals**. Approvals still go through the gateway: chat gives no
extra rights. The thread lasts as long as the page is open; the agent
keeps the conversation's history. An agent shows chat only with
`interaction.agui: true` ([chapter 5](05-configuration.md#interaction)).

## Policy tab {#policy}

**Posture** lists every agent's mode and how many of its permissions
run, need an approval, or run without one, with the approvers of its
servers (the rule `self` left out, as it never applies to agents).
**What would the gateway decide?** answers for one agent and one
`server__tool` ([chapter 6](06-permissions-and-modes.md#posture)).

## Audit tab {#audit}

**State-changing actions** joins the agents' records and the gateway's
by approval id: each action proposed (with the agent's rationale),
decided (approved or declined, by whom, scope, via), and executed or
not. **Records** lists the agents' journal records and the gateway's
audit records about agents, filtered by agent, kind and text. The tab
reads the journal when opened or refreshed, as far as the person may
read it. Without access to the gateway's records it says so, and the
approvers are missing.

## The emergency stop {#emergency-stop}

**Stop all agents** in the page's header asks first, then stops every
agent: the gateway denies every agent call at once, and systemd stops
every agent and schedule. A banner says who stopped them and when.
**Release…** says what it does, then lets the gateway allow agent calls
again and starts the sockets and schedules again; paused agents stay
paused. Both need administrative access
([chapter 9](09-administration.md#emergency-stop)).
