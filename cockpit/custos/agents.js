/* Agents tab: the agents on this host, and one agent's posture and
 * controls: its autonomy mode and permissions as the gateway enforces
 * them (§6), its schedules, pause and stop (§8). #/agents/<name> selects
 * one. Everything that changes something goes through the root bridge
 * (agent-admin, systemctl); reading goes as the person.
 */
"use strict";

function selectedAgent() {
    const p = cockpit.location.path;
    return p[0] === "agents" && p[1] ? p[1] : null;
}

/* agentState is [text, state for the badge] of an agent. */
function agentState(a) {
    const data = state.posture && state.posture.agents[a.name];
    if (a.stopped) return ["stopped", "failed"];
    if (!a.status) return ["unreachable", "error"];
    if (a.status.paused || (data && data.paused)) return ["paused", "paused"];
    return a.status.running > 0 ? [a.status.running + " running", "running"] : ["idle (socket listening)", "finished"];
}

function modeBadge(mode) {
    return el("span", { class: "badge" + (mode === "bounded" ? " badge-waiting" : mode === "supervised" ? " badge-running" : "") },
              mode || "no gateway data");
}

/* unitsOf are the units that stop and start the agent (§3a lifecycle):
 * its socket and service, so that a stopped agent stays stopped (a
 * request to a listening socket would start it again), and its socket to
 * start it, which starts the service on the next request. For a stopped
 * agent, which has no status, the lifecycle refreshState found. */
function unitsOf(a) {
    const l = (a.status && a.status.lifecycle) || a.lifecycle || {};
    const openshell = l.kind === "openshell";
    const service = l.service || (openshell ? "agent-openshell@" : "agent@") + a.name + ".service";
    const socket = l.socket || (openshell ? "agent-openshell@" : "agent@") + a.name + ".socket";
    return { stop: [socket, service], start: [socket], service };
}

/* act runs one administrative action from a button, then refreshes. */
async function act(button, what, fn) {
    if (button) button.disabled = true;
    const note = document.getElementById("agent-action");
    if (note) note.textContent = what + "…";
    try {
        await fn();
        if (note) note.textContent = "";
        showError(null);
    } catch (ex) {
        if (note) note.textContent = "";
        showError(what + " failed: " + ((ex && ex.message) || ex));
    }
    tabs.agents.refresh();
}

/* timersOf are the schedule timers' next runs and whether they are
 * enabled, read as the person. */
async function timersOf(a) {
    const scheds = (a.status && a.status.schedules) || [];
    if (scheds.length === 0) return {};
    const units = scheds.map(s => "agent-schedule@" + a.name + "_" + s.name + ".timer");
    // One unit per call: is-enabled fails when any unit it names is not.
    const enabled = await Promise.all(units.map(u => cockpit.spawn(["systemctl", "is-enabled", u], { err: "ignore" })
        .then(out => String(out).trim(), () => "disabled")));
    const next = await nextTimers();
    const out = {};
    scheds.forEach((s, i) => {
        const t = next.find(t => t.agent === a.name && t.schedule === s.name);
        out[s.name] = { unit: units[i], enabled: (enabled[i] || "").trim() === "enabled", next: t && t.next };
    });
    return out;
}

function renderAgentList() {
    const body = document.querySelector("#agents tbody");
    const sel = selectedAgent();
    body.replaceChildren();
    if (state.agents.length === 0) {
        body.append(el("tr", null, el("td", { colspan: "6", class: "muted" },
            "No agents on this host. Administrators create them with agent-admin create NAME --from agent.yaml.")));
        return;
    }
    for (const a of state.agents) {
        const data = state.posture && state.posture.agents[a.name];
        const [text, st] = agentState(a);
        const scheds = (a.status && a.status.schedules) || [];
        const now = state.activities.filter(x => x.agent === a.name && isOpen(x))[0];
        body.append(el("tr", { class: sel === a.name ? "current" : null, id: "agent-row-" + a.name },
            el("td", null, el("button", { class: "link", onclick: () => cockpit.location.go(["agents", a.name]) }, a.name)),
            el("td", null, stateBadgeText(text, st), missingCredentials(a.name).length
                ? [" ", el("span", { class: "badge badge-bad", title: "Missing: " + missingCredentials(a.name).join(", ") }, "credential missing")] : null),
            el("td", null, modeBadge(data && data.mode)),
            el("td", null, a.status ? [a.status.implementation, el("div", { class: "muted" }, a.status.model)] : "–"),
            el("td", { class: "muted" }, scheds.length ? scheds.map(s => s.name).join(", ") : "none"),
            el("td", { class: "muted" }, now ? now.summary || shortId(now.id) : a.error || "")));
    }
}

