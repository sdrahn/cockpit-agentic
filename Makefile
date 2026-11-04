# custos: build, test and install (also used by the OBS spec).
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

LDFLAGS := -X github.com/sdrahn/custos/internal/launcher.Version=$(VERSION)
BINS := agent-launcher agent-admin agent-delegate agent-openshell-relay
# The functional tests' helpers (package custos-tests).
TESTBINS := fakemodel testsrv fakegateway testidp

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
selinux: selinux/custos.pp

selinux/custos.pp: selinux/custos.te selinux/custos.fc
	$(MAKE) -C selinux -f $(SELINUX_DEVEL) custos.pp

install: build
	install -d $(DESTDIR)$(LIBEXECDIR)/custos
	install -m 0755 bin/agent-launcher bin/agent-delegate bin/agent-openshell-relay $(DESTDIR)$(LIBEXECDIR)/custos/
	install -d $(DESTDIR)$(SBINDIR)
	install -m 0755 bin/agent-admin $(DESTDIR)$(SBINDIR)/agent-admin
	ln -sf ../../sbin/agent-admin $(DESTDIR)$(LIBEXECDIR)/custos/agent-admin
	install -d $(DESTDIR)$(UNITDIR)
	install -m 0644 systemd/agent@.service systemd/agent@.socket systemd/agent-schedule@.service systemd/agent-schedule@.timer \
		systemd/agent-openshell@.service systemd/agent-openshell@.socket $(DESTDIR)$(UNITDIR)/
	install -m 0755 packaging/openshell/agent-openshell-logs $(DESTDIR)$(LIBEXECDIR)/custos/agent-openshell-logs
	install -D -m 0644 packaging/openshell/Containerfile $(DESTDIR)$(DATADIR)/custos/openshell/Containerfile
	install -D -m 0644 packaging/sysusers/custos.conf $(DESTDIR)$(SYSUSERSDIR)/custos.conf
	install -D -m 0644 packaging/tmpfiles/custos.conf $(DESTDIR)$(TMPFILESDIR)/custos.conf
	install -D -m 0644 gateway/policy/agents.rego $(DESTDIR)$(DATADIR)/mcp-gateway/policy/custos/agents.rego
	install -D -m 0644 gateway/policy/_control.yaml $(DESTDIR)$(SYSCONFDIR)/mcp-gateway/policy/agents/_control.yaml
	install -D -m 0644 gateway/policy/_http.yaml $(DESTDIR)$(SYSCONFDIR)/mcp-gateway/policy/agents/_http.yaml
	install -D -m 0644 gateway/servers.d/agents.yaml $(DESTDIR)$(DATADIR)/custos/gateway/servers.d/agents.yaml
	install -D -m 0644 examples/patch-pilot/agent.yaml $(DESTDIR)$(DATADIR)/custos/examples/patch-pilot/agent.yaml
	install -D -m 0644 examples/patch-pilot/permissions.yaml $(DESTDIR)$(DATADIR)/custos/examples/patch-pilot/permissions.yaml
	install -d -m 0755 $(DESTDIR)$(SYSCONFDIR)/custos/agents

# The functional tests, for openQA (test/openqa/README.md).
install-tests: build-tests
	install -Dm0755 test/vm/run-tests.sh $(DESTDIR)$(LIBEXECDIR)/custos/tests/run-tests.sh
	install -Dm0755 bin/tests/fakemodel $(DESTDIR)$(LIBEXECDIR)/custos/tests/fakemodel
	install -Dm0755 bin/tests/testsrv $(DESTDIR)$(LIBEXECDIR)/custos/tests/testsrv
	install -Dm0755 bin/tests/fakegateway $(DESTDIR)$(LIBEXECDIR)/custos/tests/fakegateway
	install -Dm0755 bin/tests/testidp $(DESTDIR)$(LIBEXECDIR)/custos/tests/testidp

install-selinux: selinux
	install -d $(DESTDIR)$(SELINUXDIR)
	bzip2 -9 -c selinux/custos.pp >$(DESTDIR)$(SELINUXDIR)/custos.pp.bz2
	chmod 0644 $(DESTDIR)$(SELINUXDIR)/custos.pp.bz2

clean:
	rm -rf bin selinux/*.pp selinux/tmp
