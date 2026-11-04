# Changelog

All notable changes to Custos. Versions follow
[Semantic Versioning](https://semver.org/). Within contract version 1
(CONTRACT.md), names, paths and fields are added, never renamed or
removed.

## v0.1.1 — 2026-11-04

Fixes from testing 0.1.0. The package builds on OBS. The Cockpit page
shows Start for a stopped agent, stops OpenShell agents for good, and
gains Restart and Logs. OpenShell agents get through a token renewal,
and their unit no longer fails to start when OpenShell misses that the
sandbox is ready.

Upgrading from 0.1.0 needs nothing: no configuration, gateway data or
contract changes. Running agents keep the old launcher until they exit
on idle or are restarted (`systemctl restart agent@NAME.service`, or
the page's Restart); OpenShell agents pick up the relay unit's change
at their next start.

### Added

- The Cockpit page: **Restart** and **Logs** for an agent. Restart
  restarts its service (an OpenShell agent's relay and sandbox); Logs
  opens Cockpit's journal for the agent's unit, with the unit's messages
  and the agent's records.

### Fixed

- The spec's `%check` failed in the package's tarball, which has no
  `.github`: a test of the release workflow read
  `.github/workflows/release.yml`. It skips without it. CI now builds
  the RPM from the same tarball as OBS.
- The Cockpit page showed no **Start** for a stopped agent: it took an
  agent for stopped only when its directory in `/run/agents` was gone,
  but systemd removes only the socket and keeps the directory. An agent
  without a socket is now stopped, and **Start** starts it again; for an
  OpenShell agent its relay's socket, which the page finds from systemd.
- **Stop** in the Cockpit page did not stop an OpenShell agent: it
  stopped the relay's service and left its socket listening, so the next
  request started the agent again. It stops both now.
- An OpenShell agent's unit could fail to start after a restart (a
  no-op `agent-admin edit`, say): `openshell sandbox start` missed that
  the sandbox was Ready and waited past the unit's start timeout.
  `agent-openshell-start` now bounds each start to 15 seconds, decides by
  the sandbox's phase, and counts its 120-second limit on the clock.
- An OpenShell agent's activity could fail right after OpenShell renewed
  its gateway token: the sandbox refused the connection for a moment
  (`connect: permission denied`), or the proxy closed it without an
  answer (`EOF`), and the agent did not try again. A refused connection
  is now retried for every request (nothing was sent), with the same
  backoff as the proxy's 500s; a dropped one only for a request that is
  safe to repeat (listing tools, initialize, ping), never a `tools/call`,
  which may have reached the gateway.

## v0.1.0 — 2026-11-04

The first release: governed AI agents on SUSE Linux Enterprise Server 16
and openSUSE Leap 16, supervised from Cockpit, with every tool call
decided by mcp-gateway (≥ 1.12).

There is nothing to upgrade from. Install `custos` and `custos-selinux`,
and optionally `custos-cockpit` and `custos-docs`, from the OBS
packages. The user guide, chapters 2 to 4, goes from the installation to
a first agent whose change waits for an approval. The OpenShell backend
is a draft (CONTRACT.md O10).

### Added

- **Agents.** Each agent is a Google ADK Go agent, run by systemd as its
  own system user (`agent@<name>.socket`, `agent@<name>.service`) and
  started on demand by its socket. It is configured in
  `/etc/custos/agents/<name>/agent.yaml`. Models are any
  OpenAI-compatible server (on-premises, air-gapped), Gemini, or
  Vertex AI. Credentials are kept with `systemd-creds`. Each agent has
  limits on model calls, tool calls and run time, and its schedules run
  as systemd timers.
- **Governance in mcp-gateway.** The policy module
  `/usr/share/mcp-gateway/policy/custos/agents.rego` enforces three
  autonomy modes: `advisory`, `supervised` (the default; a new agent can
  do nothing until it is given permissions) and `bounded`. Approvals are
  decided as the person, through the gateway. Pause, and the emergency
  stop (`agent-admin stop-all`, `release-all`), are enforced by the
  gateway on every backend.
- **Control API and audit.** Each agent serves its status and activities
  on `/run/agents/<name>/api.sock`, identifying callers by their peer
  credentials. It writes journal records with `AGENT_*` fields and their
  own `MESSAGE_ID`s, joined with the gateway's audit from proposal to
  execution. Chat runs over AG-UI.
- **Delegation** between agents through the gateway's `agents` server
  (`agent-delegate`), with the chain traced to its origin.
- **`agent-admin`**: `create`, `edit`, `check`, `delete`, `mode`,
  `permissions`, `set-credential`, `pause`, `resume`, `stop-all`,
  `release-all`, `trigger`, `list`, `show`. Changes are in force when
  the command returns.
- **OpenShell sandboxes** (draft): agents in NVIDIA OpenShell, reaching
  the gateway's HTTPS listener with OAuth tokens that OpenShell renews.
  The control API is relayed with the caller, and the agent's records
  reach the host journal. The relay unit waits until the sandbox is
  Ready.
- **Cockpit page** (`custos-cockpit`): the Activity, Approvals, Agents,
  Policy and Audit tabs, chat, creating, editing and deleting agents,
  and the emergency stop. It is tested in a real Cockpit with SELinux
  enforcing.
- **SELinux** (`custos-selinux`): `agent_t` for the agents,
  `mcpsrv_agents_t` for delegation, and `agent_relay_t` for the
  OpenShell relay.
- **Functional tests** (`custos-tests`) for openQA:
  `/usr/libexec/custos/tests/run-tests.sh --disposable --results DIR`,
  writing TAP and JUnit.
- **User guide** (`docs/user-guide`, package `custos-docs`): installing,
  connecting to mcp-gateway and an identity provider (Keycloak and
  others), configuration, permissions and modes, approvals, the Cockpit
  page, administration, monitoring and audit, OpenShell, troubleshooting
  with every error message, and a reference. `custos-docs` installs it
  with the contract in `/usr/share/custos/docs`, together with a
  collection file from which mcp-docs serves it to agents on the host.
- **The contract** (`CONTRACT.md`), version 1: the interface between
  the agent hosts and the control plane.