function stateBadgeText(text, st) {
    return el("span", { class: "badge " + (STATE_CLASS[st] || "") }, text);
}

async function renderAgentDetail() {
    const box = document.getElementById("agent-detail");
    const name = selectedAgent();
    // The note about an agent just created, while it is shown.
    document.getElementById("agent-created").hidden = !name || justCreated !== name;
    const a = name && state.agents.find(x => x.name === name);
    box.hidden = !name;
    if (!name) return;
    if (!a) {
        box.replaceChildren(el("p", { class: "card muted" }, "There is no agent " + name + " on this host."));
        return;
    }
    const data = state.posture && state.posture.agents[a.name];
    const control = state.posture && state.posture.control;
    const [text, st] = agentState(a);
    const paused = st === "paused";
    const units = unitsOf(a);

    const pause = el("button", { class: "secondary" }, paused ? "Resume" : "Pause");
    pause.addEventListener("click", () => act(pause, (paused ? "Resuming " : "Pausing ") + a.name,
        () => admin([paused ? "resume" : "pause", a.name])));
    const stop = el("button", { class: a.stopped ? "secondary" : "danger" }, a.stopped ? "Start" : "Stop");
    stop.addEventListener("click", () => act(stop, (a.stopped ? "Starting " : "Stopping ") + a.name,
        () => systemctl(a.stopped ? "start" : "stop", ...(a.stopped ? units.start : units.stop))));

    // Restart: the agent's service (an OpenShell agent's relay, with its
    // sandbox). Its running activities end, as with agent-admin edit.
    const restart = el("button", { class: "secondary" }, "Restart");
    restart.hidden = !!a.stopped;
    restart.addEventListener("click", () => act(restart, "Restarting " + a.name, () => systemctl("restart", units.service)));
    // Logs: Cockpit's journal for the agent's unit, which holds the unit's
    // messages and the agent's own records (agent-<name>, §10).
    const logs = el("button", { class: "secondary" }, "Logs");
    logs.addEventListener("click", () => cockpit.jump("/system/logs#/?priority=info&service=" + encodeURIComponent(units.service)));

    const del = deleteControl(a);
    const head = el("section", { class: "card" },
        el("div", { class: "toolbar" },
           el("div", null,
              el("div", { class: "head-line" }, el("h2", null, a.name), stateBadgeText(text, st), modeBadge(data && data.mode)),
              el("div", { class: "mono muted" }, [units.stop.join(" · "),
                  a.status ? a.status.implementation + " · contract " + a.status.contract : null,
                  a.status && a.status.model ? "model " + a.status.model : null].filter(Boolean).join(" · "))),
           el("span", { class: "actions" }, el("span", { id: "agent-action", class: "muted" }), pause, restart, stop, logs, del.open)),
        data && data.paused && data.paused_by ? el("p", { class: "muted" }, "Paused by " + data.paused_by + ".") : null,
        del.box);

    const mode = el("section", { class: "card" }, el("h2", null, "Autonomy mode"));
    if (data) {
        const group = el("div", { class: "modes", role: "radiogroup", "aria-label": "Autonomy mode" });
        for (const m of ["advisory", "supervised", "bounded"]) {
            const b = el("button", { class: "mode", role: "radio", "aria-checked": String(data.mode === m), "data-mode": m },
                         el("strong", null, m), el("div", { class: "muted" }, MODE_TEXT[m]));
            if (data.mode !== m) b.addEventListener("click", () => act(b, "Setting " + a.name + " to " + m, () => admin(["mode", a.name, m])));
            group.append(b);
        }
        mode.append(group, el("p", { class: "muted" }, "Enforced by mcp-gateway from /etc/mcp-gateway/policy/agents/" +
            a.name + ".yaml. Tool annotations from MCP servers never allow anything."));
    } else {
        mode.append(el("p", { class: "muted" }, "The gateway has no data for this agent: it denies it everything."));
    }

    const bounds = el("section", { class: "card" }, el("h2", null, "Tool boundaries"),
        el("p", { class: "muted" }, "What the gateway decides for agent-" + a.name + " in the current mode. Unlisted tools are denied."));
    if (data) {
        const perms = data.permissions || [];
        const table = el("table", { id: "boundaries" }, el("thead", null, el("tr", null,
            el("th", null, "Tool"), el("th", null, "Declared as"), el("th", null, "Arguments"), el("th", null, "Effect now"))));
        const tbody = el("tbody");
        if (perms.length === 0) tbody.append(el("tr", null, el("td", { colspan: "4", class: "muted" }, "No permissions: the agent can do nothing.")));
        for (const p of perms) {
            const [eff, cls] = permEffect(p, data, control);
            tbody.append(el("tr", null,
                el("td", { class: "mono" }, p.server + "__" + (p.tool || p.resource || p.prompt || "*")),
                el("td", null, permKind(p)),
                el("td", { class: "mono muted" }, p.args ? Object.entries(p.args).map(([k, v]) => k + " ~ " + v).join(", ") : "any"),
                el("td", null, el("span", { class: "badge " + cls }, eff))));
        }
        table.append(tbody);
        const yaml = el("textarea", { id: "permissions-yaml", rows: String(Math.max(4, perms.length + 2)), spellcheck: "false",
                                      "aria-label": "Permissions (YAML list)" });
        yaml.value = perms.map(p => "- " + JSON.stringify(p)).join("\n") + "\n";
        const save = el("button", null, "Save permissions");
        save.addEventListener("click", () => act(save, "Saving the permissions of " + a.name,
            () => admin(["permissions", a.name], yaml.value)));
        bounds.append(table, el("details", null, el("summary", null, "Edit the permissions"),
            el("p", { class: "muted" }, "A YAML list of gateway permissions, with read_only: true for tools that only read and " +
               "autonomous: true for changes bounded mode may make without asking (§6), or category: NAME for a task category, which " +
               "agent-admin promote lets act without asking within its windows (§6a). agent-admin checks it, and returns " +
               "once the gateway enforces it."),
            yaml, el("div", { class: "actions" }, save)));
    }

    const scheds = (a.status && a.status.schedules) || [];
    const timers = await timersOf(a);
    const schedules = el("section", { class: "card" }, el("h2", null, "Schedules"),
        el("p", { class: "muted" }, "systemd timers agent-schedule@" + a.name + "_*.timer. Their runs show up under Activity with origin schedule."));
    if (scheds.length === 0) schedules.append(el("p", { class: "muted" }, a.status ? "No schedules." : "Unknown while the agent is not reachable."));
    for (const s of scheds) {
        const t = timers[s.name] || {};
        const toggle = el("button", { class: "secondary" }, t.enabled ? "Disable" : "Enable");
        toggle.addEventListener("click", () => act(toggle, (t.enabled ? "Disabling " : "Enabling ") + s.name,
            () => systemctl(t.enabled ? "disable" : "enable", "--now", t.unit)));
        schedules.append(el("div", { class: "call" }, el("div", { class: "head" },
            el("strong", null, s.name), el("span", { class: "mono muted" }, s.on_calendar),
            el("span", { class: "muted", style: "margin-left:auto" }, t.enabled ? (t.next ? "next " + when(t.next) : "") : "disabled"),
            toggle), el("div", { class: "muted" }, "“" + s.prompt + "”")));
    }

    box.replaceChildren(head, mode, bounds, categoriesSection(a, data), schedules, playbookSection(a), credentialsSection(a), configEditor(a), chatSection(a));
}

