// agent-launcher runs one agent on its systemd socket (CONTRACT.md Part
// II):
//
//	agent-launcher [serve] [--config DIR]       the agent (agent@<name>.service)
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	configEnv := fs.String("config-env", "", "read agent.yaml (base64) from this environment variable instead (OpenShell sandboxes)")
	listen := fs.String("listen", "", "unix socket to listen on without socket activation (development)")
	listenTCP := fs.String("listen-tcp", "", "loopback TCP address to listen on in a sandbox; callers come from the host relay ($"+relaySecretEnv+")")
	idle := fs.Duration("idle-exit", 10*time.Minute, "exit after this long without requests or activities (0: never)")
	fs.Parse(args)
	if *configEnv != "" {
		d, err := configFromEnv(*configEnv)
		if err != nil {
			return err
		}
		*dir = d
	}
	if *dir == "" {
		return errors.New("--config DIR, --config-env VAR or $CONFIGURATION_DIRECTORY is needed")
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
	var ln net.Listener
	srv := &http.Server{Handler: l.Handler, ConnContext: peercred.ConnContext, ReadHeaderTimeout: 30 * time.Second}
	if *listenTCP != "" {
		host, _, err := net.SplitHostPort(*listenTCP)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("--listen-tcp %s: want a loopback address", *listenTCP)
		}
		secret := os.Getenv(relaySecretEnv)
		if len(secret) < 32 {
			return fmt.Errorf("--listen-tcp needs $%s (at least 32 characters) shared with the host relay", relaySecretEnv)
		}
		os.Unsetenv(relaySecretEnv)
		srv.Handler, srv.ConnContext = peercred.Relayed(secret, l.Handler), nil
		ln, err = net.Listen("tcp", *listenTCP)
	} else {
		ln, err = listener(*listen)
	}
	if err != nil {
		return err
	}
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

const relaySecretEnv = "AGENTIC_RELAY_SECRET"

// configFromEnv writes the base64 agent.yaml in $name to a directory
// named after the agent and returns it.
func configFromEnv(name string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(os.Getenv(name))
	if err != nil || len(b) == 0 {
		return "", fmt.Errorf("$%s: want agent.yaml, base64-encoded", name)
	}
	c, err := config.Parse(b)
	if err != nil {
		return "", err
	}
	root, err := os.MkdirTemp("", "agent-config")
	if err != nil {
		return "", err
	}
	d := filepath.Join(root, c.Name)
	if err := os.Mkdir(d, 0o700); err != nil {
		return "", err
	}
	return d, os.WriteFile(filepath.Join(d, "agent.yaml"), b, 0o600)
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
