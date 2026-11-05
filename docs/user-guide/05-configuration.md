---
title: "5. Configuring an agent (agent.yaml)"
description: Every key of agent.yaml with its default, the model providers and their credentials, the gateway keys, limits, interaction, schedules, sessions, the sandbox, SELinux and the model's port, and checking a configuration.
tags: [configuration, agent.yaml, model, credentials, schedules, limits]
---

# 5. Configuring an agent (agent.yaml)

An agent's configuration is one YAML file, installed as
`/etc/custos/agents/<name>/agent.yaml` (owned `root:agent-<name>`,
mode 0640) by `agent-admin create` and `agent-admin edit`. Unknown keys
are errors. This chapter explains each key. What an agent *may do* at
the gateway is not here: that is its permissions
([chapter 6](06-permissions-and-modes.md)).

## A complete example {#example}

```yaml
version: 1
name: patch-pilot
description: Keeps the host patched within the maintenance window.
model:
  provider: openai
  name: qwen3-32b
  endpoint: http://127.0.0.1:8000/v1
  credential: model-key          # optional
  temperature: 0.2               # optional
instruction: |
  You keep this host patched. Look before you change anything.
gateway:
  servers: [zypp, systemd]
limits: {max_llm_calls: 25, max_tool_calls: 40, max_invocation_seconds: 1800}
interaction: {agui: true, client_tools: deny}
schedules:
  - {name: compliance, on_calendar: "Mon..Fri 06:00", prompt: "List the pending security patches. Install nothing."}
sessions: {retention_days: 30}
sandbox: {kind: systemd}
```

## Top-level keys {#top-level}

