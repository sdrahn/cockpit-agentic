// agent-admin: administrative actions on agents, run as root (by
// Cockpit with administrative access, or by hand). CONTRACT.md §11.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sdrahn/custos/internal/admin"
)

const usage = `usage: agent-admin COMMAND …
  list                                   the agents
  show [NAME…]                           the gateway data and the emergency stop, as JSON
  check NAME|FILE                        validate a configuration
  create NAME --from FILE                install a new agent (supervised, no permissions)
  edit NAME --from FILE                  replace its configuration and apply it (restart; OpenShell: its sandbox)
  delete NAME [--keep-state]             remove an agent (its user stays)
  set-credential NAME CRED               encrypt a secret read from stdin
  mode NAME advisory|supervised|bounded  set the autonomy mode (enforced by mcp-gateway)
  permissions NAME                       replace its gateway permissions (YAML list on stdin)
  pause NAME | resume NAME               hold an agent / let it go on
  trigger NAME_SCHEDULE                  start a schedule's activity (agent-schedule@.service)
  stop-all                               emergency stop: every agent, every schedule
  release-all                            undo stop-all`

func main() {
	a := admin.New()
	if err := run(a, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent-admin:", err)
		os.Exit(1)
	}
}

func by() string {
	if u := os.Getenv("SUDO_USER"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

func run(a *admin.Admin, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	cmd, args := args[0], args[1:]
	need := func(n int) error {
		if len(args) < n {
			return fmt.Errorf("%s: missing arguments\n%s", cmd, usage)
		}
		return nil
	}
	from := func() (string, error) {
		for i, x := range args {
			if x == "--from" && i+1 < len(args) {
				return args[i+1], nil
			}
		}
		return "", fmt.Errorf("%s: --from FILE is needed", cmd)
	}
	switch cmd {
	case "list":
		names, err := a.List()
		if err == nil && len(names) > 0 {
			fmt.Println(strings.Join(names, "\n"))
		}
		return err
	case "show":
		p, err := a.Show(args...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case "check":
		if err := need(1); err != nil {
			return err
		}
		return a.Check(args[0])
	case "create", "edit":
		if err := need(1); err != nil {
			return err
		}
		f, err := from()
		if err != nil {
			return err
		}
		if cmd == "create" {
			return a.Create(args[0], f)
		}
		return a.Edit(args[0], f)
	case "delete":
		if err := need(1); err != nil {
			return err
		}
		return a.Delete(args[0], len(args) > 1 && args[1] == "--keep-state")
	case "set-credential":
		if err := need(2); err != nil {
			return err
		}
		return a.SetCredential(args[0], args[1], os.Stdin)
	case "mode":
		if err := need(2); err != nil {
			return err
		}
		return a.SetMode(args[0], args[1])
	case "permissions":
		if err := need(1); err != nil {
			return err
		}
		return a.SetPermissions(args[0], os.Stdin)
	case "pause", "resume":
		if err := need(1); err != nil {
			return err
		}
		return a.Pause(args[0], by(), cmd == "pause")
	case "trigger":
		if err := need(1); err != nil {
			return err
		}
		return a.Trigger(args[0])
	case "stop-all":
		return a.StopAll(by())
	case "release-all":
		return a.ReleaseAll(by())
	case "-h", "--help", "help":
		fmt.Println(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, usage)
}
