#
# spec file for package cockpit-agentic
#
# Built on the Open Build Service for SUSE Linux Enterprise Server 16.0
# and 16.1 and openSUSE Leap 16.0 and 16.1; see packaging/suse/README.md.
# Sources: the tarball and vendor.tar.gz (Go modules, OBS builds are
# offline) come from the _service file.
#

%define selinuxtype targeted
%define modulename cockpit_agentic

Name:           cockpit-agentic
Version:        0.1.0
Release:        0
Summary:        Governed agents on the host, supervised from Cockpit
# MIT for cockpit-agentic; the others for the vendored Go modules linked in.
License:        MIT AND Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause
Group:          System/Management
URL:            https://github.com/sdrahn/cockpit-agentic
Source0:        %{name}-%{version}.tar.gz
Source1:        vendor.tar.gz
Source99:       %{name}-rpmlintrc
BuildRequires:  golang(API) >= 1.25
BuildRequires:  make
BuildRequires:  pkgconfig(systemd)
BuildRequires:  sysuser-tools
BuildRequires:  systemd-rpm-macros
# The agents' tools, policy and approvals are mcp-gateway's (CONTRACT.md §1).
# NVIDIA OpenShell is optional (sandbox kind openshell, CONTRACT.md §16a).
Suggests:       openshell
Requires:       mcp-gateway >= 1.12
Requires(pre):  group(mcp-users)
Requires:       (%{name}-selinux if selinux-policy-%{selinuxtype})
%sysusers_requires
%{?systemd_ordering}

%description
cockpit-agentic runs AI agents (Google ADK) on the host, each as its
own systemd service and system user, governed by mcp-gateway: the
agents' only tools are the gateway's, its OPA policy decides what each
agent may do in its autonomy mode (advisory, supervised, bounded), and
state-changing calls wait for a human approval. Agents are started by
people (chat, AG-UI), schedules or other agents (delegation through the
gateway), and every activity is recorded in the journal.

%package selinux
Summary:        SELinux policy module for cockpit-agentic
Group:          System/Management
BuildArch:      noarch
# mcp_gateway.if, the interfaces the module uses.
BuildRequires:  mcp-gateway-selinux
BuildRequires:  selinux-policy-devel
Requires:       mcp-gateway-selinux
Requires:       selinux-policy-%{selinuxtype}
Requires(post): selinux-policy-%{selinuxtype}
%{?_selinux_policy_version:%global _selinux_policy_version %(echo '%{_selinux_policy_version}' | cut -d- -f1)}
%{?selinux_requires}

%description selinux
SELinux domain agent_t for cockpit-agentic's agents (they may reach the
gateway's MCP socket, never its control socket nor another agent's
socket), and mcpsrv_agents_t for the gateway's delegation server.

%package tests
Summary:        Functional tests of cockpit-agentic, for openQA
Group:          Development/Tools/Other
Requires:       %{name} = %{version}
Requires:       mcp-gateway >= 1.12
# What the tests run: the gateway's control API and the agents' sockets
# (curl, jq), the journal, nsenter and runuser (util-linux), and the
# kernel audit log and SELinux tools for the denial check.
Requires:       audit
Requires:       curl
Requires:       jq
Requires:       policycoreutils
Requires:       shadow
Requires:       util-linux
Recommends:     %{name}-selinux = %{version}

%description tests
Functional tests of cockpit-agentic on an installed system: agents
created with agent-admin and run by systemd, governed by mcp-gateway
(supervised by default, approvals, autonomy modes, pause, emergency
stop), schedules, AG-UI, delegation, the journal records, the sandbox
and SELinux. With a scripted model server and an MCP test server, so
no model is needed. For openQA and other test systems:
/usr/libexec/cockpit-agentic/tests/run-tests.sh --disposable --results DIR.
They change the system for good; install them on test systems only.

%prep
%autosetup -p1 -a1

%build
export GOFLAGS="-mod=vendor"
%make_build build build-tests VERSION=%{version}
make selinux
%sysusers_generate_pre packaging/sysusers/cockpit-agentic.conf %{name} %{name}.conf

%install
%make_install install install-selinux install-tests \
    PREFIX=%{_prefix} LIBEXECDIR=%{_libexecdir} DATADIR=%{_datadir} SBINDIR=%{_sbindir} \
    SYSCONFDIR=%{_sysconfdir} UNITDIR=%{_unitdir} SYSUSERSDIR=%{_sysusersdir} \
    TMPFILESDIR=%{_tmpfilesdir} SELINUXDIR=%{_datadir}/selinux/packages/%{selinuxtype}

%check
export GOFLAGS="-mod=vendor"
go test ./internal/... ./test/...

%pre -f %{name}.pre

%post
%tmpfiles_create %{_tmpfilesdir}/%{name}.conf
# Agents are template instances, enabled by agent-admin create.

%postun
# Running agents keep the old launcher until they exit on idle or are
# restarted; restarting them here could cut off an activity waiting for
# an approval.

%pre selinux
%selinux_relabel_pre -s %{selinuxtype}

%post selinux
%selinux_modules_install -s %{selinuxtype} %{_datadir}/selinux/packages/%{selinuxtype}/%{modulename}.pp.bz2

%postun selinux
if [ $1 -eq 0 ]; then
    %selinux_modules_uninstall -s %{selinuxtype} %{modulename}
fi

%posttrans selinux
%selinux_relabel_post -s %{selinuxtype}

%files
%license LICENSE
%doc README.md CONTRACT.md
%{_sbindir}/agent-admin
%dir %{_libexecdir}/cockpit-agentic
%{_libexecdir}/cockpit-agentic/agent-launcher
%{_libexecdir}/cockpit-agentic/agent-delegate
%{_libexecdir}/cockpit-agentic/agent-openshell-relay
%{_libexecdir}/cockpit-agentic/agent-admin
%{_unitdir}/agent@.service
%{_unitdir}/agent@.socket
%{_unitdir}/agent-schedule@.service
%{_unitdir}/agent-schedule@.timer
%{_unitdir}/agent-openshell@.service
%{_unitdir}/agent-openshell@.socket
%{_sysusersdir}/%{name}.conf
%{_tmpfilesdir}/%{name}.conf
%dir %{_sysconfdir}/cockpit-agentic
%dir %{_sysconfdir}/cockpit-agentic/agents
%dir %{_datadir}/mcp-gateway
%dir %{_datadir}/mcp-gateway/policy
%{_datadir}/mcp-gateway/policy/cockpit-agentic
%{_datadir}/cockpit-agentic

%files tests
%dir %{_libexecdir}/cockpit-agentic
%dir %{_libexecdir}/cockpit-agentic/tests
%{_libexecdir}/cockpit-agentic/tests/run-tests.sh
%{_libexecdir}/cockpit-agentic/tests/fakemodel
%{_libexecdir}/cockpit-agentic/tests/testsrv
%{_libexecdir}/cockpit-agentic/tests/fakegateway

%files selinux
%dir %{_datadir}/selinux/packages/%{selinuxtype}
%{_datadir}/selinux/packages/%{selinuxtype}/%{modulename}.pp.bz2

%changelog
