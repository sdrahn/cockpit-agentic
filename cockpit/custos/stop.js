/* The emergency stop (CONTRACT.md §8): "Stop all agents" in the header,
 * after a confirmation, runs agent-admin stop-all through the root
 * bridge. The gateway denies every agent everything at once
 * (_control: stopped), then systemd stops every agent and schedule. The
 * banner says who stopped them and when, and "Release…" undoes it
 * (agent-admin release-all). Both name the person (§2).
 */
"use strict";

let releasing = false; // the banner asks to confirm the release

async function emergency(args, what, buttons) {
    buttons.forEach(b => { b.disabled = true });
    const note = document.getElementById("stop-note");
    note.textContent = what + "…";
    try {
        await admin(args);
        showError(null);
    } catch (ex) {
        showError(what + " failed: " + ((ex && ex.message) || ex));
    }
    note.textContent = "";
    document.getElementById("stop-confirm").hidden = true;
    releasing = false;
    buttons.forEach(b => { b.disabled = false });
    await refreshState().catch(() => {});
    refreshCurrent();
}

function renderStopBanner() {
    const box = document.getElementById("stopped");
    const c = state.posture && state.posture.control;
    const stopped = !!(c && c.stopped);
    box.hidden = !stopped;
    document.getElementById("stop-all").disabled = stopped;
    if (!stopped) {
        releasing = false;
        return;
    }
    const text = el("span", null, el("strong", null, "All agents stopped"),
        c.by ? " by " + c.by : "", c.at ? " at " + when(c.at) : "",
        ". The gateway denies every agent call, and agent units and schedules are stopped.");
    if (!releasing) {
        box.replaceChildren(text, el("span", { class: "actions" },
            el("button", { class: "secondary", onclick: () => { releasing = true; renderStopBanner() } }, "Release…")));
        return;
    }
    const release = el("button", null, "Release all agents");
    const cancel = el("button", { class: "link", onclick: () => { releasing = false; renderStopBanner() } }, "Cancel");
    release.addEventListener("click", () => emergency(["release-all"], "Releasing all agents", [release, cancel]));
    box.replaceChildren(text, el("p", null, "Release lets the gateway allow agent calls again, and starts every agent's " +
        "socket and schedule timers again. Paused agents stay paused."), el("span", { class: "actions" }, release, cancel));
}

/* initStop wires the header's button and its confirmation (main.js). */
function initStop() {
    const confirm = document.getElementById("stop-confirm");
    document.getElementById("stop-all").addEventListener("click", () => { confirm.hidden = false });
    document.getElementById("stop-cancel").addEventListener("click", () => { confirm.hidden = true });
    const go = document.getElementById("stop-go");
    go.addEventListener("click", () => emergency(["stop-all"], "Stopping all agents", [go]));
}
