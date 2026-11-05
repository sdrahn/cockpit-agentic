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
  check NAME|FILE                        validate a configuration (FILE -: stdin)
  create NAME --from FILE                install a new agent (supervised, no permissions; FILE -: stdin)
  create NAME --template TEMPLATE [--from FILE]
                                         install a new agent from a template: its configuration (or FILE's),
                                         permissions and mode
  templates                              the agent templates (CONTRACT.md §14a), as JSON
  draft NAME [--template TEMPLATE]       a new agent's starting configuration (blank or the template's),
                                         with the host's model defaults
  defaults [--from FILE | --clear]       the model defaults for new agents (CONTRACT.md §14b), as JSON;
                                         --from sets them (FILE -: stdin), --clear removes them
  edit NAME --from FILE                  replace its configuration and apply it (restart; OpenShell: its sandbox; FILE -: stdin)
  delete NAME [--keep-state]             remove an agent (its user stays)
  set-credential NAME CRED               encrypt a secret read from stdin
  credentials [NAME…]                    the agents' credentials: names, what uses them, missing ones; never
                                         the values (JSON)
  remove-credential NAME CRED [--force]  remove a stored credential (--force: one its model uses)
  enable CATEGORY… | --all               enable task categories on a diagnostics-only host (CONTRACT.md §6b);
                                         they start supervised
  diagnostics-only                       make a fresh host diagnostics-only (the bundled install, custos-setup)
  mode NAME advisory|supervised|bounded  set the autonomy mode (enforced by mcp-gateway)
  permissions NAME                       replace its gateway permissions (YAML list on stdin)
  categories                             the task categories (CONTRACT.md §6a), as JSON
  readiness NAME [CATEGORY]              a category's promotion readiness (CONTRACT.md §6a), as JSON
  promote NAME CATEGORY --envelope FILE [--despite-readiness --reason TEXT]
                                         let a category act without approval within its envelope (FILE -: stdin)
  demote NAME CATEGORY [--reason TEXT]   back to supervised (the envelope is kept)
  review NAME [CATEGORY]                 the autonomous actions of promoted categories still to review, as JSON
  review NAME CATEGORY ACTIVITY CALL ok|incident [--reason TEXT]
                                         an approver's verdict; incident (should not have run) demotes it
  evaluate                               the demotion evaluator: demote bounded categories whose override rate
                                         or rollback conditions say so (custos-demote.timer)
  pause NAME | resume NAME               hold an agent / let it go on
  trigger NAME_SCHEDULE                  start a schedule's activity (agent-schedule@.service)
  stop-all                               emergency stop: every agent, every schedule
  release-all                            undo stop-all`

func main() {
	a := admin.New()
	a.By = by()
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
	flag := func(name string) (string, bool) {
		for i, x := range args {
			if x == name && i+1 < len(args) {
				return args[i+1], true
			}
		}
		return "", false
	}
	has := func(name string) bool {
		for _, x := range args {
			if x == name {
				return true
			}
		}
		return false
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
		if t, ok := flag("--template"); ok && cmd == "create" {
			f, _ := flag("--from")
			return a.CreateFromTemplate(args[0], t, f)
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
	case "credentials":
		c, err := a.Credentials(args...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	case "remove-credential":
		if err := need(2); err != nil {
			return err
		}
		return a.RemoveCredential(args[0], args[1], has("--force"))
	case "enable":
		var cats []string
		for _, x := range args {
			if x != "--all" {
				cats = append(cats, x)
			}
		}
		return a.Enable(by(), cats, has("--all"))
	case "diagnostics-only":
		return a.InitDiagnosticsOnly(by())
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
	case "categories":
		c, err := a.Categories()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	case "draft":
		if err := need(1); err != nil {
			return err
		}
		t, _ := flag("--template")
		b, err := a.Draft(args[0], t)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "defaults":
		if f, ok := flag("--from"); ok {
			b, err := a.ReadFrom(f)
			if err != nil {
				return err
			}
			return a.SetDefaults(b)
		}
		if has("--clear") {
			return a.SetDefaults(nil)
		}
		d, err := a.GetDefaults()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	case "templates":
		t, err := a.ListTemplates()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(t)
	case "readiness":
		if err := need(1); err != nil {
			return err
		}
		r, err := a.Readiness(args[0], args[1:]...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "promote":
		if err := need(2); err != nil {
			return err
		}
		f, ok := flag("--envelope")
		if !ok {
			return fmt.Errorf("promote: --envelope FILE is needed")
		}
		reason, _ := flag("--reason")
		b, err := a.ReadFrom(f)
		if err != nil {
			return err
		}
		return a.Promote(args[0], args[1], by(), admin.Promotion{Envelope: b, Reason: reason, DespiteReadiness: has("--despite-readiness")})
	case "demote":
		if err := need(2); err != nil {
			return err
		}
		reason, _ := flag("--reason")
		return a.Demote(args[0], args[1], by(), reason)
	case "review":
		if err := need(1); err != nil {
			return err
		}
		if len(args) >= 5 && !strings.HasPrefix(args[2], "--") {
			reason, _ := flag("--reason")
			return a.Review(args[0], args[1], args[2], args[3], args[4], by(), reason)
		}
		q, err := a.ReviewQueue(args[0], args[1:]...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(q)
	case "evaluate":
		d, err := a.Evaluate()
		for _, x := range d {
			fmt.Printf("agent %s: category %s demoted: %s\n", x.Agent, x.Category, x.Reason)
		}
		return err
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
