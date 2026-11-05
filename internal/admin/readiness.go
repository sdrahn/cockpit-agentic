package admin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sdrahn/custos/internal/readiness"
)

// PromotionConfig holds the site's readiness thresholds (§6a); without it,
// readiness.Defaults.
const PromotionConfig = "/etc/custos/promotion.yaml"

// Thresholds reads PromotionConfig over the defaults.
func (a *Admin) Thresholds() (readiness.Thresholds, error) {
	th := readiness.Defaults
	b, err := os.ReadFile(a.p(PromotionConfig))
	if errors.Is(err, os.ErrNotExist) {
		return th, nil
	}
	if err != nil {
		return th, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&th); err != nil && err.Error() != "EOF" {
		return th, fmt.Errorf("%s: %w", PromotionConfig, err)
	}
	switch {
	case th.ObservationDays < 0, th.Proposals < 0, th.WindowDays < 1:
		return th, fmt.Errorf("%s: observation_days and proposals are 0 or more, window_days 1 or more", PromotionConfig)
	case th.OverrideRate < 0 || th.OverrideRate > 1, th.IncidentRate < 0 || th.IncidentRate > 1:
		return th, fmt.Errorf("%s: override_rate and incident_rate are fractions from 0 to 1 (0.05 is 5 %%)", PromotionConfig)
	}
	return th, nil
}

// Report is agent-admin readiness's answer (§17).
type Report struct {
	Agent      string                        `json:"agent"`
	Thresholds readiness.Thresholds          `json:"thresholds"`
	Categories map[string]readiness.Measures `json:"categories"`
	// Diagnostics is the agent's diagnostics history over the window
	// (§6b): evidence shown with the readiness, not a threshold.
	Diagnostics readiness.Diagnostics `json:"diagnostics"`
}

// Readiness measures an agent's categories (or one), from the agent's
// journal and the gateway's approval decisions over the thresholds'
// window.
func (a *Admin) Readiness(name string, only ...string) (*Report, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	d, err := a.readGateway(name)
	if err != nil {
		return nil, err
	}
	th, err := a.Thresholds()
	if err != nil {
		return nil, err
	}
	cats := readiness.Categories(d.Permissions)
	if len(only) > 0 {
		cats = only
	}
	now := a.Now()
	recs, decisions, err := a.journalRecords(name, now.AddDate(0, 0, -th.WindowDays))
	if err != nil {
		return nil, err
	}
	r := &Report{Agent: name, Thresholds: th, Categories: map[string]readiness.Measures{}, Diagnostics: readiness.DiagnosticsOf(recs)}
	for _, c := range cats {
		r.Categories[c] = readiness.Measure(c, d.Permissions, recs, decisions, th, now)
	}
	return r, nil
}

// journalRecords reads the agent's records and the gateway's decisions
// for it since then.
func (a *Admin) journalRecords(name string, since time.Time) ([]readiness.Record, map[string]readiness.Decision, error) {
	at := "@" + strconv.FormatInt(since.Unix(), 10)
	read := a.JournalJSON
	if read == nil {
		read = func(args ...string) ([]byte, error) {
			return exec.Command("journalctl", append([]string{"-o", "json", "--no-pager", "--since", at}, args...)...).Output()
		}
	}
	agentLog, err := read("AGENT_NAME="+name, "AGENT_CONTRACT=1")
	if err != nil {
		return nil, nil, fmt.Errorf("reading agent %s's journal: %w", name, err)
	}
	gwLog, err := read("-u", "mcp-gateway.service")
	if err != nil {
		return nil, nil, fmt.Errorf("reading mcp-gateway's journal: %w", err)
	}
	return readiness.ParseRecords(agentLog), readiness.ParseDecisions(gwLog, "agent-"+name), nil
}
