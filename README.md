# cockpit-agentic

Decentralized agents on a Linux host, built on Google ADK, managed from
Cockpit.

Each agent is its own systemd service (`agent@<name>.service`) running as
its own system user. The operating system and
[mcp-gateway](https://github.com/sdrahn/mcp-gateway) enforce the rules:

- systemd runs and sandboxes the agents,
- journald keeps the audit trail,
- polkit and the gateway's OPA policy decide what an agent may do,
- the gateway's broker handles approvals.

The Cockpit page gives administrators visibility and the approval UX.

See [CONTRACT.md](CONTRACT.md) for the interfaces between these parts.