| Key | Required | Meaning |
|---|---|---|
| `version` | yes | `1`, the contract version this launcher implements |
| `name` | yes | the agent's name: `^[a-z][a-z0-9-]{0,30}[a-z0-9]$`. It must match the directory and the `NAME` given to `agent-admin` |
| `description` | no | one line shown in Cockpit |
| `model` | yes | [the model](#models) |
| `instruction` or `instruction_file` | no; at most one of them | the system instruction: inline text, or a file read at start (a relative path is relative to `/etc/custos/agents/<name>/`; put the file there yourself) |
| `gateway` | yes | [the gateway and the tools shown](#gateway-keys) |
| `limits` | no | [local limits](#limits) |
| `interaction` | no | [chat](#interaction) |
| `schedules` | no | [scheduled runs](#schedules) |
| `sessions` | no | `retention_days`: chat sessions untouched this long are deleted (default 30) |
| `sandbox` | no | `kind: systemd` (default) or `kind: openshell` ([chapter 11](11-openshell.md)) |

## Models {#models}

`model.provider` names the kind of model service:

| Provider | Keys | Credential |
|---|---|---|
| `openai` | `name`, `endpoint` (an `http://` or `https://` URL ending in the API's base, such as `http://127.0.0.1:8000/v1`) | optional API key (`credential`) |
| `gemini` | `name` (such as `gemini-2.5-flash`) | required: the Gemini API key (`credential`) |
| `vertex` | `name` | none in the file: Google's Application Default Credentials and the project and location, from the environment ([below](#vertex)) |

Every provider also takes `temperature` (a number; the model's own
default if absent). `credential` is the **name** of a secret stored with
`agent-admin set-credential`, such as `model-key`, never the key itself
([below](#model-credentials)). An unknown key in `model:`, such as
`api_key`, is refused.

`openai` covers any server that implements OpenAI's chat completions
with tool calls: vLLM, llama.cpp's server, Ollama's OpenAI endpoint,
LiteLLM and hosted services. With a server on the host or the local
network, the agent needs nothing from the internet. Prefer, in this
order, a model on-premises, then one in your own cloud account, then a
vendor's service; [chapter 14](14-network.md#inference) says what each
sees and every other network flow.

`model.endpoint` is only for `openai`: with `gemini` or `vertex` it is
an error (`model.endpoint: only for provider openai`).

### The model for new agents {#model-defaults}

When all agents on a host use the same model server, set it once as
the default for new agents:

```bash
agent-admin defaults --from - <<'EOF'
model:
  provider: openai
  name: granite-4
  endpoint: http://ai.example.com:8000/v1
  credential: model-key        # optional: a name, not the secret
EOF
agent-admin defaults           # show them, as JSON
agent-admin defaults --clear   # remove them
```

They are kept in `/etc/custos/defaults.yaml` and checked like a
configuration's `model:`. A new agent's configuration starts with them,
blank (`agent-admin draft NAME`) or from a template
(`agent-admin draft NAME --template T`, `agent-admin create NAME
--template T`): they replace the template's model. The agent then has
the model in its own `agent.yaml`, so changing the defaults later
changes no existing agent; change those with `agent-admin edit`. The
defaults hold no secret: each agent's credential is stored for it
([below](#model-credentials)). In Cockpit: *Agents*, **Model defaults**.
After creating an agent whose model names a credential, the page asks
for its secret.

### Model credentials {#model-credentials}

A secret never goes into `agent.yaml`. Store it encrypted with
`systemd-creds` and name it in `model.credential`:

```bash
agent-admin set-credential patch-pilot model-key < /root/model-key.txt
shred -u /root/model-key.txt
```

`set-credential` writes `/etc/credstore.encrypted/agent-patch-pilot.model-key`
(root, 0600), encrypted with the host's key and, on a host with a TPM2,
sealed to the TPM as well. The agent's unit imports it
(`ImportCredential=agent-%i.*`): systemd decrypts it when the agent
starts, and the launcher reads it from `$CREDENTIALS_DIRECTORY`. Then set
`model.credential: model-key` and run `agent-admin edit`. The name is
your choice (lower-case letters, digits, `-` and `_`, starting with a
letter or digit), but `gateway-token` and `gateway-client-secret` are
reserved for OpenShell agents.

`model.credential` is the *name*, never the key. A key typed there, such
as `sk-…`, would not reach the model: the agent looks for a stored
credential of that name and does not start. `agent-admin check`,
`create`, `edit` and `defaults` refuse a value that looks like a key
(a key's usual prefix such as `sk-` or `hf_`, or a long string of
letters and digits), without repeating it. After `create` and `edit`,
agent-admin says when the named credential is not stored yet, with the
`set-credential` command that stores it. Deleting an agent removes its
credentials: an agent created again under the same name needs them
again. A model server that needs no key needs no `credential:`.

### Vertex AI {#vertex}

The `vertex` provider reads `GOOGLE_CLOUD_PROJECT` and
`GOOGLE_CLOUD_LOCATION` from the agent's environment and authenticates
with Google's Application Default Credentials. Set them in a drop-in of
the agent's unit, for example with a service account key stored as a
credential:

```bash
agent-admin set-credential patch-pilot vertex-sa < /root/sa.json
systemctl edit agent@patch-pilot.service
#   [Service]
#   Environment=GOOGLE_CLOUD_PROJECT=my-project GOOGLE_CLOUD_LOCATION=europe-west4
#   Environment=GOOGLE_APPLICATION_CREDENTIALS=%d/agent-patch-pilot.vertex-sa
systemctl restart agent@patch-pilot.service
```

(`%d` is the unit's credentials directory.) The host then needs HTTPS to
Google's endpoints.

### SELinux and the model's port {#selinux-and-the-models-port}

SELinux lets an agent (`agent_t`) connect to ports labelled
`http_port_t` and `http_cache_port_t` (80 and 443 among others; list
them with `semanage port -l | grep -E 'http_(port|cache_port)_t'`). A model server on another
port is refused with `Permission denied` until you either label the
port or allow all ports:

```bash
semanage port -a -t http_port_t -p tcp 8000 2>/dev/null || semanage port -m -t http_port_t -p tcp 8000
# or, for every port:
setsebool -P agent_connect_any on
```

Labelling one port is the narrower choice.

## Gateway keys {#gateway-keys}

| Key | Default | Meaning |
|---|---|---|
| `gateway.socket` | `/run/mcp-gateway/mcp.sock` | the gateway's MCP socket (agents with the `systemd` backend) |
| `gateway.servers` | (required) | the gateway servers whose tools the model is shown; at least one |
| `gateway.tools` | all of those servers' tools | a narrower list of `<server>__<tool>`; each server must be in `servers` |
| `gateway.url` | | instead of `socket`: the gateway's HTTPS endpoint, for OpenShell agents ([chapter 11](11-openshell.md)) |
| `gateway.token_env` | | with `url`: the environment variable holding the bearer token |
| `gateway.oauth` | | with `url`, OpenShell only: the OAuth client that gets the agent's tokens: `token_url`, `client_id`, `scopes`, `refresh_before_seconds` ([chapter 11](11-openshell.md#oauth)) |

`servers` and `tools` only decide what the model is shown. What it may
call is decided by the gateway from the agent's permissions; a tool
shown but not permitted is refused there.

## Limits {#limits}

| Key | Default | Ends the activity when |
|---|---|---|
| `limits.max_llm_calls` | 25 | the model was asked this often in one activity |
| `limits.max_tool_calls` | 40 | this many tool calls were made in one activity |
| `limits.max_invocation_seconds` | 900 | the activity ran this long (waiting for an approval counts) |

An activity ended by a limit has the state `limit`, and the journal has
a `limit.exceeded` record. Limits are the launcher's: they help, but the
gateway's policy and approvals are what protect the system.

## Interaction (chat) {#interaction}

| Key | Default | Meaning |
|---|---|---|
| `interaction.agui` | `false` | serve AG-UI on the agent's socket: chat in Cockpit |
| `interaction.client_tools` | `deny` | tools a chat frontend offers the model: `deny`, or `{allow: [name, …]}`. Names with `__` are always refused, so they cannot pose as gateway tools |

## Schedules {#schedules}

Each schedule runs the agent with a prompt at times given as a systemd
calendar expression (`systemd.time(7)`):

```yaml
schedules:
  - {name: compliance, on_calendar: "Mon..Fri 06:00", prompt: "List the pending security patches. Install nothing."}
  - {name: weekly-cleanup, on_calendar: "Sun 03:00", prompt: "Clean the package cache."}
```

`name` is unique per agent: lower-case letters, digits and `-`, starting
with a letter, up to 31 characters. `agent-admin` turns each into the
timer `agent-schedule@<agent>_<schedule>.timer`, with the calendar in
`/etc/systemd/system/agent-schedule@<agent>_<schedule>.timer.d/schedule.conf`.
Check an expression with `systemd-analyze calendar "Mon..Fri 06:00"`. A
scheduled activity runs as `system:schedule` with the origin
`schedule · <name>`, under the same mode, permissions and approvals as
any other. In supervised mode a scheduled change waits for an approval
like a chat's.

## Checking and changing a configuration {#check-and-edit}

```bash
agent-admin check /root/patch-pilot.yaml      # a file; needs no root
agent-admin check patch-pilot                 # the installed configuration
agent-admin edit patch-pilot --from /root/patch-pilot.yaml
```

`check` reports every problem at once; the messages are in
[chapter 12](12-troubleshooting.md#configuration-errors). `edit`
validates, replaces the file and syncs the schedules. It then restarts
a running agent (`try-restart`), which ends its activities in flight;
an idle agent picks the change up when it next starts. The name and
`sandbox.kind` cannot change with `edit`: delete and create the agent
instead. In Cockpit, *Configuration* in the agent's view does the same
([chapter 8](08-cockpit.md#agents)). `-` reads the configuration from
standard input: `agent-admin edit patch-pilot --from - < file`.
