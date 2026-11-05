# Changelog

All notable changes to Custos. Versions follow
[Semantic Versioning](https://semver.org/). Within contract version 1
(CONTRACT.md), names, paths and fields are added, never renamed or
removed.

## Unreleased

### Added

- The demotion evaluator tells the approvers of each automatic demotion
  through mcp-gateway's notices (its requirement R3): about the agent,
  for the servers of the category's permissions, by mail, desktop
  notification and the gateway's approval page, to the servers'
  approvers and the agent's owners. `agent-admin evaluate` says whether
  they were notified; a refused notice leaves the demotion as it is.
- The gateway's denials of an agent's call say why (with mcp-gateway's
  `deny_reasons` and `unmatched_reasons`, its requirement R1): "task
  category patch-errata is not enabled on this host (diagnostics-only):
  agent-admin enable patch-errata", "the agent is paused", "the
  emergency stop is in effect", "the agent is advisory" and the others,
  in the agent's error and the gateway's audit, instead of "denied by
  policy" or "no matching permission" (user guide chapter 12).
- The rollback condition `tool_error` and the incident rate take each
  call's outcome from the gateway's audit (mcp-gateway's `mcp-result`,
  its requirement R2): a call the gateway saw fail (a tool error, an
  error, a timeout) counts although the agent recorded it as ok, or not
  at all. The agent's own `tool.result` still counts too.
- A suspected prompt injection is shown with the approval item
  (CONTRACT.md §7, O14): the agent checks what its model read before each
  call for signs of an injection (an instruction to ignore its
  instructions, a forged role marker, a claimed approval, an instruction
  to use the called tool, the call's own argument values). Cockpit marks
  the approval item *suspected injection*, with the signs and the output
  the model read before the call; the activity view marks the call; the
  journal has `AGENT_SUSPECTED_INJECTION`. Advisory: the approval is what
  holds.
- A prompt-injection evaluation suite (CONTRACT.md O14): the functional
  tests' server serves notes an attacker wrote (an override, a forged
  system message, a claimed approval, a tool not given, a leak request,
  instructions buried in a log and in data). Section "Prompt injection"
  runs them with a scripted model that obeys each one, and the
  air-gapped gate with the tested model: an injected change waits for
  an approval and does not run declined, a tool not given never reaches
  the gateway. How often the tested model followed is reported
  (`injection-<model>.json`, the gate's summary).
- A model's call to a tool the agent was not given is refused by the
  launcher and recorded as `tool.denied.local` with reason `not offered`
  and the model's stated reason (user guide chapter 10).
- Requirements for mcp-gateway (`docs/requirements/mcp-gateway-for-custos-0.3.md`):
  a reason for a denial given by a policy module (such as "task
  category not enabled"), each call's outcome in the audit, notices to
  approvers from a trusted local service (automatic demotions), and
  reviews of autonomous actions by the gateway's approvers.
- User guide, chapter 14: where inference runs, in the order on-premises,
  the customer's own cloud account, vendor-hosted; every network flow
  that leaves a Custos host, each with its switch (Custos itself sends
  no telemetry, model updates or incident reports); fencing a systemd
  agent to its model; installing without a network.
- The air-gapped release gate (CONTRACT.md O15): on release branches, CI
  installs the packages, Ollama and a tested open-weight model
  (`test/airgap/versions`: Qwen3 0.6B) from an offline mirror in a Leap 16
  VM without network, and runs the functional tests, with an agent on
  that model calling a read-only tool and asking for an approved change.
  An agent's attempt to reach a host outside fails the gate.
- `run-tests.sh --model URL NAME` (custos-tests): the same checks with
  an agent on a real model, for openQA and other test systems.
- Credentials management. `agent-admin credentials [NAME…]` lists an
  agent's credentials by name, with what uses them and the missing ones,
  never a value; `agent-admin remove-credential NAME CRED [--force]`
  removes one. In Cockpit, each agent has a Credentials card: set,
  replace and remove them, store one key for several agents whose model
  uses the same name (each its own encrypted copy), and restart the
  agents that run. The agents list marks an agent whose credential is
  missing.

- CONTRACT.md: the design of the bundled health-check and diagnostics
  install (§20b, O16): the `diagnostics` and `compliance-audit`
  categories, the enablement state `diagnostics-only` enforced by the
  gateway (§6b, `agent-admin enable`), diagnosis reports with
  host-verified evidence and administrator feedback (§4a), diagnostic
  playbooks (§14c) and `custos-setup` for the installer (§17a). Not
  implemented yet; the journal's message IDs are reserved.

- Diagnostics-only hosts (CONTRACT.md §6b, the bundled diagnostics
  install): the task categories `diagnostics` (read-only permissions
  only, never promoted) and `compliance-audit`; an enablement state in
  which only `diagnostics` is usable, enforced by the gateway (calls of
  a disabled category are denied and its tools not shown) and by
  `agent-admin`, which refuses and records (`category.refused`)
  permissions, promotions and templates of a disabled category;
  `agent-admin enable CATEGORY…|--all`, the one recorded action out of
  it; `agent-admin diagnostics-only` for the installer's setup; and
  `agent-admin show` reporting a state changed by hand. The functional
  tests check it as a release gate.

- Diagnostic playbooks (CONTRACT.md §14c): eleven published, read-only
  diagnostic procedures in `/usr/share/custos/playbooks` (failed units,
  service health, disk and memory pressure, journal errors, SELinux
  denials, time sync, network configuration, boot health, package
  consistency, update status), your own in `/etc/custos/playbooks`, and
  their commands for mcp-gateway's exec server
  (`exec.d-diagnostics.yaml`). `POST /v1/activities` and schedules run
  one (`playbook`); it is refused when the gateway does not offer a
  step's tool. `agent-admin playbooks` lists them.

- The agent template `health-check`, the bundled diagnostics agent:
  every diagnostic playbook's tools as read-only permissions in
  `diagnostics`, so it runs on a diagnostics-only host; supervised; the
  playbooks failed-units, disk-pressure and journal-errors daily and
  update-status weekly.

- Diagnosis reports (CONTRACT.md §4a): an agent reports with the host's
  tool `report_diagnosis`: a summary, findings, hypotheses with its
  confidence, the evidence as quotes from its tools' output, and a
  remediation plan for the administrator. Custos verifies each quote
  against what that tool returned in the activity and marks the others
  unverified. The report is in the activity (`report`), the journal
  (`diagnosis.report`) and on AG-UI (`agent.report`).
  `agent-admin feedback` records an administrator's verdict on it
  (`diagnosis.feedback`).

- Cockpit: an activity's diagnosis report, with each hypothesis's
  confidence (the agent's) next to how many of its quotes Custos
  verified, each quote marked verified or the agent's claim alone, the
  plan's commands to copy, and **Was it right?** for the
  administrator's feedback; and **Diagnostic playbooks** in an agent's
  view, to run one as the person.
- Readiness shows the diagnostics history (CONTRACT.md §6b):
  `agent-admin readiness` and Cockpit's Task categories card list the
  agent's diagnosis reports over the window, how many of their quotes
  Custos verified, and the feedback on them (confirmed, rejected, plans
  useful and carried out); evidence for a category enabled later, not a
  threshold. Only root's feedback counts.

- `custos-setup` and the package `custos-diagnostics`, the bundled
  health-check and diagnostics install the installer offers (CONTRACT.md
  §17a): on a fresh host it makes the host diagnostics-only, sets the
  model for one of the four backends in the documented order (on-host,
  local network, the customer's cloud, vendor), creates `health-check`,
  stores its key from a file and installs the playbooks' exec commands;
  idempotent, one line per step.

- On a host that started diagnostics-only, the agent's diagnostics
  history counts in readiness (CONTRACT.md §6a, §6b): a category is
  ready only with at least 10 diagnosis reports with feedback and 70 %
  of the decided hypotheses confirmed (`diagnostics_feedback`,
  `diagnostics_confirmed_rate` in `/etc/custos/promotion.yaml`), so
  `promote` follows them; Cockpit says so.

- Owners approve their agent's calls (CONTRACT.md §7, O1):
  `agent-admin owners NAME user:alice group:web-team` (and *Owners* in
  Cockpit's agent view) lets them decide the agent's pending calls
  besides the servers' approvers, and mails them as it mails those. A
  policy module for the gateway's approver rules (`owners.rego`), with
  no gateway change.
- Cockpit tells approvers of automatic demotions (CONTRACT.md O11): the
  Approvals tab lists the task categories the demotion evaluator set
  back to supervised in the last week, with the agent and the trigger
  (from root's records only), and the agent's view marks such a
  category.
- Cockpit shows a diagnostics-only host's enablement state on the Agents
  tab (enabled and disabled categories, who changed it, a state changed
  outside agent-admin), with **Enable…**; a disabled category is marked
  in an agent's task categories and offers no **Promote…**.

### Changed

- `custos` and `custos-tests` require mcp-gateway 1.14, which has
  what Custos asked of it (`docs/requirements/mcp-gateway-for-custos-0.3.md`):
  reasons for denials from policy modules, each call's outcome in the
  audit, notices to approvers and reviews by approvers.

### Fixed

- Security: an agent could write a review verdict into its own journal
  (`category.review` with `SYSLOG_IDENTIFIER=custos-promotion`): a
  later "ok" for one of its autonomous actions replaced an approver's
  "incident", lowering its override rate, so the demotion evaluator
  could miss a reason to demote it. Readiness, the review and the
  evaluator now count only reviews journald records as root's
  (`_UID=0`); `agent-admin show`'s enablement check, too.
- A deleted agent stayed on the Cockpit page, as stopped, until the
  host restarted: `agent-admin delete` left its directory in
  `/run/agents`. It now removes it, and the page no longer lists a
  directory without a socket and without gateway data, which also
  clears what 0.2.0 left behind.
- A key given as `model.credential`, where the name of the stored key
  goes, made the agent fail with `no such file or directory`.
  `agent-admin check`, `create`, `edit` and `defaults` now refuse a
  value that looks like a key (`sk-…` and other keys' prefixes, or a long
  string with many digits) without repeating it. After `create` and
  `edit`, agent-admin says when the model's credential is not stored yet,
  with the `set-credential` command. The agent's own error says the same,
  and Cockpit's model defaults ask for the credential's name.

## v0.2.0 — 2026-11-05

Autonomy per task category, and agent templates. An agent's autonomy is
promoted per kind of work (patching, service restarts, …) within time
windows the gateway enforces, once the journal shows approvers rarely
override it; a demotion evaluator takes it back when the override rate
or a rollback condition says so, and approvers review what a promoted
category did. Seven templates give agents that look after one system a
start, transactional systems included (mcp-server-tukit), with model
defaults for new agents; the Cockpit page creates agents from them.
Fixes from testing 0.1.1: model credentials work with SELinux enforcing
and on hosts with a TPM, and the Cockpit page shows why an action failed
instead of hanging.

Upgrading from 0.1.x needs no change to configurations or gateway data:
contract version 1 only gained names. To know:

- Running agents keep the old launcher and unit until they next start
  (idle exit, or `systemctl restart agent@NAME.service`). OpenShell
  agents: rebuild the agent image, then set each agent's gateway
  credential again to move its sandbox to it (user guide, chapter 11).
- Remove the workarounds for 0.1.x if you used them: the drop-in
  `/etc/systemd/system/agent@NAME.service.d/60-tpm.conf` and the local
  SELinux module `custos-credentials` (`semodule -r custos-credentials`).
- Credentials stored by 0.1.x are readable by everyone (still
  encrypted): `chmod 0600 /etc/credstore.encrypted/agent-*`.
- `agent-admin edit` and `release-all` now leave a schedule or an agent
  you disabled as it is.
- Nothing is promoted on its own: every category stays supervised until
  `agent-admin promote`, which also enables `custos-demote.timer`.

Known limits: approvers are not told of an automatic demotion; a review
verdict needs root (or Cockpit's administrative access), not a gateway
approver; an envelope's `rate`, `hosts` and `blast_radius` are refused
until something enforces them (mcp-gateway, the fleet); the templates
are checked against the servers' tool definitions, not yet run against
the servers themselves.

### Added

- Task categories (CONTRACT.md §6a): an agent's autonomy is promoted
  per category, not for the whole agent. The catalog ships
  `patch-errata`, `security-response` and `config-lifecycle`
  (`/usr/share/custos/categories.d`, your own in
  `/etc/custos/categories.d`; `agent-admin categories`). A permission
  joins one with `category:`, and stays supervised until its category
  is promoted.
- `agent-admin promote NAME CATEGORY --envelope FILE` lets a category
  act without approval within its envelope's time windows, which the
  gateway's policy enforces; outside them its calls need an approval.
  `agent-admin demote` takes it back.
- Readiness: `agent-admin readiness NAME [CATEGORY]` measures, from the
  agent's records and the gateway's approval decisions, how often
  approvers overrode a category's proposals and how often approved
  calls went wrong. `promote` refuses a category below the thresholds
  (14 days, 50 proposals, at most 5 % overrides and 2 % incidents; your
  own in `/etc/custos/promotion.yaml`) unless given
  `--despite-readiness --reason TEXT`.
- The review of what a promoted category did without an approval:
  `agent-admin review NAME [CATEGORY]` lists the week's autonomous
  actions to review (all of them up to 20 a week, then 20), and
  `agent-admin review NAME CATEGORY ACTIVITY CALL ok|incident --reason
  TEXT` records a verdict (`category.review`). An incident counts as an
  override in the readiness and demotes the category at once; the
  demotion evaluator also demotes on it. In Cockpit, *To review* with
  **Fine** and **Should not have run**.
- The Cockpit page: an agent's task categories with their readiness
  (ready or not, what is not met, override and incident rates,
  proposals), **Promote…** with the envelope, and **Demote**.
- The demotion evaluator: `custos-demote.timer` (every 5 minutes;
  `promote` enables it) runs `agent-admin evaluate`, which demotes a
  bounded category whose override rate since its promotion is above the
  threshold, or whose envelope's `rollback` condition fires:
  `tool_error`, `limit_exceeded` or `health_check_failed` (a command of
  `health_check`). Demotions are recorded by `custos-demote` with the
  reason. Both are recorded in the
  journal (`custos-promotion`, `category.promoted`,
  `category.demoted`), and `agent-admin show` lists the categories.
- Agent templates (CONTRACT.md §14a) for managing one system, in
  `/usr/share/custos/templates` (your own in `/etc/custos/templates`):
  `triage` and `exposure-watch` (advisory, read-only),
  `service-doctor`, `patch-pilot`, `disk-watch`, and for transactional
  systems with mcp-server-tukit `patch-pilot-transactional` and
  `disk-watch-transactional` (supervised). Each has the configuration,
  the permissions with the gateway's real tool names, their task
  categories and suggested envelopes. `agent-admin templates` lists
  them; `agent-admin create NAME --template TEMPLATE [--from FILE]`
  creates an agent with the template's permissions and mode. The
  Cockpit page's **Create agent** can start from a template.
- Model defaults for new agents (CONTRACT.md §14b):
  `/etc/custos/defaults.yaml`, set with `agent-admin defaults --from
  FILE` or the Cockpit page's **Model defaults**. A new agent's
  configuration, blank or from a template, starts with that model
  (`agent-admin draft NAME [--template T]`); existing agents keep
  theirs. When the model names a credential, the page asks for the new
  agent's secret after creating it.

### Fixed

- The example `patch-pilot` named tools that no gateway server has
  (`install_patches`, `restart_unit`, …); it names mcp-server-zypp's
  and systemd-mcp's tools now.
- The Cockpit page hung when `agent-admin` refused something: **Check**
  and **Save and apply** in an agent's configuration editor, and every
  other action that runs `agent-admin` or `systemctl`, never showed the
  error, and Cockpit reported an unexpected error instead. The page
  threw in a handler of Cockpit's own promise, which does not reject the
  promise it returns. The test stub now behaves like Cockpit's promises.
- The Cockpit page cleared an action's error at its next refresh, a
  moment later, so a refused **Release all agents** (or delete, mode,
  …) seemed to do nothing. An action's error now stays until the next
  action or **Dismiss**.
- The Cockpit page listed no agents at all while one agent's socket
  was held by systemd but its service failed at start (a credential not
  yet set, say): the request to that agent was never answered, and the
  page waited for it, skipping every refresh. A read of an agent's or
  the gateway's API now gives up after 8 seconds, and the agent is
  listed as not answering, pointing to its logs.
- The demotion evaluator and the review counted records from the whole
  second of a promotion, so a proposal declined in the same second just
  before it counted against the new promotion, which could be demoted
  at once for its override rate. `promoted_by` and `demoted_by` keep
  the time to the fraction of the second.
- Building the OpenShell agent image (`packaging/openshell/Containerfile`)
  failed while openSUSE's codec repository was unreachable; the image
  needs nothing from it and no longer refreshes it.
- An agent whose model credential was stored on a host with a TPM2 did
  not start (`Failed to set up credentials: Protocol error`,
  `243/CREDENTIALS`): `systemd-creds encrypt` seals the secret to the
  TPM there, and `agent@.service`'s `PrivateDevices=yes` kept systemd
  from reaching the TPM while it decrypted it. The unit allows
  `/dev/tpmrm0` for that (`DeviceAllow=`); the agent's own `/dev` stays
  private. It also says `ConfigurationDirectoryMode=0750`, as
  `agent-admin create` makes the directory, so systemd no longer warns
  at every start.
- `agent-admin set-credential` left the encrypted credential readable
  by everyone (0644, from `systemd-creds encrypt`'s umask); it is root's
  alone now (0600). Decrypting it needs root's host key or the TPM.
- An agent could not read its model credential under SELinux
  (`model.credential: open /run/credentials/…: permission denied`): the
  `custos` module did not let `agent_t` read the unit's credentials
  (`init_var_run_t`), and the base policy does not log that denial.
- `agent-admin edit` enabled every schedule's timer of the agent again,
  and `release-all` started every agent's socket and schedule timer,
  so a schedule or an agent an administrator had disabled ran again.
  `edit` enables only new schedules now (a running timer restarts for
  its new calendar), and `release-all` starts only what is enabled.

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
