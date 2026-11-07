// custos-second-host is a second agent host for the Cockpit control plane
// (CONTRACT.md Part I; #112, O8): a mock that serves the control API (§5)
// on /run/agents/<name>/api.sock and writes the audit records (§10), with
// no model or gateway behind it. It uses the standard library only, no
// Custos code, so that the control plane's use of it rests on the
// contract alone. The functional tests run it as a transient unit:
//
//	systemd-run --unit=custos-second-host-mock.service \
//	    /usr/libexec/custos/tests/secondhost -name mock -service custos-second-host-mock.service
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/sdrahn/custos/test/vm/secondhost/host"
)

func main() {
	name := flag.String("name", "", "the agent's name")
	dir := flag.String("dir", "/run/agents", "where agents' sockets are (§3)")
	group := flag.String("group", "agent-clients", "the socket's group (§3)")
	service := flag.String("service", "", "the systemd unit that runs it, for lifecycle (§3a); default custos-second-host@<name>.service")
	flag.Parse()
	if *name == "" {
		log.Fatal("custos-second-host: -name is missing")
	}
	if *service == "" {
		*service = "custos-second-host@" + *name + ".service"
	}
	h := host.New(*name, *service, journalLogger(*name))

	sockDir := filepath.Join(*dir, *name)
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		log.Fatal(err)
	}
	sock := filepath.Join(sockDir, "api.sock")
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal(err)
	}
	// root:agent-clients 0660 (§3).
	if g, err := user.LookupGroup(*group); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		os.Chown(sock, 0, gid)
	}
	os.Chmod(sock, 0o660)

	srv := &http.Server{
		Handler: h.Handler(),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if cr, err := peer(c); err == nil {
				return host.WithCaller(ctx, cr)
			}
			return ctx
		},
	}
	h.Log("agent.start", "Agent "+*name+" started (second host)", nil)
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		h.Log("agent.stop", "Agent "+*name+" stopped", nil)
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	os.Remove(sock)
}

// peer is the caller by SO_PEERCRED (§5).
func peer(c net.Conn) (host.Caller, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return host.Caller{}, fmt.Errorf("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return host.Caller{}, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return host.Caller{}, err
	}
	if serr != nil {
		return host.Caller{}, serr
	}
	name := strconv.Itoa(int(cred.Uid))
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	return host.Caller{UID: int(cred.Uid), Name: name}, nil
}

// messageIDs are §10's.
var messageIDs = map[string]string{
	"agent.start": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a601", "agent.stop": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a602",
	"agent.paused": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a604", "agent.resumed": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a605",
	"invocation.start": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a610", "invocation.end": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a611",
	"activity.cancel": "6f3c0e1a9b2d4c58a7e1f2d3c4b5a612",
}

// journalLogger writes §10's records with journald's native protocol, or,
// without a journal, JSON lines on stderr with lower-case keys and
// without the AGENT_ prefix.
func journalLogger(name string) host.Logger {
	const socket = "/run/systemd/journal/socket"
	return func(event, message string, f map[string]string) {
		fields := map[string]string{"CONTRACT": "1", "NAME": name, "EVENT": event}
		for k, v := range f {
			if v != "" {
				fields[k] = v
			}
		}
		if c, err := net.Dial("unixgram", socket); err == nil {
			defer c.Close()
			var b bytes.Buffer
			line := func(k, v string) { b.WriteString(k + "=" + strings.ReplaceAll(v, "\n", " ") + "\n") }
			line("MESSAGE", message)
			line("MESSAGE_ID", messageIDs[event])
			line("PRIORITY", "6")
			line("SYSLOG_IDENTIFIER", "agent-"+name)
			for k, v := range fields {
				line("AGENT_"+k, v)
			}
			if _, err := c.Write(b.Bytes()); err == nil {
				return
			}
		}
		out := map[string]string{"message": message}
		for k, v := range fields {
			out[strings.ToLower(k)] = v
		}
		json.NewEncoder(os.Stderr).Encode(out)
	}
}
