// agent-launcher runs one agent on its systemd socket (CONTRACT.md Part
// II):
//
//	agent-launcher [serve] [--config DIR]       the agent (agent@<name>.service)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sdrahn/cockpit-agentic/internal/config"
	"github.com/sdrahn/cockpit-agentic/internal/journal"
	"github.com/sdrahn/cockpit-agentic/internal/launcher"
	"github.com/sdrahn/cockpit-agentic/internal/peercred"
)

// SocketDir holds the agents' sockets (CONTRACT.md §3).
const SocketDir = "/run/agents"

func main() {
	args := os.Args[1:]
	mode := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		mode, args = args[0], args[1:]
	}
	var err error
	switch mode {
	case "serve":
		err = serve(args)
	case "version":
		fmt.Println(launcher.Version)
	default:
		err = fmt.Errorf("unknown mode %q (serve, version)", mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-launcher:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("config", os.Getenv("CONFIGURATION_DIRECTORY"), "the agent's configuration directory")
	listen := fs.String("listen", "", "unix socket to listen on without socket activation (development)")
	idle := fs.Duration("idle-exit", 10*time.Minute, "exit after this long without requests or activities (0: never)")
	fs.Parse(args)
	if *dir == "" {
		return errors.New("--config DIR or $CONFIGURATION_DIRECTORY is needed")
	}
	name := filepathBase(*dir)
	log := journal.New(name, os.Stderr)
	cfg, err := config.Load(*dir)
	if err != nil {
		log.Log(journal.ConfigInvalid, "Invalid configuration: "+err.Error(), journal.Fields{"OUTCOME": "error", "REASON": err.Error()})
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	l, err := launcher.New(ctx, launcher.Options{
		Config: cfg, Log: log, StateDir: os.Getenv("STATE_DIRECTORY"), Credentials: os.Getenv("CREDENTIALS_DIRECTORY"),
	})
	if err != nil {
		log.Log(journal.ConfigInvalid, "Cannot start: "+err.Error(), journal.Fields{"OUTCOME": "error", "REASON": err.Error()})
		return err
	}
	ln, err := listener(*listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: l.Handler, ConnContext: peercred.ConnContext, ReadHeaderTimeout: 30 * time.Second}
	log.Log(journal.AgentStart, fmt.Sprintf("Agent %s %s started (contract %d)", cfg.Name, launcher.Version, config.ContractVersion), journal.Fields{"OUTCOME": "ok"})
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		lastExpire := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if *idle > 0 && l.Idle() > *idle {
					stop()
					return
				}
				if time.Since(lastExpire) > 24*time.Hour {
					l.Expire(ctx)
					lastExpire = time.Now()
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()
	err = srv.Serve(ln)
	l.Wait()
	log.Log(journal.AgentStop, "Agent "+cfg.Name+" stopped", journal.Fields{"OUTCOME": "ok"})
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// listener takes the socket systemd passed (LISTEN_FDS), or listens on path.
func listener(path string) (net.Listener, error) {
	if path != "" {
		os.Remove(path)
		return net.Listen("unix", path)
	}
	if pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID")); pid != os.Getpid() || os.Getenv("LISTEN_FDS") != "1" {
		return nil, errors.New("no socket from systemd (LISTEN_FDS); start agent@<name>.socket, or use --listen")
	}
	f := os.NewFile(3, "agent.sock")
	syscall.CloseOnExec(3)
	return net.FileListener(f)
}

func filepathBase(p string) string {
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
