/* Custos page for Cockpit: shared helpers.
 *
 * The page acts as the logged-in person (CONTRACT.md §2, "People and the
 * control plane"): it reads the agents' sockets (/run/agents/<name>/
 * api.sock, §3–§5) and mcp-gateway's control API, and decides approvals
 * there, always through the person's own bridge, never as root. Only
 * what needs root (cancelling another person's activity, administration)
 * asks for the root bridge (superuser: "try").
 */
"use strict";

const AGENTS_DIR = "/run/agents";
const GATEWAY_SOCKET = "/run/mcp-gateway/control.sock";
const NAME_RE = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/* cockpit.http rejects with the body beside the error (a second argument,
 * which await drops); keepBody keeps it as ex.body and, for the gateway's
 * JSON errors, its text as ex.message. */
function keepBody(request) {
    return new Promise((resolve, reject) => request.then(resolve, (ex, body) => {
        if (ex && typeof body === "string" && body !== "") {
            ex.body = body;
            try {
                const e = JSON.parse(body);
                if (e && e.error) ex.message = e.error;
            } catch {
                ex.message = body.trim(); // the agents' errors are plain text (§5)
            }
        }
        reject(ex);
    }));
}

function client(socket, opts) {
    const http = cockpit.http(Object.assign({ unix: socket }, opts || {}));
    return {
        get: path => keepBody(http.get(path)).then(JSON.parse),
        post: (path, body) => keepBody(http.post(path, body === undefined ? "" : JSON.stringify(body),
                                                 { "Content-Type": "application/json" })),
    };
}

/* The gateway's control API and an agent's socket, as the person. */
const gateway = client(GATEWAY_SOCKET);
const agentSocket = name => AGENTS_DIR + "/" + name + "/api.sock";
const agentApi = name => client(agentSocket(name));
/* An agent's socket through the root bridge, where Cockpit has it. */
const agentApiAsRoot = name => client(agentSocket(name), { superuser: "try" });

/* Tabs register here: { refresh(), intervalMs, init()? } (main.js). */
const tabs = {};

/* nodes is children as DOM nodes: arrays flattened, null, undefined and
 * false left out, the rest as text. */
function nodes(...children) {
    return children.flat(Infinity).filter(c => c !== null && c !== undefined && c !== false)
        .map(c => c instanceof Node ? c : document.createTextNode(String(c)));
}

function el(tag, attrs, ...children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
        if (v === null || v === undefined || v === false) continue;
        if (k === "class") e.className = v;
        else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
        else e.setAttribute(k, v === true ? "" : v);
    }
    e.append(...nodes(children));
    return e;
}

/* relative describes a time relative to now ("in 5 min", "3 h ago"). */
function relative(date) {
    let s = Math.round((new Date(date) - Date.now()) / 1000);
    const past = s < 0;
    s = Math.abs(s);
    let t;
    if (s < 2) return "now";
    if (s < 120) t = s + " s";
    else if (s < 7200) t = Math.round(s / 60) + " min";
    else if (s < 172800) t = Math.round(s / 3600) + " h";
    else t = Math.round(s / 86400) + " days";
    return past ? t + " ago" : "in " + t;
}

