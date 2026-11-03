# SUSE's openQA tests
#
# Copyright SUSE LLC
# SPDX-License-Identifier: FSFAP
#
# Package: cockpit-agentic cockpit-agentic-tests mcp-gateway
# Summary: Functional tests of cockpit-agentic (package cockpit-agentic-tests):
#          agents under systemd governed by mcp-gateway, approvals,
#          autonomy modes, intervention, schedules, AG-UI, delegation,
#          journal records, sandbox, no SELinux denials. Each check
#          becomes a result of its own (JUnit).
# Maintainer: cockpit-agentic maintainers

use Mojo::Base 'consoletest';
use testapi;
use serial_terminal 'select_serial_terminal';
use utils qw(zypper_call);

my $results = '/var/tmp/cockpit-agentic-tests';

sub run {
    select_serial_terminal;
    # The tests check the SELinux domains when SELinux is enforcing, as the
    # product ships it, and skip those checks otherwise.
    zypper_call('in cockpit-agentic-tests cockpit-agentic-selinux mcp-gateway-selinux');
    assert_script_run("mkdir -p $results");
    # Exit status 0: all passed; 1: a check failed (the JUnit results say
    # which); 2: wrong usage.
    my $rc = script_run("/usr/libexec/cockpit-agentic/tests/run-tests.sh --disposable --results $results "
          . "> $results/run.log 2>&1", timeout => 1800);
    upload_logs("$results/run.log");
    die "run-tests.sh did not run (exit status $rc)" if $rc == 2 || script_run("test -s $results/results.xml") != 0;
    upload_logs("$results/results.tap");
    parse_extra_log(XUnit => "$results/results.xml");
}

sub post_fail_hook {
    my ($self) = @_;
    select_serial_terminal;
    script_run("journalctl --no-pager -o short-precise -u mcp-gateway -u mcp-opa -u 'agent@*' -u 'agent-schedule@*' "
          . "> $results/journal.txt 2>&1");
    upload_logs("$results/journal.txt", failok => 1);
    script_run("journalctl --no-pager -o json AGENT_CONTRACT=1 > $results/agent-records.json 2>&1");
    upload_logs("$results/agent-records.json", failok => 1);
    script_run("ausearch -m AVC,USER_AVC,SELINUX_ERR -ts boot > $results/avc.txt 2>&1");
    upload_logs("$results/avc.txt", failok => 1);
    $self->SUPER::post_fail_hook;
}

sub test_flags {
    return {fatal => 0};
}

1;