tabs.agents = {
    intervalMs: 5000,
    init: initCreate,
    async refresh() {
        await refreshState();
        renderAgentList();
        // Not while someone edits the permissions or types a message.
        const f = document.activeElement;
        const editing = f && (f.id === "permissions-yaml" || f.closest(".chat") || f.closest(".confirm-delete") || f.closest(".config-edit") ||
            f.closest(".credentials-edit") || f.closest(".playbook-run"));
        if (!editing) await renderAgentDetail();
    },
};

/* categoriesSection: the agent's task categories (§6a), their mode and
 * envelope, their readiness (agent-admin readiness, which reads the
 * journal: through the root bridge), and promote and demote. */
const ENVELOPE_TEMPLATE = "windows:\n  - {days: [sat], from: '02:00', to: '04:00', tz: UTC}\n";

function windowsText(c) {
    return ((c && c.envelope && c.envelope.windows) || []).map(w =>
        (w.days ? w.days.join(",") : "every day") + " " + w.from + "–" + w.to + " " + (w.tz || "UTC")).join("; ");
}

function pctText(x) { return (x * 100).toFixed(1) + " %"; }

/* diagnosticsEvidence shows the agent's diagnostics history over the
 * readiness window (§6b): its reports, how much of their evidence was
 * verified, and the feedback on them; evidence, not a threshold. */