function sameDay(a, b) {
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/* when is a time of day, with "yesterday" or the date when not today. */
function when(date) {
    const d = new Date(date);
    const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    const now = new Date();
    if (sameDay(d, now)) return time;
    const y = new Date(now);
    y.setDate(now.getDate() - 1);
    if (sameDay(d, y)) return "yesterday " + time.replace(/:\d\d$/, "");
    return d.toLocaleDateString() + " " + time.replace(/:\d\d$/, "");
}

/* shortId abbreviates an activity id (e-7c21…) for display. */
function shortId(id) {
    return id && id.length > 6 ? id.slice(0, 6) : id;
}

const STATE_TEXT = {
    running: "running", waiting_approval: "waiting for approval", paused: "paused",
    finished: "finished", failed: "failed", cancelled: "cancelled", limit: "stopped at a limit",
    ok: "ok", error: "error", pending: "pending", approved: "approved", declined: "declined",
};
const STATE_CLASS = {
    running: "badge-running", waiting_approval: "badge-waiting", paused: "badge-waiting", pending: "badge-waiting",
    finished: "badge-ok", ok: "badge-ok", approved: "badge-ok",
    failed: "badge-bad", error: "badge-bad", limit: "badge-bad", declined: "badge-bad",
};

function stateBadge(state) {
    return el("span", { class: "badge " + (STATE_CLASS[state] || "") }, STATE_TEXT[state] || state);
}

/* originText says who or what started an activity (§4 origin). */
function originText(a) {
    const o = a.origin || {};
    switch (o.kind) {
    case "schedule": return "schedule · " + (o.schedule || o.by);
    case "event": return "event · " + o.by;
    case "chat": return "chat · " + (o.by || a.user);
    case "api": return "api · " + (o.by || a.user);
    case "delegation": return "delegated by " + ((a.parent && a.parent.agent) || o.by);
    default: return o.kind || a.user || "";
    }
}

/* listAgents finds the agents on this host (§3): the directories of
 * /run/agents whose socket answers /v1/status with contract 1. */
async function listAgents() {
    let names;
    try {
        names = (await cockpit.spawn(["ls", "-1", AGENTS_DIR], { err: "message" })).split("\n");
    } catch (ex) {
        if (ex && ex.exit_status) return []; // no /run/agents: no agents
        throw ex;
    }
    names = names.map(n => n.trim()).filter(n => NAME_RE.test(n)).sort();
    return Promise.all(names.map(async name => {
        try {
            const status = await agentApi(name).get("/v1/status");
            return status.contract === 1 ? { name, status } : { name, error: "speaks contract " + status.contract };
        } catch (ex) {
            return { name, error: describeAgentFailure(ex) };
        }
    }));
}

/* loadActivities reads every reachable agent's activities, each with its
 * agent's name. */
async function loadActivities(agents) {
    const lists = await Promise.all(agents.filter(a => a.status).map(async a => {
        try {
            const r = await agentApi(a.name).get("/v1/activities");
            return (r.activities || []).map(x => Object.assign({ agent: a.name }, x));
        } catch (ex) {
            a.error = describeAgentFailure(ex);
            return [];
        }
    }));
    return lists.flat();
}

function describeAgentFailure(ex) {
    if (ex && ex.problem === "access-denied") return "no access to its socket (members of agent-clients have it)";
    if (ex && ex.problem === "not-found") return "not running";
    return (ex && (ex.message || ex.problem)) || String(ex);
}

function describeGatewayFailure(ex) {
    if (ex && ex.problem === "not-found") return "mcp-gateway is not running (" + GATEWAY_SOCKET + " not found).";
    if (ex && ex.problem === "access-denied") return "No access to mcp-gateway's control socket (" + GATEWAY_SOCKET + ").";
    return "Talking to mcp-gateway failed: " + ((ex && (ex.message || ex.problem)) || ex);
}

function showError(msg) {
    const box = document.getElementById("error");
    box.hidden = !msg;
    box.textContent = msg || "";
}

/* setTabCount shows n after a tab's name; none for 0. */
function setTabCount(tab, n) {
    const badge = document.querySelector("#tabs button[data-tab=" + tab + "] .count");
    if (!badge) return;
    badge.textContent = n > 0 ? String(n) : "";
    badge.hidden = !(n > 0);
}

/* The page's view of the host, shared by the tabs: refreshed by whichever
 * tab is shown. */
const state = { agents: [], activities: [], posture: null, user: null };

async function refreshState() {
    const [agents, posture] = await Promise.all([listAgents(), loadPosture()]);
    // Agents with gateway data but no socket (stopped) are agents too.
    if (posture) {
        for (const name of Object.keys(posture.agents)) {
            if (!agents.some(a => a.name === name)) agents.push({ name, error: "not running", stopped: true });
        }
        agents.sort((x, y) => x.name.localeCompare(y.name));
    }
    state.agents = agents;
    state.posture = posture;
    state.activities = await loadActivities(agents);
    setTabCount("activity", state.activities.filter(a => a.state === "running" || a.state === "waiting_approval").length);
    renderStopBanner();
    return state;
}

/* loadPosture reads what the gateway enforces for the agents: their data
 * (mode, pause, permissions, §6) and the emergency stop (§8), from
 * agent-admin show (the files are readable by everyone). null if it
 * cannot be read. */
async function loadPosture() {
    // As the person; through the root bridge where Cockpit has one and the
    // files are not readable for the person.
    for (const superuser of [undefined, "try"]) {
        try {
            return JSON.parse(await cockpit.spawn(["agent-admin", "show"], { superuser, err: "message" }));
        } catch {
            // next way, or none
        }
    }
    return null;
}

/* admin runs agent-admin through the root bridge (§2: administration
 * needs root), naming the person, whom agent-admin records (by). It
 * rejects with agent-admin's own message. */
function admin(args, input) {
    const proc = cockpit.spawn(["agent-admin", ...args], {
        superuser: "require", err: "message",
        environ: state.user ? ["SUDO_USER=" + state.user] : [],
    });
    if (input !== undefined) proc.input(input);
    return proc.catch(ex => {
        throw new Error(ex && ex.problem === "access-denied"
            ? "This needs administrative access (turn it on in Cockpit's top bar)."
            : ((ex && ex.message) || String(ex)).replace(/^agent-admin: /, "").trim());
    });
}

/* systemctl runs systemctl through the root bridge. */
function systemctl(...args) {
    return cockpit.spawn(["systemctl", ...args], { superuser: "require", err: "message" }).catch(ex => {
        throw new Error(ex && ex.problem === "access-denied"
            ? "This needs administrative access (turn it on in Cockpit's top bar)."
            : ((ex && ex.message) || String(ex)).trim());
    });
}

function renderStopBanner() {
    const box = document.getElementById("stopped");
    const c = state.posture && state.posture.control;
    box.hidden = !(c && c.stopped);
    if (c && c.stopped) {
        box.replaceChildren(...nodes(el("strong", null, "All agents stopped"),
            c.by ? " by " + c.by : "", c.at ? " at " + when(c.at) : "",
            ". The gateway denies every agent call, and agent units and schedules are stopped."));
    }
}

/* The gateway's decision for an agent's permission or tool, by the rules
 * of the agents module (gateway/policy/agents.rego). The gateway's own
 * decision is the one that counts: this only explains it. */
const MODE_TEXT = {
    advisory: "Only read-only tools run. Everything that changes the system is denied.",
    supervised: "Read-only tools run. Everything that changes the system waits for an approval.",
    bounded: "Like supervised, but the changes marked autonomous run without asking.",
};

function permKind(p) {
    return p.read_only ? "read-only" : p.autonomous ? "changes · autonomous" : "changes the system";
}

/* permEffect is [text, badge class] of a permission in force now. */
function permEffect(p, data, control) {
    if (control && control.stopped) return ["denied (emergency stop)", "badge-bad"];
    if (data.paused) return ["denied (paused)", "badge-bad"];
    if (p.read_only) return ["allowed", "badge-ok"];
    if (data.mode === "advisory") return ["denied (advisory mode)", "badge-bad"];
    if (data.mode === "bounded" && p.autonomous) return ["allowed, no approval", "badge-ok"];
    return ["needs approval", "badge-waiting"];
}

function globRe(g) {
    return new RegExp("^" + g.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".") + "$");
}

