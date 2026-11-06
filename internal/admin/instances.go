package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// errNoControl: the gateway's control socket is not there (the gateway
// does not run).
var errNoControl = errors.New("mcp-gateway's control socket is not there")

// stopInstances stops the running instances of a server through the
// gateway's control API (GET /v1/servers, DELETE /v1/instances/{id}), as
// Cockpit's Servers tab does: the exec server reads its commands when an
// instance starts, so new commands are offered only by a new instance.
// It returns how many it stopped.
func stopInstances(server string) (int, error) { return stopInstancesOn(ControlSocket, server) }

func stopInstancesOn(sock, server string) (int, error) {
	if _, err := os.Stat(sock); err != nil {
		return 0, errNoControl
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	res, err := c.Get("http://localhost/v1/servers")
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return 0, fmt.Errorf("the gateway's servers (%d): %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var servers []struct {
		Name      string `json:"name"`
		Instances []struct {
			ID string `json:"id"`
		} `json:"instances"`
	}
	if err := json.NewDecoder(res.Body).Decode(&servers); err != nil {
		return 0, fmt.Errorf("the gateway's servers: %w", err)
	}
	n := 0
	var errs []string
	for _, s := range servers {
		if s.Name != server {
			continue
		}
		for _, in := range s.Instances {
			req, _ := http.NewRequest(http.MethodDelete, "http://localhost/v1/instances/"+in.ID, nil)
			r, err := c.Do(req)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			r.Body.Close()
			if r.StatusCode != http.StatusNoContent && r.StatusCode != http.StatusNotFound {
				errs = append(errs, fmt.Sprintf("instance %s: %d", in.ID, r.StatusCode))
				continue
			}
			n++
		}
	}
	if len(errs) > 0 {
		return n, errors.New(strings.Join(errs, "; "))
	}
	return n, nil
}
