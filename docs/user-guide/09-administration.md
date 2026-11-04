---
title: "9. Administering agents"
description: Day-to-day administration with agent-admin and systemd - creating, editing and deleting agents, credentials, pause and resume, stopping and starting, schedules, cancelling activities, the emergency stop, the units' sandbox, and backups.
tags: [administration, agent-admin, pause, stop, schedules, emergency stop, backup, credentials]
---

# 9. Administering agents

Administration is done as root, with `agent-admin` and `systemctl`, or
in Cockpit with administrative access ([chapter 8](08-cockpit.md#agents)),
which runs the same commands. `agent-admin` records who acted: the
person behind `sudo` (`SUDO_USER`), else `USER`.

## agent-admin at a glance {#agent-admin}

| Command | Does |
|---|---|
| `agent-admin list` | the agents (those with a configuration) |
| `agent-admin show [NAME…]` | their gateway data and the emergency stop, as JSON (no root needed) |
| `agent-admin check NAME\|FILE` | validate a configuration (a FILE needs no root; `-` is stdin) |
| `agent-admin create NAME --from FILE` | create an agent: supervised, no permissions |
| `agent-admin edit NAME --from FILE` | replace its configuration and apply it |
| `agent-admin delete NAME [--keep-state]` | remove an agent |
| `agent-admin mode NAME MODE` | `advisory`, `supervised` or `bounded` |
| `agent-admin permissions NAME < list.yaml` | replace its permissions |
| `agent-admin categories` | the task categories, as JSON (no root needed) |
| `agent-admin promote NAME CATEGORY --envelope FILE --despite-readiness --reason TEXT` | let a category act without approval within its windows ([chapter 6](06-permissions-and-modes.md#promote)) |
| `agent-admin demote NAME CATEGORY [--reason TEXT]` | back to supervised |
| `agent-admin set-credential NAME CRED < secret` | store a secret for it |
| `agent-admin pause NAME`, `resume NAME` | hold it, let it go on |
| `agent-admin stop-all`, `release-all` | the emergency stop and its end |
| `agent-admin trigger NAME_SCHEDULE` | start a schedule's run (what its timer runs) |

`mode`, `permissions`, `promote`, `demote`, `pause`, `resume`, `stop-all` and `release-all`
return once the gateway's policy engine has the change, so it is in
force when the command ends. If the engine does not load it within 20
seconds, the command says so and fails
([chapter 12](12-troubleshooting.md#policy-not-loaded)). Its standard
output is its own messages; what the programs it runs print goes to
standard error.

## Creating an agent {#create}

```bash
agent-admin create NAME --from agent.yaml
agent-admin create NAME --from - < agent.yaml
```

`create` validates the configuration and creates the system user
`agent-<name>` (`/etc/sysusers.d/agent-<name>.conf`, `systemd-sysusers`).
It installs `/etc/custos/agents/<name>/agent.yaml` and writes the gateway
data (supervised, no permissions) and the schedules' timers. Then it
enables `agent@<name>.socket` and the timers. Next, give the agent
permissions ([chapter 6](06-permissions-and-modes.md)) and, if its model
needs one, a credential. In Cockpit, *Agents*, **Create agent**: a name,
the configuration from a template, **Check**, **Create**.

## Editing an agent {#edit}

`agent-admin edit NAME --from FILE` validates the new configuration,
replaces it and syncs the schedules. It then restarts the agent if it is
running, which ends its activities in flight. For OpenShell agents,
`edit` may recreate the sandbox ([chapter 11](11-openshell.md#edit)). The
name and the sandbox kind cannot change. In Cockpit: the agent's
*Configuration*, **Edit configuration**, **Save and apply**.

## Deleting an agent {#delete}

```bash
agent-admin delete NAME              # also its state (/var/lib/agents/NAME)
agent-admin delete NAME --keep-state
```

`delete` disables and stops the agent's socket, service and timers. It
removes its configuration, gateway data, credentials, drop-ins and
timers, and, unless `--keep-state`, its sessions. The user
`agent-<name>` stays: uids are not reused while files may still belong
to it. In Cockpit, **Delete…** asks you to type the agent's name first.

## Credentials {#credentials}

```bash
agent-admin set-credential NAME model-key < key.txt
```

The secret is encrypted with `systemd-creds` (bound to this host) into
`/etc/credstore.encrypted/agent-<name>.<cred>`, root-only. The agent's
unit imports `agent-<name>.*` at start; restart a running agent to use a
new value (`systemctl restart agent@NAME.service`). For OpenShell agents,
`gateway-client-secret` and `gateway-token` create the sandbox's gateway
credential ([chapter 11](11-openshell.md#credential)).

## Pausing and resuming an agent {#pause}

```bash
agent-admin pause NAME
agent-admin resume NAME
```

Pausing does two things. The gateway denies the agent every call
(`paused: true`, `paused_by: <person> <time>` in its data), whatever the
agent does. And the agent holds its activities before their next model or
tool call and refuses new ones (`409`). Resuming lets them go on. The
journal has `agent.paused` and `agent.resumed`.

## Stopping and starting an agent {#stop}

Stop an agent by its units; the next request no longer starts it:

```bash
systemctl stop agent@NAME.socket agent@NAME.service
systemctl start agent@NAME.socket
```

For an OpenShell agent: `systemctl stop agent-openshell@NAME.service`,
which also stops its sandbox. A stopped agent's gateway data stays: the
Agents tab shows it as *stopped*. To keep it stopped across reboots,
`systemctl disable agent@NAME.socket`.

## Schedules {#schedules}

```bash
systemctl list-timers 'agent-schedule@*'
systemctl disable --now agent-schedule@NAME_SCHEDULE.timer     # pause one schedule
systemctl enable --now agent-schedule@NAME_SCHEDULE.timer
systemctl start agent-schedule@NAME_SCHEDULE.service           # run it now
```

To change a schedule's calendar or prompt, edit the configuration
([chapter 5](05-configuration.md#schedules)). `agent-admin edit` rewrites
the timers. A disabled timer stays disabled until you enable it, except
after `release-all`, which starts every schedule's timer again.

## Cancelling an activity {#cancel}

```bash
curl -s --unix-socket /run/agents/NAME/api.sock http://agent/v1/activities/ID/cancel -X POST
```

`204` when it was running, `409` when it was not. Root, the activity's
user and whoever started it may cancel. In Cockpit, **Cancel activity**.
The journal has `activity.cancel`.

## The emergency stop {#emergency-stop}

```bash
agent-admin stop-all
agent-admin release-all
```

`stop-all` writes `_control: {stopped: true, by, at}` to
`/etc/mcp-gateway/policy/agents/_control.yaml`: once the gateway has
it, it denies every agent everything, on every backend. Then it stops
every agent's socket and service, every OpenShell sandbox and every
schedule timer. An agent started by hand afterwards is still denied by
the gateway. `release-all` sets `stopped: false`, recording who released
it, and starts every agent's socket and schedule timers again. Paused
agents stay paused. In Cockpit: **Stop all agents** in the header, and
**Release…** in the banner.

## The agents' systemd sandbox {#sandbox}

`agent@.service` runs each agent as its own user with
`ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp`,
`PrivateDevices`, `NoNewPrivileges`, no capabilities, a system call
filter and `MemoryDenyWriteExecute`, without the gateway's control socket
and without `/run/agents`. Adjust it for one agent with a drop-in, for
example to set environment variables:

```bash
systemctl edit agent@NAME.service           # writes /etc/systemd/system/agent@NAME.service.d/override.conf
```

Do not open the control socket or `/run/agents` to an agent: that would
let it reach the gateway's approvals or other agents. The gateway's
policy still decides each call, but the sandbox is part of the design.
`systemd-analyze security agent@NAME.service` shows the exposure.

## Backing up and moving agents {#backup}

To restore a host's agents, keep:

| Path | What |
|---|---|
| `/etc/custos/agents/` | configurations |
| `/etc/mcp-gateway/policy/agents/` | modes, permissions, pause, the emergency stop, `_http.yaml` |
| `/etc/sysusers.d/agent-*.conf` | the agents' users |
| `/etc/systemd/system/agent-schedule@*.timer.d/`, `/etc/systemd/system/agent@*.service.d/` | schedules' calendars, drop-ins |
| `/var/lib/agents/` | chat sessions (optional) |

The credentials (`/etc/credstore.encrypted/agent-*`) are encrypted for
this host and cannot be read on another. On a new host, run
`agent-admin create` with each configuration, restore the permissions
with `agent-admin permissions`, and set the credentials again with
`agent-admin set-credential`.
