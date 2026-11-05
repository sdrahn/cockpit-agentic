/* An agent's credentials (CONTRACT.md §17): agent-admin credentials lists
 * their names, what uses them and the missing ones, never a value;
 * set-credential stores or replaces one (the secret on stdin), and
 * remove-credential removes one. All through the root bridge: the
 * credential store is root's alone. The page keeps no secret: the field
 * is cleared once it is stored.
 */
"use strict";

/* loadCredentials reads every agent's credentials; null without
 * administrative access. */
async function loadCredentials() {
    try {
        return JSON.parse(await cockpit.spawn(["agent-admin", "credentials"], { superuser: "try", err: "message" }));
    } catch {
        return null;
    }
}

/* missingCredentials are the credentials an agent needs and does not
 * have: it does not start without them. */
function missingCredentials(name) {
    const c = state.credentials && state.credentials[name];
    return (c && c.missing) || [];
}

const USED_BY = { model: "its model (model.credential)", gateway: "the gateway (OpenShell)" };

/* The form and the removal being confirmed, per agent: kept across the
 * tab's refreshes, which rebuild the table around them. */
const credentialUIs = new Map();

function credentialUI(a) {
    if (credentialUIs.has(a.name)) return credentialUIs.get(a.name);
    const ui = { removing: null };
    const name = el("input", { name: "credential", class: "mono", placeholder: "model-key", autocomplete: "off",
                               "aria-label": "Credential name" });
    const secret = el("input", { name: "secret", type: "password", autocomplete: "new-password", "aria-label": "Secret" });
    const restart = el("input", { type: "checkbox", name: "restart", checked: true });
    const also = el("div", { class: "also" });
    const note = el("span", { class: "muted" });
    const store = el("button", { type: "submit" }, "Store");
    const form = el("form", { class: "credentials-edit" },
        el("div", { class: "fields" },
           el("label", null, "Credential name ", name),
           el("label", null, "Secret (the key itself) ", secret)),
        also,
        el("label", null, restart, " Restart the agents that run, so that they use it"),
        el("div", { class: "actions" }, store, note));
    const say = (msg, cls) => { note.className = cls || "muted"; note.textContent = msg };
    // Other agents whose model uses the same name: one key for several
    // agents, each with its own encrypted copy.
    ui.renderAlso = () => {
        const n = name.value.trim();
        const others = Object.entries(state.credentials || {}).filter(([agent, c]) => agent !== a.name &&
            c.credentials.some(x => x.name === n && x.used_by.includes("model")));
        const kept = new Set([...also.querySelectorAll("input:checked")].map(i => i.value));
        also.replaceChildren(...(others.length ? ["Store it for these agents too, whose model uses " + n + ": ",
            ...others.map(([agent]) => el("label", null, el("input", { type: "checkbox", value: agent, checked: kept.has(agent) }), " " + agent))] : []));
    };
    name.addEventListener("input", ui.renderAlso);
    form.addEventListener("submit", async ev => {
        ev.preventDefault();
        const n = name.value.trim();
        if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(n)) {
            say("A credential name is lower-case letters, digits, - and _, such as model-key.", "error-text");
            return;
        }
        if (!secret.value) {
            say("Type the secret to store.", "error-text");
            return;
        }
        const targets = [a.name, ...[...also.querySelectorAll("input:checked")].map(i => i.value)];
        store.disabled = true;
        const done = [], restarted = [];
        try {
            for (const t of targets) {
                say("Storing for " + t + "…");
                await admin(["set-credential", t, n], secret.value);
                done.push(t);
            }
            secret.value = "";
            if (restart.checked) {
                // An OpenShell agent's set-credential makes its sandbox
                // itself; a systemd agent reads its credentials at start.
                for (const t of targets) {
                    const ag = state.agents.find(x => x.name === t);
                    if (!ag || ag.stopped) continue;
                    const u = unitsOf(ag);
                    if (u.service.startsWith("agent@")) {
                        await systemctl("try-restart", u.service);
                        restarted.push(t);
                    }
                }
            }
            say("Stored " + n + ", encrypted, for " + done.join(", ") + (restarted.length ? "; restarted " + restarted.join(", ") : "") + ".", "ok-text");
        } catch (ex) {
            secret.value = "";
            say((done.length ? "Stored for " + done.join(", ") + "; then: " : "") + ex.message, "error-text");
        }
        store.disabled = false;
        tabs.agents.refresh();
    });
    Object.assign(ui, { form, name, secret, say });
    credentialUIs.set(a.name, ui);
    return ui;
}

function credentialsSection(a) {
    const box = el("section", { class: "card", id: "credentials" }, el("h2", null, "Credentials"));
    const all = state.credentials;
    if (!all) {
        box.append(el("p", { class: "muted" }, "Listing an agent's credentials needs administrative access (turn it on in " +
            "Cockpit's top bar)."));
        return box;
    }
    const mine = all[a.name] || { credentials: [], missing: [] };
    const ui = credentialUI(a);
    box.append(el("p", { class: "muted" }, "Encrypted in /etc/credstore.encrypted, root's alone; systemd hands them to the " +
        "agent when it starts. A value can be replaced, never read back. A model server that needs no key needs no credential."));
    const tbody = el("tbody");
    if (mine.credentials.length === 0) tbody.append(el("tr", null, el("td", { colspan: "4", class: "muted" }, "No credentials.")));
    for (const c of mine.credentials) {
        const set = el("button", { class: "secondary" }, c.stored ? "Replace" : "Set");
        set.addEventListener("click", () => {
            ui.name.value = c.name;
            ui.renderAlso();
            ui.say("");
            ui.secret.focus();
        });
        const remove = el("button", { class: "link danger-text" }, "Remove…");
        remove.hidden = !c.stored || !!c.held_by;
        remove.addEventListener("click", () => { ui.removing = c.name; renderAgentDetail() });
        tbody.append(el("tr", { id: "credential-" + c.name },
            el("td", { class: "mono" }, c.name),
            el("td", null, c.used_by.length ? c.used_by.map(u => USED_BY[u] || u).join(", ") : el("span", { class: "muted" }, "nothing")),
            el("td", null, !c.stored ? el("span", { class: "badge badge-bad" }, "missing: the agent does not start")
                : c.held_by ? "held by OpenShell" : "stored " + (c.stored_at ? when(c.stored_at) : "")),
            el("td", null, el("span", { class: "actions" }, set, remove))));
    }
    box.append(el("table", null, el("thead", null, el("tr", null,
        el("th", null, "Name"), el("th", null, "Used by"), el("th", null, "State"), el("th", null, ""))), tbody));

    const removing = ui.removing && mine.credentials.find(c => c.name === ui.removing && c.stored);
    if (removing) {
        const used = removing.used_by.includes("model");
        const go = el("button", { class: "danger" }, "Remove " + removing.name);
        const cancel = el("button", { class: "link" }, "Cancel");
        cancel.addEventListener("click", () => { ui.removing = null; renderAgentDetail() });
        go.addEventListener("click", () => act(go, "Removing " + removing.name + " from " + a.name, async () => {
            await admin(["remove-credential", a.name, removing.name, ...(used ? ["--force"] : [])]);
            ui.removing = null;
        }));
        box.append(el("div", { class: "confirm credentials-edit" },
            el("h2", null, "Remove the credential " + removing.name + " of " + a.name + "?"),
            el("p", null, used ? "Its model uses it: the agent does not start without it until one is stored again."
                : "Nothing in its configuration uses it."),
            el("div", { class: "actions" }, go, cancel)));
    }
    ui.renderAlso();
    box.append(el("h3", null, "Store a credential"), ui.form);
    return box;
}
