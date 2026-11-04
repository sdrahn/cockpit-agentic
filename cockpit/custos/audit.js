/* Audit tab: what the agents did and what was decided about it, from the
 * journal (CONTRACT.md §10), as far as the person may read it:
 *
 * - state-changing actions, each through its history: proposed by the
 *   agent (tool.call with its rationale, approval.pending), decided at
 *   the gateway (its mcp-approval record, with the approver), executed
 *   or not (tool.result), joined by approval id ("History of an action");
 * - the records themselves, the agents' (AGENT_CONTRACT=1) and the
 *   gateway's audit records about agents (sub or principal agent-*),
 *   filtered by agent, kind and text.
 */
"use strict";

const AUDIT_LINES = { 500: "Last 500", 2000: "Last 2000", 10000: "Last 10000" };

/* journalJSON reads journal entries (journalctl -o json) for matches. */
async function journalJSON(lines, ...matches) {
    const out = await cockpit.spawn(["journalctl", "--no-pager", "-o", "json", "-n", String(lines), ...matches],
                                    { err: "message" });
    return out.split("\n").filter(Boolean).map(l => { try { return JSON.parse(l) } catch { return null } }).filter(Boolean);
}

const entryTime = e => new Date(Number(e.__REALTIME_TIMESTAMP) / 1000);

/* gatewayRecords are the gateway's audit records about agents: its JSON
 * lines with "audit": true whose subject or principal is an agent. */
function gatewayRecords(entries) {
    return entries.map(e => {
        let m;
        try { m = JSON.parse(e.MESSAGE) } catch { return null }
        if (!m || m.audit !== true) return null;
        const who = m.sub || m.principal || "";
        if (!who.startsWith("agent-")) return null;
        return Object.assign({ time: entryTime(e), agent: who.slice(6) }, m);
    }).filter(Boolean);
}

/* actions joins the records into one history per approval (§10). */
function actions(agentRecs, gwRecs) {
    const byId = new Map();
    const get = id => {
        if (!byId.has(id)) byId.set(id, { id });
        return byId.get(id);
    };
    for (const r of agentRecs) {
        const id = r.AGENT_APPROVAL_ID;
        if (!id) continue;
        const a = get(id);
        a.agent = r.AGENT_NAME;
        a.tool = a.tool || r.AGENT_TOOL;
        if (r.AGENT_EVENT === "approval.pending") Object.assign(a, { proposed: entryTime(r), call: r.AGENT_CALL_ID, invocation: r.AGENT_INVOCATION, user: r.AGENT_USER });
        if (r.AGENT_EVENT === "approval.resolved") a.resolved = { time: entryTime(r), outcome: r.AGENT_OUTCOME };
        if (r.AGENT_EVENT === "tool.result") a.result = { time: entryTime(r), outcome: r.AGENT_OUTCOME, reason: r.AGENT_REASON };
    }
    // The rationale is on the call (tool.call), joined by call id.
    for (const r of agentRecs) {
        if (r.AGENT_EVENT !== "tool.call") continue;
        for (const a of byId.values()) {
            if (a.call === r.AGENT_CALL_ID && a.invocation === r.AGENT_INVOCATION && a.agent === r.AGENT_NAME) a.rationale = r.AGENT_RATIONALE;
        }
    }
    for (const g of gwRecs) {
        if (g.event !== "mcp-approval" || !byId.has(g.id)) continue;
        byId.get(g.id).decision = { time: g.time, approved: g.ok === true || g.ok === "true", by: g.by, scope: g.scope, via: g.via, channel: g.channel };
    }
    return [...byId.values()].filter(a => a.proposed || a.decision).sort((x, y) => (y.proposed || 0) - (x.proposed || 0));
}

function step(label, cls, text, time) {
    return el("div", { class: "step" }, el("span", { class: "badge " + cls }, label), " ", text,
              time ? el("span", { class: "muted" }, " · " + when(time)) : null);
}

function renderActions(list) {
    const box = document.getElementById("audit-actions");
    if (list.length === 0) {
        box.replaceChildren(el("p", { class: "muted" }, "No state-changing action in these records."));
        return;
    }
    box.replaceChildren(...list.map(a => {
        const d = a.decision;
        const r = a.result;
        const decided = d
            ? step(d.approved ? "approved" : "declined", d.approved ? "badge-ok" : "badge-bad",
                   "by " + d.by + (d.scope ? " (" + d.scope + ")" : "") + (d.via ? " via " + d.via : ""), d.time)
            : a.resolved
                ? step(a.resolved.outcome, STATE_CLASS[a.resolved.outcome] || "", "(the gateway's record is not readable here)", a.resolved.time)
                : step("pending", "badge-waiting", "no decision yet", null);
        const executed = r
            ? (d && !d.approved) || r.outcome === "declined"
                ? step("not executed", "", "", r.time)
                : step(r.outcome === "ok" ? "executed" : r.outcome, r.outcome === "ok" ? "badge-ok" : "badge-bad", r.reason || "", r.time)
            : null;
        return el("div", { class: "action", id: "action-" + a.id },
            el("div", { class: "head" }, el("span", { class: "mono" }, a.tool || "?"),
               el("span", { class: "muted", title: a.id }, (a.agent || "?") + " · approval " + shortApproval(a.id) + (a.user ? " · for " + a.user : ""))),
            a.proposed ? step("proposed", "badge-running", a.rationale ? "“" + a.rationale + "” (the agent's)" : "", a.proposed) : null,
            decided, executed);
    }));
}

