# cockpit-agentic: build, test and install (also used by the OBS spec).
VERSION    ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
PREFIX     ?= /usr
LIBEXECDIR ?= $(PREFIX)/libexec
DATADIR    ?= $(PREFIX)/share
SBINDIR    ?= $(PREFIX)/sbin
UNITDIR    ?= $(PREFIX)/lib/systemd/system
SYSUSERSDIR ?= $(PREFIX)/lib/sysusers.d
TMPFILESDIR ?= $(PREFIX)/lib/tmpfiles.d
SYSCONFDIR ?= /etc
SELINUXDIR ?= $(DATADIR)/selinux/packages/targeted
SELINUX_DEVEL ?= /usr/share/selinux/devel/Makefile
GO         ?= go
OPA        ?= opa
# The gateway's shipped policy, for testing the agents module against it.
GATEWAY_POLICY ?= $(DATADIR)/mcp-gateway/policy

LDFLAGS := -X github.com/sdrahn/cockpit-agentic/internal/launcher.Version=$(VERSION)
BINS := agent-launcher agent-admin agent-delegate agent-openshell-relay
# The functional tests' helpers (package cockpit-agentic-tests).
TESTBINS := fakemodel testsrv fakegateway

.PHONY: all build build-tests test check fmt-check vet policy-test selinux install install-selinux install-tests clean

all: build

build: $(BINS:%=bin/%)

bin/%: FORCE
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/$*

build-tests: $(TESTBINS:%=bin/tests/%)

bin/tests/%: FORCE
	$(GO) build -trimpath -o $@ ./test/vm/$*

FORCE:

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }

# The autonomy modes, tested together with the gateway's own policy
# (GATEWAY_POLICY: a checkout's policy/ directory, or the installed one).
policy-test:
	$(OPA) fmt --list --fail gateway/policy
	$(OPA) check --strict $(GATEWAY_POLICY) gateway/policy
	$(OPA) test $(GATEWAY_POLICY) gateway/policy

check: fmt-check vet test

# Needs selinux-policy-devel and mcp_gateway.if (mcp-gateway-selinux).
selinux: selinux/cockpit_agentic.pp

selinux/cockpit_agentic.pp: selinux/cockpit_agentic.te selinux/cockpit_agentic.fc
	$(MAKE) -C selinux -f $(SELINUX_DEVEL) cockpit_agentic.pp

install: build
	install -d $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic
	install -m 0755 bin/agent-launcher bin/agent-delegate bin/agent-openshell-relay $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/
	install -d $(DESTDIR)$(SBINDIR)
	install -m 0755 bin/agent-admin $(DESTDIR)$(SBINDIR)/agent-admin
	ln -sf ../../sbin/agent-admin $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/agent-admin
	install -d $(DESTDIR)$(UNITDIR)
	install -m 0644 systemd/agent@.service systemd/agent@.socket systemd/agent-schedule@.service systemd/agent-schedule@.timer \
		systemd/agent-openshell@.service systemd/agent-openshell@.socket $(DESTDIR)$(UNITDIR)/
	install -m 0755 packaging/openshell/agent-openshell-logs $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/agent-openshell-logs
	install -D -m 0644 packaging/openshell/Containerfile $(DESTDIR)$(DATADIR)/cockpit-agentic/openshell/Containerfile
	install -D -m 0644 packaging/sysusers/cockpit-agentic.conf $(DESTDIR)$(SYSUSERSDIR)/cockpit-agentic.conf
	install -D -m 0644 packaging/tmpfiles/cockpit-agentic.conf $(DESTDIR)$(TMPFILESDIR)/cockpit-agentic.conf
	install -D -m 0644 gateway/policy/agents.rego $(DESTDIR)$(DATADIR)/mcp-gateway/policy/cockpit-agentic/agents.rego
	install -D -m 0644 gateway/policy/_control.yaml $(DESTDIR)$(SYSCONFDIR)/mcp-gateway/policy/agents/_control.yaml
	install -D -m 0644 gateway/servers.d/agents.yaml $(DESTDIR)$(DATADIR)/cockpit-agentic/gateway/servers.d/agents.yaml
	install -D -m 0644 examples/patch-pilot/agent.yaml $(DESTDIR)$(DATADIR)/cockpit-agentic/examples/patch-pilot/agent.yaml
	install -D -m 0644 examples/patch-pilot/permissions.yaml $(DESTDIR)$(DATADIR)/cockpit-agentic/examples/patch-pilot/permissions.yaml
	install -d -m 0755 $(DESTDIR)$(SYSCONFDIR)/cockpit-agentic/agents

# The functional tests, for openQA (test/openqa/README.md).
install-tests: build-tests
	install -Dm0755 test/vm/run-tests.sh $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/tests/run-tests.sh
	install -Dm0755 bin/tests/fakemodel $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/tests/fakemodel
	install -Dm0755 bin/tests/testsrv $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/tests/testsrv
	install -Dm0755 bin/tests/fakegateway $(DESTDIR)$(LIBEXECDIR)/cockpit-agentic/tests/fakegateway

install-selinux: selinux
	install -d $(DESTDIR)$(SELINUXDIR)
	bzip2 -9 -c selinux/cockpit_agentic.pp >$(DESTDIR)$(SELINUXDIR)/cockpit_agentic.pp.bz2
	chmod 0644 $(DESTDIR)$(SELINUXDIR)/cockpit_agentic.pp.bz2

clean:
	rm -rf bin selinux/*.pp selinux/tmp
