---
title: "4. Creating your first agent"
description: A walk-through from the example agent patch-pilot to a first approved change, with the commands and what to expect at each step.
tags: [quickstart, tutorial, agent-admin, create]
---

# 4. Creating your first agent

This chapter creates the example agent `patch-pilot`, which keeps the
host patched, and runs it until its first change waits for, and gets, an
approval. It assumes the installation of [chapter 2](02-installation.md)
and the gateway servers `zypp` and `systemd` of
[chapter 3](03-gateway.md#servers).

## Step 1: a model the agent can reach {#step-1}

The example uses an OpenAI-compatible server on the host,
`http://127.0.0.1:8000/v1`, with the model `qwen3-32b`. Check that it
answers:

```bash
curl -s http://127.0.0.1:8000/v1/models | jq -r '.data[].id'
```

Use any other OpenAI-compatible server, Gemini or Vertex AI instead
([chapter 5](05-configuration.md#models)). If the server listens on a
port SELinux does not count as an HTTP port (8000 is one of those on
many policies), allow it first
([chapter 5](05-configuration.md#selinux-and-the-models-port)).

## Step 2: the configuration {#step-2}

Copy the example and adjust the model:

```bash
cp /usr/share/custos/examples/patch-pilot/agent.yaml /root/patch-pilot.yaml
vi /root/patch-pilot.yaml          # model.endpoint, model.name
agent-admin check /root/patch-pilot.yaml
```

`agent-admin check` prints nothing and exits 0 when the configuration
is valid. Otherwise it names each problem, for example
`model.endpoint: "localhost:8000": want an http(s) URL`. Every key is
explained in [chapter 5](05-configuration.md).

## Step 3: create the agent {#step-3}

```bash
agent-admin create patch-pilot --from /root/patch-pilot.yaml
```

This creates the system user `agent-patch-pilot` and installs the
configuration as `/etc/custos/agents/patch-pilot/agent.yaml`. It also
writes the agent's gateway data: supervised mode, **no permissions**. It
then enables the socket `agent@patch-pilot.socket` and the timer of the
example's schedule `compliance`. The agent starts when someone first
talks to it, and stops after 10 minutes without work.

A new agent can do nothing yet: the gateway denies it every tool.

## Step 4: what the agent may do {#step-4}

List the permissions. The example's file allows reading package and unit
state, and installing patches, which needs an approval:

```bash
cat /usr/share/custos/examples/patch-pilot/permissions.yaml
agent-admin permissions patch-pilot < /usr/share/custos/examples/patch-pilot/permissions.yaml
agent-admin show patch-pilot | jq .agents
```

The mode stays `supervised`: read-only tools run, everything else waits
for a person ([chapter 6](06-permissions-and-modes.md)).

## Step 5: ask it something {#step-5}

In Cockpit, open *Custos*, tab *Agents*, select `patch-pilot`, and type
in its chat: *List the pending security patches.* Or from a shell, as a
member of `agent-clients`:

```bash
curl -s --unix-socket /run/agents/patch-pilot/api.sock http://agent/v1/activities \
  -X POST -d '{"prompt": "List the pending security patches.", "wait": true}' | jq '{state, summary}'
```

The agent calls `zypp__list_updates` (read-only, allowed) and answers.
The activity appears in the *Activity* tab.

## Step 6: a change, and its approval {#step-6}

Ask for a change: *Install the pending security patches.* The agent
calls `zypp__install_patches`. The gateway holds the call, and the
activity shows **waiting for approval**. In the *Approvals* tab, a
person who may approve ([chapter 7](07-approvals.md)) sees:

- the call from the gateway: the agent's principal, the policy that
  asked, and the exact arguments;
- the agent's account: its stated reason, who started the activity, and
  its earlier calls.

*Approve once* lets this call run; *Decline* refuses it. The activity
goes on and finishes. The *Audit* tab shows the history: proposed,
approved by whom, executed.

## Step 7: the schedule {#step-7}

The example's schedule `compliance` runs on weekdays at 06:00
(`on_calendar: "Mon..Fri 06:00"`) and only reports. See when it runs
next, or start it now:

```bash
systemctl list-timers 'agent-schedule@patch-pilot_*'
systemctl start agent-schedule@patch-pilot_compliance.service
```

Its activities have the origin `schedule · compliance`.

## Where to go next {#where-to-go-next}

- Change what the agent may do: [chapter 6](06-permissions-and-modes.md).
- Day-to-day administration (pause, stop, delete, credentials):
  [chapter 9](09-administration.md).
- Something did not work: [chapter 12](12-troubleshooting.md).
