// agent-delegate is the "agents" MCP server mcp-gateway runs for agents
// that delegate to other agents (CONTRACT.md §9). It speaks MCP on stdio
// and runs as the delegating agent's user (run_as: principal), in its
// own SELinux domain (mcpsrv_agents_t), the only one besides Cockpit
// that may reach the agents' sockets.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sdrahn/custos/internal/delegate"
	"github.com/sdrahn/custos/internal/launcher"
)

func main() {
	if err := delegate.Serve(context.Background(), "/run/agents", launcher.Version); err != nil {
		fmt.Fprintln(os.Stderr, "agent-delegate:", err)
		os.Exit(1)
	}
}
