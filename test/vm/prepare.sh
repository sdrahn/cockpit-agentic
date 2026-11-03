#!/bin/bash
# Prepares the CI VM for the functional tests (run-vm.sh runs it as root):
# the tests' dependencies, OPA, mcp-gateway's packages and the packages
# built from this tree, cockpit-agentic-tests among them. On a product
# system (openQA), the packages come from its repositories instead.
#
# Usage: prepare.sh <dir>, with <dir>/rpms (the packages) and <dir>/opa
# (OPA, used if the distribution has no opa package).
set -euo pipefail
dir=$1
die() {
	echo "FATAL: $*"
	exit 1
}

zypper -n --gpg-auto-import-keys ref >/tmp/zypper.log 2>&1 ||
	{ tail -n 30 /tmp/zypper.log; die "refreshing the repositories failed"; }
# policycoreutils-python-utils: the %selinux_* macros of the -selinux packages.
deps="audit policycoreutils policycoreutils-python-utils selinux-tools polkit jq curl util-linux shadow"
# shellcheck disable=SC2086 # deps is a list of package names
zypper -n in --no-recommends $deps selinux-policy-targeted >/tmp/zypper.log 2>&1 ||
	{ tail -n 30 /tmp/zypper.log; die "installing dependencies failed"; }
if zypper -n in --no-recommends opa >/dev/null 2>&1; then
	echo "  opa: distribution package"
else
	install -m 0755 "$dir/opa" /usr/bin/opa
	restorecon /usr/bin/opa
	echo "  opa: release binary ($(opa version | head -1))"
fi
# mcp-gateway first (its SELinux module defines the interfaces ours uses),
# then cockpit-agentic.
rpm -Uvh --nodeps "$dir"/rpms/mcp-gateway-[0-9]*.rpm "$dir"/rpms/mcp-gateway-selinux-*.rpm ||
	die "installing mcp-gateway failed"
rpm -Uvh "$dir"/rpms/cockpit-agentic-[0-9]*.rpm "$dir"/rpms/cockpit-agentic-selinux-*.rpm \
	"$dir"/rpms/cockpit-agentic-tests-*.rpm ||
	die "installing cockpit-agentic failed"
semodule -l | grep -qx cockpit_agentic || die "the cockpit_agentic SELinux module is not loaded"
echo "  installed: $(rpm -q mcp-gateway cockpit-agentic cockpit-agentic-selinux cockpit-agentic-tests | tr '\n' ' ')"