const KINDS = {
    all: () => true,
    approvals: r => /^approval\./.test(r.event) || r.event === "mcp-approval" || r.event === "mcp-grant-revoke",
    interventions: r => /^(agent\.(paused|resumed)|activity\.cancel)$/.test(r.event),
    denials: r => r.effect === "deny" || r.outcome === "denied",
    tools: r => /^tool\./.test(r.event) || r.action === "tools/call",
};

/* rows are both sources as one list: {time, source, agent, event, outcome, text}. */
function rows(agentRecs, gwRecs) {
    const a = agentRecs.map(r => ({
        time: entryTime(r), source: "agent", agent: r.AGENT_NAME, event: r.AGENT_EVENT, outcome: r.AGENT_OUTCOME,
        text: [r.AGENT_TOOL, r.AGENT_APPROVAL_ID && "approval " + r.AGENT_APPROVAL_ID, r.AGENT_REASON, r.MESSAGE].filter(Boolean).join(" · "),
    }));
    const g = gwRecs.map(r => ({
        time: r.time, source: "gateway", agent: r.agent, event: r.event || r.action, effect: r.effect, action: r.action,
        outcome: r.effect || (r.ok === undefined ? "" : r.event === "mcp-approval"
            ? (r.ok === true || r.ok === "true" ? "approved" : "declined")
            : (r.ok === true || r.ok === "true" ? "ok" : "failed")),
        text: [r.server && (r.server + "__" + (r.name || r.target || "")), r.id && "approval " + r.id, r.by && "by " + r.by,
               r.reason, r.grant && "grant " + r.grant].filter(Boolean).join(" · "),
    }));
    return a.concat(g).sort((x, y) => y.time - x.time);
}

function renderRows(all) {
    const f = document.getElementById("audit-filter");
    const agent = f.elements.agent.value;
    const kind = KINDS[f.elements.kind.value] || KINDS.all;
    const text = f.elements.text.value.trim().toLowerCase();
    const shown = all.filter(r => (!agent || r.agent === agent) && kind(r) &&
        (!text || [r.agent, r.event, r.outcome, r.text].join(" ").toLowerCase().includes(text)));
    const body = document.querySelector("#audit-records tbody");
    body.replaceChildren(...(shown.length ? shown.slice(0, 500).map(r => el("tr", null,
        el("td", { class: "mono muted" }, when(r.time)),
        el("td", null, el("span", { class: "badge" + (r.source === "gateway" ? " badge-running" : "") }, r.source)),
        el("td", null, r.agent), el("td", { class: "mono" }, r.event),
        el("td", null, r.outcome ? stateBadge(r.outcome === "allow" ? "ok" : r.outcome === "deny" ? "denied" : r.outcome) : ""),
        el("td", { class: "muted details" }, r.text))) : [el("tr", null, el("td", { colspan: "6", class: "muted" }, "No records match."))]));
}

let auditRows = [];

tabs.audit = {
    intervalMs: 0, // on demand: the journal can be long
    init() {
        const f = document.getElementById("audit-filter");
        f.elements.lines.replaceChildren(...Object.entries(AUDIT_LINES).map(([n, t]) => el("option", { value: n }, t)));
        f.addEventListener("submit", ev => { ev.preventDefault(); refreshCurrent() });
        for (const name of ["agent", "kind"]) f.elements[name].addEventListener("change", () => renderRows(auditRows));
        f.elements.text.addEventListener("input", () => renderRows(auditRows));
    },
    async refresh() {
        const lines = document.getElementById("audit-filter").elements.lines.value || "500";
        let agentRecs, gwEntries;
        try {
            [agentRecs, gwEntries] = await Promise.all([
                journalJSON(lines, "AGENT_CONTRACT=1"),
                journalJSON(lines, "-u", "mcp-gateway.service").catch(() => [])]);
        } catch {
            throw { userMessage: "Reading the journal needs access to it (systemd-journal or wheel)." };
        }
        const gwRecs = gatewayRecords(gwEntries);
        document.getElementById("audit-gateway-note").hidden = gwEntries.length > 0;
        renderActions(actions(agentRecs, gwRecs));
        auditRows = rows(agentRecs, gwRecs);
        const select = document.getElementById("audit-filter").elements.agent;
        const names = [...new Set(auditRows.map(r => r.agent).filter(Boolean))].sort();
        const keep = select.value;
        select.replaceChildren(el("option", { value: "" }, "All agents"), ...names.map(n => el("option", { value: n }, n)));
        select.value = names.includes(keep) ? keep : "";
        renderRows(auditRows);
    },
};