/* decideTool is what the gateway decides for a call of server__tool by
 * the agent: the most permissive of the permissions that match it, or
 * denied when none does. Argument constraints are not checked. */
function decideTool(data, control, server, tool) {
    const matching = (data.permissions || []).filter(p =>
        p.resource === undefined && p.prompt === undefined &&
        globRe(p.server).test(server) && globRe(p.tool || "*").test(tool));
    if (matching.length === 0) {
        return { text: "denied", cls: "badge-bad", why: "no permission lists it" };
    }
    const rank = { "badge-ok": 2, "badge-waiting": 1, "badge-bad": 0 };
    const [text, cls] = matching.map(p => permEffect(p, data, control)).sort((x, y) => rank[y[1]] - rank[x[1]])[0];
    const p = matching.find(p => permEffect(p, data, control)[1] === cls);
    const args = matching.some(p => p.args) ? "; its argument constraints are not checked here" : "";
    return { text, cls, why: "by the permission " + p.server + "__" + (p.tool || "*") + " (" + permKind(p) + ") in " + data.mode + " mode" + args };
}

/* findActivity is an activity by agent and id, from the last refresh. */
function findActivity(agent, id) {
    return state.activities.find(a => a.agent === agent && a.id === id);
}

/* chainOf is the delegation chain of a (§9): its root first, a last. A
 * link not on this host (or not kept anymore) is named without details. */
function chainOf(a) {
    const out = [a];
    let cur = a;
    while (cur.parent && cur.parent.activity && out.length < 16) {
        const p = findActivity(cur.parent.agent, cur.parent.activity) ||
                  { agent: cur.parent.agent, id: cur.parent.activity, missing: true };
        out.unshift(p);
        if (p.missing) break;
        cur = p;
    }
    return out;
}

/* chainView shows a chain: the root's origin, then each activity. */
function chainView(chain) {
    const box = el("div", { class: "chain" });
    const root = chain[0];
    if (!root.missing) box.append(el("span", { class: "badge" }, originText(root)));
    chain.forEach((a, i) => {
        if (i > 0 || !root.missing) box.append(el("span", { class: "sep" }, "›"));
        box.append(el("span", { class: "badge" + (i === chain.length - 1 ? " badge-running" : "") },
                      a.agent + " " + shortId(a.id)));
    });
    return box;
}
