package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ControlSocket is mcp-gateway's control socket, where root posts notices
// to approvers (its requirement R3).
const ControlSocket = "/run/mcp-gateway/control.sock"

// Notice is a notice to the approvers of an agent's calls: mcp-gateway's
// POST /v1/notices. The gateway decides its audience as for a pending
// call of the principal on those servers (the servers' approvers and the
// agent's owners) and delivers it by mail, on its event stream and its
// approval page.
type Notice struct {
	Principal string   `json:"principal"`
	Servers   []string `json:"servers,omitempty"`
	Severity  string   `json:"severity"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
}

// errNoNotices: the gateway takes no notices (it has no POST /v1/notices,
// or no control socket).
var errNoNotices = errors.New("this mcp-gateway takes no notices")

// postNotice posts n on the gateway's control socket, as the caller (root
// for the demotion evaluator).
func postNotice(n Notice) (string, error) { return postNoticeTo(ControlSocket, n) }

func postNoticeTo(sock string, n Notice) (string, error) {
	if _, err := os.Stat(sock); err != nil {
		return "", errNoNotices
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	b, _ := json.Marshal(n)
	res, err := c.Post("http://localhost/v1/notices", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed:
		return "", errNoNotices
	case res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK:
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return "", fmt.Errorf("the gateway refused the notice (%d): %s", res.StatusCode, e.Error)
	}
	var out struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &out)
	return out.ID, nil
}

// demotionNotice is the notice of an automatic demotion of category c of
// agent name: for the servers its permissions name (globs aside, whose
// approvers the gateway cannot tell).
func demotionNotice(name, c, reason string, d *GatewayData) Notice {
	set := map[string]bool{}
	for _, p := range d.Permissions {
		s, _ := p["server"].(string)
		if cat, _ := p["category"].(string); cat == c && s != "" && !strings.ContainsAny(s, "*?[") {
			set[s] = true
		}
	}
	servers := make([]string, 0, len(set))
	for s := range set {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	text := fmt.Sprintf("The demotion evaluator set task category %s of agent %s back to supervised: %s.\n\n"+
		"Its changes wait for an approval again. Once the cause is dealt with, an administrator can promote it again "+
		"(agent-admin readiness %s %s; agent-admin promote).", c, name, reason, name, c)
	if r := []rune(text); len(text) > 4000 {
		text = string(r[:1000]) + "…" // the gateway takes at most 4000 bytes
	}
	return Notice{Principal: "agent-" + name, Servers: servers, Severity: "warning",
		Title: fmt.Sprintf("%s: %s demoted to supervised", name, c), Text: text}
}
