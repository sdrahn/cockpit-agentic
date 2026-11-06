/* Policy tab: every agent's posture at a glance (§6), who may decide its
 * approvals (the gateway's approver rules, §7), and what the gateway would
 * decide for one call: its policy's own decision and reason, or, without
 * access to it, the agents module's rules worked out in the page.
 */
"use strict";

/* approverRules are the gateway's approver rules ({server: [rules],
 * default: [rules]}), from mcp-gateway-admin policy show, as the person
 * or else through the root bridge. null if neither can read them. */
async function approverRules() {
    for (const superuser of [undefined, "try"]) {
        try {
            const out = JSON.parse(await cockpit.spawn(["mcp-gateway-admin", "policy", "show"], { superuser, err: "message" }));
            const data = out && (out.data || out);
            return (data && data.approvers) || { default: ["self"] };
        } catch {
            // next way, or none
        }
    }
    return null;
}

function approversOf(rules, data) {
    if (!rules) return "unknown (the role data is not readable)";
    const servers = [...new Set((data.permissions || []).filter(p => !p.read_only).map(p => p.server))];
    // "self" never lets an agent approve its own call (§7): agents have no
    // password.
    const list = servers.flatMap(s => rules[s] || rules.default || []).filter(r => r !== "self");
    return list.length ? [...new Set(list)].join(", ") : servers.length ? "nobody (only self)" : "–";
}

function renderPosture(rules) {
    const body = document.querySelector("#posture tbody");
    body.replaceChildren();
    const agents = state.posture ? Object.entries(state.posture.agents) : [];
    if (agents.length === 0) {
        body.append(el("tr", null, el("td", { colspan: "6", class: "muted" },
            state.posture ? "No agents." : "The agents' gateway data cannot be read (agent-admin show).")));
        return;
    }
    const control = state.posture.control;
    for (const [name, data] of agents) {
        const effects = (data.permissions || []).map(p => permEffect(p, data, control)[0]);
        const count = text => effects.filter(e => e === text).length || "–";
        body.append(el("tr", { id: "posture-" + name },
            el("td", null, name), el("td", null, modeBadge(data.mode), data.paused ? [" ", stateBadge("paused")] : null),
            el("td", null, String(count("allowed"))), el("td", null, String(count("needs approval"))),
            el("td", null, String(count("allowed, no approval"))),
            el("td", { class: "mono" }, approversOf(rules, data))));
    }
}

/* OPA_SOCKET is the gateway's OPA (mcp-opa.service), for its decision on
 * one call: through the root bridge, since the socket is the gateway's. */
const OPA_SOCKET = "/run/mcp-gateway/opa.sock";
const opa = cockpit.http({ unix: OPA_SOCKET, superuser: "try" });
let whatIfSeq = 0;

/* gatewayDecision asks the gateway's policy (data.mcp.authz.decision) what
 * it decides for the agent's call, with the input the gateway gives it:
 * the agent's principal over the unix socket, the tool, the arguments.
 * The decision carries the gateway's reason (its deny_reasons and
 * unmatched_reasons, CONTRACT.md §6). Rejects when OPA cannot be asked. */
async function gatewayDecision(agent, server, tool, args) {
    const sub = "agent-" + agent;
    const input = {
        principal: { sub, groups: [sub], transport: "unix", session_id: "cockpit-whatif" },
        action: "tools.call", resource: { server, kind: "tool", name: tool }, args, grants: [],
    };
    const out = JSON.parse(await keepBody(opa.post("/v1/data/mcp/authz/decision", JSON.stringify({ input }),
                                                   { "Content-Type": "application/json" }), GET_TIMEOUT_MS));
    if (!out || !out.result || !out.result.effect) throw new Error("no decision");
    return out.result;
}

function decisionView(d) {
    const how = { allow: ["allowed", "badge-ok"], ask: ["needs approval", "badge-waiting"], deny: ["denied", "badge-bad"] }[d.effect] ||
        [d.effect, "badge"];
    const review = d.obligations && d.obligations.review ? "; the gateway's approvers review it after it ran" : "";
    return [el("span", { class: "badge " + how[1] }, how[0]), " ",
        el("span", null, d.reason ? "“" + d.reason + "”" : ""),
        el("span", { class: "muted" }, " — the gateway's policy decided this" + review + ".")];
}

function renderWhatIf() {
    const select = document.querySelector("#whatif select");
    const names = state.posture ? Object.keys(state.posture.agents) : [];
    if (select.options.length !== names.length || names.some((n, i) => select.options[i].value !== n)) {
        const keep = select.value;
        select.replaceChildren(...names.map(n => el("option", { value: n }, n)));
        if (names.includes(keep)) select.value = keep;
    }
    const out = document.getElementById("whatif-result");
    const data = state.posture && state.posture.agents[select.value];
    const [server, tool] = document.getElementById("whatif-tool").value.trim().split("__");
    if (!data || !server || !tool) {
        out.replaceChildren(el("span", { class: "muted" }, "Name a tool as server__tool."));
        return;
    }
    let args = {};
    const argText = document.getElementById("whatif-args").value.trim();
    if (argText) {
        try {
            args = JSON.parse(argText);
        } catch {
            whatIfSeq++;
            out.replaceChildren(el("span", { class: "error-text" }, "The arguments are not JSON."));
            out.dataset.for = select.value + " " + server + "__" + tool + " " + argText;
            return;
        }
    }
    const local = () => {
        const d = decideTool(data, state.posture.control, server, tool);
        return [el("span", { class: "badge " + d.cls }, d.text), " ", el("span", { class: "muted" }, d.why),
            el("span", { class: "muted" }, " — worked out in the page; the gateway's policy could not be asked (it needs administrative access).")];
    };
    const seq = ++whatIfSeq;
    const done = view => {
        if (seq !== whatIfSeq) return; // a newer question is under way
        out.replaceChildren(...nodes(view));
        out.dataset.for = select.value + " " + server + "__" + tool + " " + argText;
    };
    gatewayDecision(select.value, server, tool, args).then(d => done(decisionView(d))).catch(() => done(local()));
}

tabs.policy = {
    intervalMs: 10000,
    init() {
        document.querySelector("#whatif select").addEventListener("change", renderWhatIf);
        document.getElementById("whatif-tool").addEventListener("input", renderWhatIf);
        document.getElementById("whatif-args").addEventListener("input", renderWhatIf);
        document.getElementById("whatif").addEventListener("submit", ev => ev.preventDefault());
    },
    async refresh() {
        const [, rules] = await Promise.all([refreshState(), approverRules()]);
        renderPosture(rules);
        renderWhatIf();
    },
};
