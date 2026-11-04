# Packaging for SLES 16 and openSUSE Leap 16 (OBS)

`custos.spec`, `_service` and `custos-rpmlintrc` are
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
<!-- osc meta prj -e home:<you>:custos -->
<project name="home:<you>:custos">
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
osc mkpac custos
cd custos
cp <repo>/packaging/suse/{custos.spec,_service,custos-rpmlintrc} .
osc service manualrun            # tarball from main, version, vendor.tar.gz
osc vc -m "Initial package"
osc add *
osc commit
```

As with mcp-gateway, the services refresh the sources only. Copy the
spec, `_service` and the rpmlintrc again whenever they change here.

| Package | Contents |
|---|---|
| `custos` | `agent-launcher`, `agent-delegate`, `agent-admin`, the unit templates `agent@` and `agent-schedule@`, the group `agent-clients`, the autonomy-mode policy module for the gateway (`/usr/share/mcp-gateway/policy/custos/agents.rego`), the gateway's `agents` server definition (opt-in), an example agent |
| `custos-selinux` | the `custos` module; pulled in where `selinux-policy-targeted` is installed |
| `custos-tests` | the functional tests for openQA and other test systems (`/usr/libexec/custos/tests/run-tests.sh --disposable --results DIR`: TAP and JUnit), with a scripted model server and an MCP test server; they change the system for good, so only for test systems ([test/openqa](../../test/openqa/README.md)) |
| `custos-cockpit` | the Cockpit page: the agents' activities, approvals (decided as the logged-in person), agents and their posture, audit, chat and the emergency stop; requires `cockpit-bridge` |

`custos-cockpit` is the Cockpit page (`/usr/share/cockpit/custos`), the
control plane built on CONTRACT.md Part I; see `docs/screenshots`.

## After installation

```bash
usermod -aG agent-clients <admin>              # who may supervise agents
agent-admin create patch-pilot --from /usr/share/custos/examples/patch-pilot/agent.yaml
agent-admin permissions patch-pilot < /usr/share/custos/examples/patch-pilot/permissions.yaml
agent-admin mode patch-pilot supervised        # the default
```

Agents run fully on-premises with a local OpenAI-compatible model
server (`model.provider: openai`). No network access beyond that
endpoint and the gateway's socket is needed.

## Before the first release

- **SELinux:** `custos.te` has not yet been built or run against
  SLES 16 / Leap 16 policy. Build it (`make selinux` with
  `mcp-gateway-selinux` installed), then run an agent in enforcing mode
  and resolve the AVC denials (`ausearch -m AVC -c agent-launcher`).
- **Go version:** check that each target repository's `golang(API)`
  satisfies `>= 1.25` (ADK Go 1.9 needs it), or add the path above.
- **Tests on the target:** `%check` runs the unit and end-to-end tests
  (scripted model, fake gateway). The functional tests
  (`custos-tests`) have not run on SLES 16 / Leap 16 yet: run
  them in openQA or a VM first (test/openqa/README.md).
