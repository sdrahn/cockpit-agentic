---
title: "13. Reference"
description: Programs, files and directories, systemd units, users and groups, environment variables, SELinux types and booleans, ports, and where the interfaces are defined.
tags: [reference, files, units, paths, selinux, environment]
---

# 13. Reference

Lists for looking things up. The interfaces (control API, journal
fields, gateway data, AG-UI events) are defined in the contract,
`CONTRACT.md`, installed beside this guide.

## Programs {#programs}

| Program | Runs as | Does |
|---|---|---|
| `/usr/sbin/agent-admin` | root (or anyone for `check FILE`, `show`) | administration ([chapter 9](09-administration.md#agent-admin)) |
| `/usr/libexec/custos/agent-launcher` | `agent-<name>` (`agent_t`) | the agent: `agent-launcher serve` |
| `/usr/libexec/custos/agent-delegate` | the delegating agent's user (`mcpsrv_agents_t`), started by mcp-gateway | the `agents` server: delegation |
| `/usr/libexec/custos/agent-openshell-relay` | root (`agent_relay_t`) | an OpenShell agent's socket and journal |
| `/usr/libexec/custos/agent-openshell-logs` | the relay | follows the sandbox container's output |

## Files and directories {#files}

| Path | Owner, mode | Contents |
|---|---|---|
| `/etc/custos/agents/<name>/agent.yaml` | root:agent-`<name>` 0640 | the configuration ([chapter 5](05-configuration.md)) |
| `/etc/custos/agents/<name>/openshell.env`, `openshell-policy.yaml`, `openshell-profile.yaml`, `relay.secret` | root | OpenShell agents: relay settings, sandbox policy, provider profile, the relay's secret (0600) |
| `/etc/mcp-gateway/policy/agents/<name>.yaml` | root 0644 | the agent's mode, pause and permissions ([chapter 6](06-permissions-and-modes.md)) |
| `/etc/mcp-gateway/policy/agents/_control.yaml` | root 0644 | the emergency stop |
| `/etc/mcp-gateway/policy/agents/_http.yaml` | root 0644 | token issuers trusted for agents ([chapter 11](11-openshell.md#issuers)) |
| `/etc/credstore.encrypted/agent-<name>.<cred>` | root 0600 | credentials (`systemd-creds`) |
| `/etc/sysusers.d/agent-<name>.conf` | root 0644 | the agent's user |
| `/etc/systemd/system/agent-schedule@<name>_<schedule>.timer.d/schedule.conf` | root | a schedule's calendar |
| `/etc/systemd/system/agent@<name>.service.d/` | root | drop-ins for the agent's unit |
| `/var/lib/agents/<name>/sessions.db` | agent-`<name>` 0600 | chat sessions |
| `/run/agents/<name>/api.sock` | root:agent-clients 0660 | the agent's control API |
| `/usr/share/mcp-gateway/policy/custos/agents.rego` | root 0644 | the autonomy policy module |
| `/usr/share/custos/gateway/servers.d/agents.yaml` | root 0644 | the `agents` server, to copy to `/etc/mcp-gateway/servers.d/` |
| `/usr/share/custos/categories.d/*.yaml` | root 0644 | the task categories ([chapter 6](06-permissions-and-modes.md#categories)) |
| `/etc/custos/categories.d/*.yaml` | root 0644 | your categories: a shipped name replaces, an empty file disables |
| `/usr/share/custos/examples/patch-pilot/` | root 0644 | the example agent and permissions |
| `/usr/share/custos/openshell/Containerfile` | root 0644 | the OpenShell agent image |
| `/usr/share/cockpit/custos/` | root 0644 | the Cockpit page |
| `/usr/share/custos/docs/` | root 0644 | this guide and the contract (`custos-docs`) |
| `/usr/share/mcp-docs/collections.d/custos.yaml` | root 0644 | the collection file for mcp-docs (`custos-docs`) |

## systemd units {#units}

| Unit | Does |
|---|---|
| `agent@<name>.socket` | listens on the agent's socket; starts the service on a request |
| `agent@<name>.service` | runs the agent; exits after 10 minutes idle |
| `agent-schedule@<name>_<schedule>.timer` | starts the schedule's service at its calendar |
| `agent-schedule@<name>_<schedule>.service` | runs `agent-admin trigger <name>_<schedule>` |
| `agent-openshell@<name>.socket`, `.service` | an OpenShell agent's socket and relay |

## Users and groups {#users}

| Name | What |
|---|---|
| `agent-<name>` | an agent's system user; no password, no login shell |
| `agent-clients` | people who supervise agents; agents are members too, for delegation |
| `mcp-users` | the gateway's socket group; agents are members |
| `wheel` | in the gateway's default role data, the role `admin` (may approve); in Cockpit, sudo for administrative access |

## Environment variables {#environment}

| Variable | Read by | Meaning |
|---|---|---|
| `CUSTOS_OPENSHELL_IMAGE` | `agent-admin` | the image for new sandboxes (default `localhost/custos-agent:latest`) |
| `CUSTOS_OPENSHELL_USER` | `agent-admin` | the user whose rootless Podman holds the sandboxes |
| `SUDO_USER`, `USER` | `agent-admin` | who acted, for `paused_by` and the emergency stop's `by` |
| `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`, `GOOGLE_APPLICATION_CREDENTIALS` | the agent (a unit drop-in) | Vertex AI ([chapter 5](05-configuration.md#vertex)) |

## SELinux {#selinux}

| Type or boolean | What |
|---|---|
| `agent_t` (`agent_exec_t`) | the agents; reach the gateway's MCP socket and HTTP ports |
| `mcpsrv_agents_t` | the delegation bridge; reaches the agents' sockets |
| `agent_relay_t` | the OpenShell relay |
| `agent_conf_t`, `agent_var_lib_t`, `agent_runtime_t`, `agent_sock_t` | `/etc/custos`, `/var/lib/agents`, `/run/agents`, the sockets |
| `agent_connect_any` (off) | lets `agent_t` connect to any port |

## Ports {#ports}

| Port | Who | Note |
|---|---|---|
| 9090 | Cockpit | the web console |
| the model server's | the agents | must be an HTTP port for SELinux, or `agent_connect_any` |
| 8443 (your choice) | mcp-gateway's HTTPS listener | only for OpenShell agents; label `mcp_port_t` |
| 47000–47999 | OpenShell agents, inside the sandbox | the agent's control API behind the relay's forward |

## Where the interfaces are defined {#contract}

| Interface | `CONTRACT.md` |
|---|---|
| identity, people and the control plane | §2 |
| discovery, sandbox backends | §3, §3a |
| activities, the control API | §4, §5 |
| autonomy modes, the agents' gateway data | §6 |
| approvals, intervention, delegation | §7, §8, §9 |
| journal fields and `MESSAGE_ID`s | §10 |
| AG-UI (chat) | §11 |
| files, configuration, units, launcher, OpenShell, `agent-admin`, SELinux, packages | §13 to §19 |
