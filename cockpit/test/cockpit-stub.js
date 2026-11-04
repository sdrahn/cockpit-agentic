// Stub of the parts of cockpit.js the Custos page uses, with fixtures: two
// agents (triage delegated to patch-pilot, whose install waits for
// approval a-42), one agent whose socket is not reachable, the gateway's
// pending approval, a timer and a journal record. window.__calls records
// what the page asked for: [what, socket or command, path or "", body,
// superuser].
(function () {
    const log = window.__calls = [];
    const ago = s => new Date(Date.now() - s * 1000).toISOString();
    const listeners = {};
    const location = {
        path: (window.location.hash.replace(/^#\/?/, "") || "").split("/").filter(Boolean),
        go(path) {
            this.path = path;
            window.location.hash = "#/" + path.join("/");
            (listeners.locationchanged || []).forEach(f => f());
        },
    };

    let activities = {
        triage: [
            { id: "e-5a10f00d", session: "s-1", user: "system:schedule", state: "running", started: ago(120),
              origin: { kind: "schedule", by: "agent-schedule@triage_morning-check.timer", schedule: "morning-check" },
              root: { agent: "triage", activity: "e-5a10f00d" },
              summary: "Found 1 pending security patch; asked patch-pilot to install it.",
              calls: [{ call_id: "c1", tool: "zypp__list_updates", state: "ok", since: ago(115), ended: ago(114) },
                      { call_id: "c2", tool: "agents__delegate", state: "running", since: ago(100) }] },
        ],
        "patch-pilot": [
            { id: "e-7c21beef", session: "s-2", user: "agent-triage", state: "waiting_approval", started: ago(100),
              origin: { kind: "delegation", by: "agent-triage" },
              parent: { agent: "triage", activity: "e-5a10f00d" }, root: { agent: "triage", activity: "e-5a10f00d" },
              summary: "Installing 1 security patch (openssl-3); nginx restarts, about 5 s.",
              calls: [{ call_id: "c1", tool: "zypp__list_updates", state: "ok", since: ago(95), ended: ago(94) },
                      { call_id: "c2", tool: "zypp__install_patches", state: "waiting_approval", since: ago(90),
                        approval_id: "a-42", approval: "pending",
                        rationale: "Installing 1 security patch (openssl-3); nginx restarts, about 5 s." }] },
            { id: "e-33b0cafe", session: "t1", user: "alice", state: "finished", started: ago(3600), ended: ago(3500),
              origin: { kind: "chat", by: "alice" }, root: { agent: "patch-pilot", activity: "e-33b0cafe" },
              summary: "3 patches pending, none security-relevant. Nothing installed.",
              calls: [{ call_id: "c1", tool: "zypp__list_updates", state: "ok" }] },
        ],
    };
    const status = name => ({ contract: 1, implementation: "custos/adk-go", name, version: "test",
        lifecycle: { kind: "systemd", service: "agent@" + name + ".service", socket: "agent@" + name + ".socket" } });
    let pending = [
        { id: "a-42", channel: "oob", action: "tools.call", server: "zypp", name: "install_patches",
          principal: { sub: "agent-patch-pilot", uid: 981, transport: "unix", client: { name: "custos/patch-pilot" } },
          args: { patches: ["SUSE-SLE-16-2026-4711"], auto_agree_with_licenses: false },
          prompt: "supervised mode: changes the system, needs approval (out of band)",
          scopes: ["once", "1h"], expires: new Date(Date.now() + 30000).toISOString(), waiting: true },
    ];

    function fail(status, problem, body) {
        const ex = new Error(body || problem);
        ex.status = status;
        ex.problem = problem;
        const p = Promise.reject(ex);
        // cockpit's promise passes the body as the second argument.
        return { then: (ok, bad) => p.then(ok, e => bad(e, body)) };
    }
    const ok = v => Promise.resolve(typeof v === "string" ? v : JSON.stringify(v));

    function agentRoute(name, method, path, superuser) {
        if (name === "disk-watch") return fail(0, "access-denied");
        if (method === "GET" && path === "/v1/status") return ok(status(name));
        if (method === "GET" && path === "/v1/activities") return ok({ activities: activities[name] });
        const cancel = path.match(/^\/v1\/activities\/([^/]+)\/cancel$/);
        if (method === "POST" && cancel) {
            // Not the person's activity: only root may (§5).
            if (!superuser) return fail(403, null, "only root, the activity's user or originator may cancel it\n");
            activities[name] = activities[name].map(a => a.id === cancel[1]
                ? Object.assign({}, a, { state: "cancelled", ended: new Date().toISOString() }) : a);
            return ok("");
        }
        return fail(404, null, "not found\n");
    }

    function gatewayRoute(method, path, body) {
        if (method === "GET" && path === "/v1/approvals") return ok(pending);
        const m = path.match(/^\/v1\/approvals\/([^/]+)$/);
        if (method === "POST" && m) {
            if (!pending.some(p => p.id === m[1])) return fail(404, null, JSON.stringify({ error: "not pending" }));
            pending = pending.filter(p => p.id !== m[1]);
            return ok("");
        }
        return fail(404, null, JSON.stringify({ error: "not found" }));
    }

    window.cockpit = {
        location,
        addEventListener(ev, f) { (listeners[ev] = listeners[ev] || []).push(f); },
        user() { return Promise.resolve({ name: "carol" }); },
        http(opts) {
            const sock = opts.unix;
            const route = (method, path, body) => {
                log.push([method, sock, path, body || "", opts.superuser || ""]);
                const m = sock.match(/^\/run\/agents\/([^/]+)\/api\.sock$/);
                if (m) return agentRoute(m[1], method, path, opts.superuser);
                if (sock === "/run/mcp-gateway/control.sock") return gatewayRoute(method, path, body);
                return fail(0, "not-found");
            };
            return { get: path => route("GET", path), post: (path, body) => route("POST", path, body) };
        },
        spawn(args, opts) {
            log.push(["spawn", args.join(" "), "", "", (opts || {}).superuser || ""]);
            if (args[0] === "ls") return Promise.resolve("disk-watch\npatch-pilot\ntriage\nnot_an_agent\n");
            if (args[0] === "systemctl") {
                const next = new Date(); next.setHours(next.getHours() + 3, 0, 0, 0);
                return Promise.resolve(JSON.stringify([
                    { next: next.getTime() * 1000, unit: "agent-schedule@patch-pilot_compliance.timer", activates: "agent-schedule@patch-pilot_compliance.service" },
                    { next: next.getTime() * 1000, unit: "agent-schedule@triage_morning-check.timer", activates: "agent-schedule@triage_morning-check.service" },
                ]));
            }
            if (args[0] === "journalctl") {
                return Promise.resolve(JSON.stringify({ AGENT_EVENT: "approval.resolved", AGENT_OUTCOME: "declined",
                    AGENT_NAME: "disk-watch", AGENT_TOOL: "snapper__delete_snapshots", AGENT_APPROVAL_ID: "a-17",
                    __REALTIME_TIMESTAMP: String((Date.now() - 600000) * 1000) }) + "\n");
            }
            return Promise.reject(Object.assign(new Error("not stubbed"), { exit_status: 1 }));
        },
    };
})();
