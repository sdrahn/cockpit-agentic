/* Policy tab: every agent's posture at a glance (§6), who may decide its
 * approvals (the gateway's approver rules, §7), and what the gateway would
 * decide for one call, by the agents module's rules.
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
    const [server, tool] = document.querySelector("#whatif input").value.trim().split("__");
    if (!data || !server || !tool) {
        out.replaceChildren(el("span", { class: "muted" }, "Name a tool as server__tool."));
        return;
    }
    const d = decideTool(data, state.posture.control, server, tool);
    out.replaceChildren(el("span", { class: "badge " + d.cls }, d.text), " ", el("span", { class: "muted" }, d.why));
}

tabs.policy = {
    intervalMs: 10000,
    init() {
        document.querySelector("#whatif select").addEventListener("change", renderWhatIf);
        document.querySelector("#whatif input").addEventListener("input", renderWhatIf);
        document.getElementById("whatif").addEventListener("submit", ev => ev.preventDefault());
    },
    async refresh() {
        const [, rules] = await Promise.all([refreshState(), approverRules()]);
        renderPosture(rules);
        renderWhatIf();
    },
};