function diagnosticsEvidence(box, r) {
    const d = r.diagnostics;
    const old = box.querySelector("#diagnostics-evidence");
    if (old) old.remove();
    if (!d || !d.reports) return;
    const days = (r.thresholds && r.thresholds.window_days) || 30;
    const verdicts = d.confirmed + d.rejected;
    box.append(el("div", { id: "diagnostics-evidence" },
        el("h3", null, "Diagnostics evidence"),
        el("p", { class: "muted" }, "What this agent's diagnosis reports showed in the last " + days + " days, and what people " +
           "said of them (agent-admin feedback). " + (r.diagnostics_apply
            ? "This host started diagnostics-only, so it counts in every category's readiness above: at least " +
              ((r.thresholds && r.thresholds.diagnostics_feedback) || 0) + " reports with feedback, and at least " +
              pctText((r.thresholds && r.thresholds.diagnostics_confirmed_rate) || 0) + " of the decided hypotheses confirmed."
            : "This host did not start diagnostics-only: it is shown, not counted in the readiness above.")),
        el("ul", null,
            el("li", null, d.reports + (d.reports === 1 ? " report, " : " reports, ") + d.hypotheses + " hypotheses; " +
               d.verified_quotes + " of " + d.quotes + " quotes verified by Custos"),
            el("li", null, d.with_feedback + " of " + d.reports + " reports with feedback: " + d.confirmed + " hypotheses confirmed, " +
               d.rejected + " rejected, " + d.unsure + " unsure" + (verdicts ? " (" + pctText(d.confirmed_rate) + " confirmed)" : "")),
            el("li", null, "plans: " + d.plans_useful + " useful, " + d.plans_not_useful + " not useful, " + d.plans_executed + " carried out"))));
}

