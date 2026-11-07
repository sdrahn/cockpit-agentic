package admin

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/sdrahn/custos/internal/config"
	"github.com/sdrahn/custos/internal/openshell"
)

// The name an OpenShell agent reaches mcp-gateway by (CONTRACT.md §16a,
// #131). OpenShell's proxy, in the host's network, terminates the agent's
// TLS and connects to the gateway by the name in gateway.url, checking the
// gateway's certificate for it. The host's own name, which an official
// certificate can carry, is the default; host.openshell.internal (what a
// loopback URL becomes) needs a certificate of a private CA. The proxy
// never connects to a loopback or link-local address of a name that is
// not the alias.

// gatewayHostNotes says what keeps an OpenShell agent from reaching the
// gateway by the name in its gateway.url, or a certificate from covering it.
func (a *Admin) gatewayHostNotes(cfg *config.Config) []string {
	if cfg.Sandbox.Kind != "openshell" || cfg.Gateway.URL == "" {
		return nil
	}
	u, err := url.Parse(cfg.Gateway.URL)
	if err != nil {
		return nil
	}
	var notes []string
	h := u.Hostname()
	ip := net.ParseIP(h)
	switch {
	case h == "localhost" || h == openshell.HostAlias || (ip != nil && ip.IsLoopback()):
		notes = append(notes, fmt.Sprintf("gateway.url %s: the sandbox reaches the gateway as %s, a name only a private CA's certificate can carry; "+
			"with the gateway's own host name an official certificate does (user guide, chapter 11, step 3)", cfg.Gateway.URL, openshell.HostAlias))
	case ip != nil:
		notes = append(notes, fmt.Sprintf("gateway.url %s: an IP address; the gateway's certificate must name it (an official one names the host)", cfg.Gateway.URL))
	default:
		lookup := a.LookupHost
		if lookup == nil {
			lookup = net.LookupHost
		}
		addrs, err := lookup(h)
		if err != nil {
			notes = append(notes, fmt.Sprintf("gateway.url %s: %s does not resolve on this host (%v), and OpenShell's proxy resolves it here", cfg.Gateway.URL, h, err))
			break
		}
		for _, s := range addrs {
			if x := net.ParseIP(s); x != nil && (x.IsLoopback() || x.IsLinkLocalUnicast() || x.IsUnspecified()) {
				notes = append(notes, fmt.Sprintf("gateway.url %s: %s resolves to %s on this host, which OpenShell's proxy never connects to; "+
					"make it resolve to the host's network address (/etc/hosts or DNS)", cfg.Gateway.URL, h, s))
				break
			}
		}
	}
	if gh, err := a.gatewayHTTP(); err == nil && strings.TrimRight(gh.Audience, "/") != strings.TrimRight(cfg.Gateway.URL, "/") {
		notes = append(notes, fmt.Sprintf("gateway.url %s is not the gateway's http.audience %s: the agent's tokens carry the audience, "+
			"and the gateway's certificate must name the URL's host", cfg.Gateway.URL, gh.Audience))
	}
	return notes
}

// noteGatewayHost prints the notes, which do not stop anything.
func (a *Admin) noteGatewayHost(cfg *config.Config) {
	for _, n := range a.gatewayHostNotes(cfg) {
		fmt.Fprintf(a.Out, "agent %s: note: %s\n", cfg.Name, n)
	}
}
