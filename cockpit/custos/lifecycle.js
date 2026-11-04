/* Creating and deleting agents (CONTRACT.md §17), on the Agents tab, with
 * administrative access: agent-admin create NAME --from - (the
 * configuration on stdin) and agent-admin delete NAME [--keep-state].
 * A new agent is supervised and has no permissions: it can do nothing
 * until its tool boundaries list something (§6). Checking a
 * configuration needs no root (agent-admin check -).
 */
"use strict";

const TEMPLATE = `version: 1
name: NAME
description: What this agent is for.
model:
  provider: openai              # an on-premises OpenAI-compatible server
  name: qwen3-32b
  endpoint: http://127.0.0.1:8000/v1
instruction: |
  Say what the agent does, and that it looks before it changes anything.
gateway:
  servers: [zypp]
limits:
  max_llm_calls: 25
  max_tool_calls: 40
  max_invocation_seconds: 1800
interaction:
  agui: true
  client_tools: deny
`;

let justCreated = null; // the agent this page created last

function setCreateNote(text, cls) {
    const note = document.getElementById("create-note");
    note.className = cls || "muted";
    note.textContent = text;
}

/* withName puts name into the configuration's top-level name: line. */
function withName(yaml, name) {
    return /^name:.*$/m.test(yaml) ? yaml.replace(/^name:.*$/m, "name: " + (name || "NAME")) : "name: " + name + "\n" + yaml;
}

async function checkConfig(yaml) {
    const proc = cockpit.spawn(["agent-admin", "check", "-"], { err: "message" });
    proc.input(yaml);
    await proc.catch(ex => { throw new Error(((ex && ex.message) || String(ex)).replace(/^agent-admin: /, "").trim()) });
}

function initCreate() {
    const form = document.getElementById("create-agent");
    const name = form.elements.name;
    const yaml = form.elements.config;
    document.getElementById("create-open").addEventListener("click", () => {
        form.hidden = false;
        if (!yaml.value) yaml.value = TEMPLATE;
        name.focus();
    });
    name.addEventListener("input", () => { yaml.value = withName(yaml.value, name.value.trim()) });
    form.elements.cancel.addEventListener("click", () => { form.hidden = true; setCreateNote("") });
    form.elements.check.addEventListener("click", async () => {
        setCreateNote("Checking…");
        try {
            await checkConfig(yaml.value);
            setCreateNote("The configuration is valid.", "ok-text");
        } catch (ex) {
            setCreateNote(ex.message, "error-text");
        }
    });
    form.addEventListener("submit", async ev => {
        ev.preventDefault();
        const n = name.value.trim();
        if (!NAME_RE.test(n)) {
            setCreateNote("The name is 2 to 32 lower-case letters, digits and dashes, starting with a letter.", "error-text");
            return;
        }
        const submit = form.elements.create;
        submit.disabled = true;
        setCreateNote("Creating " + n + "…");
        try {
            await admin(["create", n, "--from", "-"], yaml.value);
            form.hidden = true;
            yaml.value = "";
            name.value = "";
            setCreateNote("");
            showError(null);
            justCreated = n;
            document.getElementById("agent-created").textContent = "Created " + n + ": supervised, with no permissions. " +
                "It can do nothing until its tool boundaries list what it may do.";
            cockpit.location.go(["agents", n]);
        } catch (ex) {
            setCreateNote(ex.message, "error-text");
        }
        submit.disabled = false;
    });
}

/* The delete confirmation, kept per agent across the tab's refreshes. */
const deletions = new Map();

function deleteControl(a) {
    if (deletions.has(a.name)) return deletions.get(a.name);
    const open = el("button", { class: "link danger-text" }, "Delete…");
    const typed = el("input", { "aria-label": "Type the agent's name to confirm", placeholder: a.name, class: "mono" });
    const keep = el("input", { type: "checkbox", id: "keep-" + a.name });
    const go = el("button", { class: "danger", disabled: true }, "Delete " + a.name);
    const cancel = el("button", { class: "link" }, "Cancel");
    const box = el("div", { class: "confirm confirm-delete", hidden: true },
        el("h2", null, "Delete agent " + a.name + "?"),
        el("p", null, "Its units are stopped and disabled; its configuration, gateway data, credentials, schedules and " +
           "drop-ins are removed. Its user agent-" + a.name + " stays: uids are not reused while files may belong to it."),
        el("label", null, keep, " Keep its state (/var/lib/agents/" + a.name + ")"),
        el("div", { class: "actions" }, typed, go, cancel));
    typed.addEventListener("input", () => { go.disabled = typed.value !== a.name });
    open.addEventListener("click", () => { box.hidden = false; typed.focus() });
    cancel.addEventListener("click", () => { box.hidden = true; typed.value = ""; go.disabled = true });
    go.addEventListener("click", async () => {
        go.disabled = true;
        try {
            await admin(["delete", a.name, ...(keep.checked ? ["--keep-state"] : [])]);
            deletions.delete(a.name);
            chats.delete(a.name);
            showError(null);
            cockpit.location.go(["agents"]);
        } catch (ex) {
            showError("Deleting " + a.name + " failed: " + ex.message);
            go.disabled = false;
        }
    });
    const c = { open, box };
    deletions.set(a.name, c);
    return c;
}