function categoriesSection(a, data) {
    const box = el("section", { class: "card", id: "categories-card" }, el("h2", null, "Task categories"),
        el("p", { class: "muted" }, "A category is promoted on its own: within its windows a bounded category's changes run without an " +
           "approval; outside them, and in every other category, they need one (CONTRACT.md §6a). The demotion evaluator " +
           "(custos-demote.timer) demotes it again on its override rate or its rollback conditions."));
    if (!data) return box;
    const states = data.categories || {};
    const names = [...new Set([...(data.permissions || []).map(p => p.category).filter(Boolean), ...Object.keys(states)])].sort();
    if (names.length === 0) {
        box.append(el("p", { class: "muted" }, "No permission names a category: add category: NAME to a permission to promote it later."));
        return box;
    }
    const table = el("table", { id: "categories" }, el("thead", null, el("tr", null,
        el("th", null, "Task category"), el("th", null, "Mode"), el("th", null, "Windows"), el("th", null, "Readiness"),
        el("th", null, "Overrides"), el("th", null, "Incidents"), el("th", null, "Proposals"), el("th", null, ""))));
    const tbody = el("tbody");
    const reason = el("input", { id: "category-reason", type: "text", placeholder: "Reason (recorded)", "aria-label": "Reason" });
    const env = el("textarea", { id: "category-envelope", rows: "5", spellcheck: "false", "aria-label": "Envelope (YAML)" });
    const despite = el("input", { id: "category-despite", type: "checkbox" });
    const which = el("select", { id: "category-select", "aria-label": "Category" }, ...names.map(n => el("option", { value: n }, n)));
    const fillEnvelope = () => {
        const c = states[which.value];
        env.value = c && c.envelope ? JSON.stringify(c.envelope, null, 2) + "\n" : ENVELOPE_TEMPLATE;
    };
    which.addEventListener("change", fillEnvelope);
    fillEnvelope();
    const cells = {};
    for (const n of names) {
        const c = states[n] || { mode: "supervised" };
        const bounded = c.mode === "bounded";
        const demote = el("button", { class: "secondary" }, "Demote");
        demote.hidden = !bounded;
        demote.addEventListener("click", () => act(demote, "Demoting " + n,
            () => admin(["demote", a.name, n, "--reason", reason.value.trim() || "demoted in Cockpit"])));
        const promote = el("button", { class: "secondary" }, bounded ? "Change envelope…" : "Promote…");
        promote.addEventListener("click", () => {
            which.value = n;
            fillEnvelope();
            form.open = true;
            env.focus();
        });
        cells[n] = { ready: el("td", { class: "muted" }, "…"), over: el("td", { class: "muted" }), inc: el("td", { class: "muted" }), props: el("td", { class: "muted" }) };
        tbody.append(el("tr", { id: "category-" + n }, el("td", { class: "mono" }, n),
            el("td", null, el("span", { class: "badge " + (bounded ? "badge-ok" : "badge-waiting") }, bounded ? "bounded (promoted)" : c.mode),
                !bounded && /^custos-demote /.test(c.demoted_by || "")
                    ? el("div", { class: "muted demoted-auto" }, "demoted automatically " + when(c.demoted_by.slice("custos-demote ".length)) +
                         " (see the Approvals tab)")
                    : null),
            el("td", { class: "muted" }, bounded ? (windowsText(c) || "always") : "–"),
            cells[n].ready, cells[n].over, cells[n].inc, cells[n].props,
            el("td", null, el("span", { class: "actions" }, promote, demote)),
        ));
    }
    table.append(tbody);
    const go = el("button", null, "Promote");
    go.addEventListener("click", () => {
        const args = ["promote", a.name, which.value, "--envelope", "-"];
        if (despite.checked) args.push("--despite-readiness");
        if (reason.value.trim()) args.push("--reason", reason.value.trim());
        act(go, "Promoting " + which.value, () => admin(args, env.value));
    });
    const form = el("details", { id: "promote-form" }, el("summary", null, "Promote a category"),
        el("p", { class: "muted" }, "The envelope: its windows (days mon…sun, from and to as HH:MM, tz), and optionally rollback " +
           "(on: tool_error, limit_exceeded, health_check_failed; health_check: a command). agent-admin checks it and refuses a " +
           "category that is not ready, unless you promote it despite its readiness, with a reason."),
        el("div", { class: "toolbar" }, which), env,
        el("label", null, despite, " Promote despite its readiness (needs a reason)"),
        el("div", { class: "actions" }, go));
    const reviews = el("div", { id: "category-reviews" });
    box.append(table, el("div", { class: "toolbar" }, reason), reviews, form);
    // The after-the-fact review of promoted categories' autonomous actions
    // (agent-admin review, through the root bridge).
    if (names.some(n => states[n] && states[n].promoted_by)) {
        admin(["review", a.name]).then(out => {
            const q = JSON.parse(out);
            const items = [];
            for (const [cat, cq] of Object.entries(q.categories || {}).sort()) {
                for (const x of cq.pending || []) {
                    const verdict = (v, label, cls) => {
                        const b = el("button", { class: cls }, label);
                        b.addEventListener("click", () => {
                            const args = ["review", a.name, cat, x.activity, x.call, v];
                            if (reason.value.trim()) args.push("--reason", reason.value.trim());
                            act(b, "Reviewing " + x.tool, () => admin(args));
                        });
                        return b;
                    };
                    items.push(el("div", { class: "call", id: "review-" + x.activity + "-" + x.call },
                        el("div", { class: "head" }, el("strong", { class: "mono" }, x.tool), el("span", { class: "muted" }, cat + " · " + x.activity),
                            el("span", { class: "muted", style: "margin-left:auto" }, when(x.at) + (x.outcome === "error" ? " · failed" : "")),
                            verdict("ok", "Fine", "secondary"), verdict("incident", "Should not have run", "danger"))));
                }
            }
            const counts = Object.entries(q.categories || {}).map(([cat, cq]) => cat + ": " + cq.reviewed + " of " + cq.actions + " reviewed this week");
            reviews.replaceChildren(el("h3", null, "To review"),
                el("p", { class: "muted" }, "What promoted categories did without an approval. \"Should not have run\" counts as an override " +
                   "and demotes the category at once; it needs a reason. " + counts.join("; ")),
                ...(items.length ? items : [el("p", { class: "muted" }, "Nothing to review.")]));
        }).catch(ex => reviews.replaceChildren(el("p", { class: "muted" }, "Review: " + ex.message)));
    }
    // Readiness, as the person with administrative access (it reads the
    // agent's and the gateway's journal).
    admin(["readiness", a.name]).then(out => {
        const r = JSON.parse(out);
        for (const n of names) {
            const m = r.categories && r.categories[n];
            if (!m) continue;
            const th = r.thresholds || {};
            cells[n].ready.replaceChildren(el("span", { class: "badge " + (m.ready ? "badge-ok" : "badge-waiting") }, m.ready ? "ready" : "not ready"),
                m.ready ? "" : el("div", { class: "muted" }, (m.unmet || []).join("; ")));
            cells[n].over.textContent = pctText(m.override_rate) + " (" + m.overrides + ") of max " + pctText(th.override_rate || 0);
            cells[n].inc.textContent = pctText(m.incident_rate) + " (" + m.incidents + ") of max " + pctText(th.incident_rate || 0);
            cells[n].props.textContent = m.proposals + " in " + m.observed_days.toFixed(1) + " days · " + m.tools + " tools, " + m.distinct_calls + " distinct calls";
        }
        diagnosticsEvidence(box, r);
    }).catch(ex => {
        for (const n of names) cells[n].ready.textContent = "unknown";
        box.append(el("p", { id: "readiness-note", class: "muted" }, "Readiness: " + ex.message));
    });
    return box;
}
