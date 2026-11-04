---
title: Custos user guide
description: Index of the Custos user guide - installing, connecting to mcp-gateway and an identity provider, configuring, administering, supervising and troubleshooting governed AI agents on SLES 16 and Leap 16 - with the common questions mapped to the sections that answer them.
tags: [index, custos, user guide]
---

# Custos user guide

Custos runs AI agents on SUSE Linux Enterprise Server 16 and openSUSE
Leap 16 under people's supervision: mcp-gateway decides every tool call,
systemd and SELinux confine each agent, and Cockpit shows what they do.
This guide is for the people who install, configure, administer and
supervise them. Start with chapter 1, then follow chapters 2 to 4 to a
first working agent.

## Chapters {#chapters}

| Chapter | For |
|---|---|
| [1. What Custos is and how it fits together](01-overview.md) | the parts, who enforces what, the terms |
| [2. Installing Custos](02-installation.md) | requirements, packages, SELinux, checking the installation, people's accounts, upgrading, removing |
| [3. Connecting Custos to mcp-gateway](03-gateway.md) | the socket, the agents' gateway data, servers, approvers, approval links, delegation |
| [4. Creating your first agent](04-first-agent.md) | a walk-through from configuration to an approved tool call |
| [5. Configuring an agent (agent.yaml)](05-configuration.md) | every key of `agent.yaml`, models and credentials, limits, chat, schedules |
| [6. Permissions and autonomy modes](06-permissions-and-modes.md) | advisory, supervised, bounded; permission lists; pause |
| [7. Approving an agent's calls](07-approvals.md) | the flow, who may decide, Cockpit and command line, grants, notifications |
| [8. Using the Custos page in Cockpit](08-cockpit.md) | logging in, roles, the tabs, the emergency stop |
| [9. Administering agents](09-administration.md) | `agent-admin`, create, edit, delete, pause, stop, schedules, backups |
| [10. Monitoring agents and reading the audit record](10-monitoring-and-audit.md) | the control API, journal fields, the gateway's audit, retention |
| [11. Agents in OpenShell sandboxes, the gateway's HTTPS listener and the identity provider](11-openshell.md) | agents in OpenShell, the HTTPS listener, Keycloak or another IdP, tokens |
| [12. Troubleshooting](12-troubleshooting.md) | symptoms, causes and fixes; every error message |
| [13. Reference](13-reference.md) | programs, files, units, users, variables, SELinux, ports |

## Common questions {#questions}

| Question | Section |
|---|---|
| Which packages do I install? | [02-installation.md#install](02-installation.md#install) |
| How do I check that the installation works? | [02-installation.md#check-installation](02-installation.md#check-installation) |
| Which accounts and groups do people need? | [02-installation.md#people](02-installation.md#people), [08-cockpit.md#roles](08-cockpit.md#roles) |
| How does an agent reach mcp-gateway? | [03-gateway.md](03-gateway.md), [03-gateway.md#check-connection](03-gateway.md#check-connection) |
| Which tool servers can an agent use? | [03-gateway.md#servers](03-gateway.md#servers) |
| Who may approve an agent's calls? | [03-gateway.md#approvers](03-gateway.md#approvers), [07-approvals.md#who-may-decide](07-approvals.md#who-may-decide) |
| How do I let one agent ask another? | [03-gateway.md#delegation](03-gateway.md#delegation) |
| How do I create an agent? | [04-first-agent.md](04-first-agent.md), [09-administration.md#create](09-administration.md#create) |
| How do I choose and configure the model? | [05-configuration.md#models](05-configuration.md#models), [05-configuration.md#vertex](05-configuration.md#vertex) |
| Where does the model's API key go? | [05-configuration.md#model-credentials](05-configuration.md#model-credentials), [09-administration.md#credentials](09-administration.md#credentials) |
| SELinux blocks the model server's port | [05-configuration.md#selinux-and-the-models-port](05-configuration.md#selinux-and-the-models-port) |
| How do I run an agent on a schedule? | [05-configuration.md#schedules](05-configuration.md#schedules), [09-administration.md#schedules](09-administration.md#schedules) |
| What do advisory, supervised and bounded mean? | [06-permissions-and-modes.md#modes](06-permissions-and-modes.md#modes) |
| How do I let an agent do something without asking? | [06-permissions-and-modes.md#permissions](06-permissions-and-modes.md#permissions), [06-permissions-and-modes.md#examples](06-permissions-and-modes.md#examples) |
| How do I approve or deny a call? | [07-approvals.md#cockpit](07-approvals.md#cockpit), [07-approvals.md#command-line](07-approvals.md#command-line) |
| Who is recorded as having approved? | [07-approvals.md#who-is-recorded](07-approvals.md#who-is-recorded) |
| How do I let a kind of work (patching, say) run without approval in a maintenance window? | [06-permissions-and-modes.md#categories](06-permissions-and-modes.md#categories), [06-permissions-and-modes.md#promote](06-permissions-and-modes.md#promote) |
| How do I take that back? | [06-permissions-and-modes.md#demote](06-permissions-and-modes.md#demote) |
| How do I pause one agent? | [09-administration.md#pause](09-administration.md#pause) |
| How do I stop all agents at once? | [09-administration.md#emergency-stop](09-administration.md#emergency-stop), [08-cockpit.md#emergency-stop](08-cockpit.md#emergency-stop) |
| How do I change or delete an agent? | [09-administration.md#edit](09-administration.md#edit), [09-administration.md#delete](09-administration.md#delete) |
| What did an agent do, and who allowed it? | [10-monitoring-and-audit.md#history](10-monitoring-and-audit.md#history), [08-cockpit.md#audit](08-cockpit.md#audit) |
| Which journal fields do agents write? | [10-monitoring-and-audit.md#journal](10-monitoring-and-audit.md#journal) |
| How do I back up the agents? | [09-administration.md#backup](09-administration.md#backup) |
| How do I run an agent in an OpenShell sandbox? | [11-openshell.md](11-openshell.md) |
| How do I connect the gateway to Keycloak or another IdP? | [11-openshell.md#idp](11-openshell.md#idp), [11-openshell.md#keycloak](11-openshell.md#keycloak), [11-openshell.md#other-idp](11-openshell.md#other-idp) |
| Which token issuers does the gateway trust for agents? | [11-openshell.md#issuers](11-openshell.md#issuers) |
| How is the sandbox's gateway credential set? | [11-openshell.md#credential](11-openshell.md#credential) |
| Where is a file, unit or SELinux type? | [13-reference.md](13-reference.md) |

## Where errors are explained {#errors}

[Chapter 12](12-troubleshooting.md) is ordered by symptom: installation,
configuration errors, an agent that does not start, the gateway, denied
tools, the model, approvals, schedules, Cockpit, OpenShell, a policy that
is not loaded, SELinux. Its last section,
[the error messages](12-troubleshooting.md#error-messages), lists each
message of `agent-admin`, the agents and the page verbatim, with its
cause and the section with the fix. Search for the message's text.

## What this guide does not contain {#not-here}

- **This machine's agents and their settings.** Ask the machine:
  `agent-admin list`, `agent-admin show [NAME]` (modes, permissions,
  pause, the emergency stop) and `agent-admin check NAME`.
- **mcp-gateway itself**: its configuration, policy language, servers
  and approvals beyond what Custos uses. They are in mcp-gateway's own
  documentation and `mcp-gateway-admin`.
- **The exact interfaces**: the control API, journal fields, gateway data
  and AG-UI events are defined in the contract,
  `CONTRACT.md`, installed beside this guide
  (`/usr/share/custos/docs/CONTRACT.md`; at the top of the source tree).
