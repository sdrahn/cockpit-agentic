# Packaging for SLES 16 and openSUSE Leap 16 (OBS)

`cockpit-agentic.spec`, `_service` and `cockpit-agentic-rpmlintrc` are
meant for an Open Build Service project. They follow the packaging of
[mcp-gateway](https://github.com/sdrahn/mcp-gateway/tree/main/packaging/suse),
which this package requires.

## Targets

| Distribution | OBS repository (check the names in your OBS instance) |
|---|---|
| SUSE Linux Enterprise Server 16.0 | `SUSE:SLFO:Products:SLES:16.0` / `standard` |
| SUSE Linux Enterprise Server 16.1 | `SUSE:SLFO:Products:SLES:16.1` / `standard` |
| openSUSE Leap 16.0 | `openSUSE:Leap:16.0` / `standard` |
| openSUSE Leap 16.1 | `openSUSE:Leap:16.1` / `standard` |
| openSUSE Tumbleweed | `openSUSE:Factory` / `snapshot` (development only) |

The SLES and Leap 16.1 project names above are what the 16.0 pattern
suggests. Confirm them on build.opensuse.org (or your internal OBS)
before you commit the project meta.

## Setting up the OBS project

The build needs `mcp-gateway-selinux`, which provides `mcp_gateway.if`
for the SELinux module. It also needs `golang(API) >= 1.25`. Build
mcp-gateway in the same project, or add a project that publishes it as
a path for each repository:

```xml
<!-- osc meta prj -e home:<you>:agentic -->
<project name="home:<you>:agentic">
  <title>Governed agents for SLES 16 and Leap 16</title>
  <description/>
  <person userid="<you>" role="maintainer"/>
  <repository name="SLES_16.0">
    <path project="home:<you>:mcp-gateway" repository="SLES_16.0"/>
    <path project="SUSE:SLFO:Products:SLES:16.0" repository="standard"/>
    <arch>x86_64</arch><arch>aarch64</arch><arch>s390x</arch><arch>ppc64le</arch>
  </repository>
  <repository name="SLES_16.1">
    <path project="home:<you>:mcp-gateway" repository="SLES_16.1"/>
    <path project="SUSE:SLFO:Products:SLES:16.1" repository="standard"/>
    <arch>x86_64</arch><arch>aarch64</arch><arch>s390x</arch><arch>ppc64le</arch>
  </repository>
  <repository name="Leap_16.0">
    <path project="home:<you>:mcp-gateway" repository="Leap_16.0"/>
    <path project="openSUSE:Leap:16.0" repository="standard"/>
    <arch>x86_64</arch><arch>aarch64</arch>
  </repository>
  <repository name="Leap_16.1">
    <path project="home:<you>:mcp-gateway" repository="Leap_16.1"/>
    <path project="openSUSE:Leap:16.1" repository="standard"/>
    <arch>x86_64</arch><arch>aarch64</arch>
  </repository>
</project>
```

If a repository lacks Go 1.25, add the project of the `go1.25` package
(for example `devel:languages:go`) as a path. Do this for build only;
the binaries are static apart from libc.

## The package

```bash
osc mkpac cockpit-agentic
cd cockpit-agentic
cp <repo>/packaging/suse/{cockpit-agentic.spec,_service,cockpit-agentic-rpmlintrc} .
osc service manualrun            # tarball from main, version, vendor.tar.gz
osc vc -m "Initial package"
osc add *
osc commit
```

As with mcp-gateway, the services refresh the sources only. Copy the
spec, `_service` and the rpmlintrc again whenever they change here.

| Package | Contents |
|---|---|
| `cockpit-agentic` | `agent-launcher`, `agent-delegate`, `agent-admin`, the unit templates `agent@` and `agent-schedule@`, the group `agent-clients`, the autonomy-mode policy module for the gateway (`/usr/share/mcp-gateway/policy/cockpit-agentic/agents.rego`), the gateway's `agents` server definition (opt-in), an example agent |
| `cockpit-agentic-selinux` | the `cockpit_agentic` module; pulled in where `selinux-policy-targeted` is installed |

There is no `-cockpit` subpackage yet. The Cockpit control plane is
designed (CONTRACT.md Part I, the mockup in `docs/`) but not yet
implemented as a Cockpit page.

## After installation

```bash
usermod -aG agent-clients <admin>              # who may supervise agents
agent-admin create patch-pilot --from /usr/share/cockpit-agentic/examples/patch-pilot/agent.yaml
agent-admin permissions patch-pilot < /usr/share/cockpit-agentic/examples/patch-pilot/permissions.yaml
agent-admin mode patch-pilot supervised        # the default
```

Agents run fully on-premises with a local OpenAI-compatible model
server (`model.provider: openai`). No network access beyond that
endpoint and the gateway's socket is needed.

## Before the first release

- **SELinux:** `cockpit_agentic.te` has not yet been built or run against
  SLES 16 / Leap 16 policy. Build it (`make selinux` with
  `mcp-gateway-selinux` installed), then run an agent in enforcing mode
  and resolve the AVC denials (`ausearch -m AVC -c agent-launcher`).
- **Go version:** check that each target repository's `golang(API)`
  satisfies `>= 1.25` (ADK Go 1.9 needs it), or add the path above.
- **Tests on the target:** `%check` runs the unit and end-to-end tests
  (scripted model, fake gateway). A VM test against a real mcp-gateway
  and OPA is still to do.
