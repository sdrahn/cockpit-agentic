/* Smoke test of the Custos page in a headless browser, against a stub of
 * cockpit.js with fixtures (cockpit-stub.js). Needs playwright-core (or
 * playwright) and a Chromium or Chrome binary:
 *
 *   npm install --no-save playwright-core
 *   CHROME=/usr/bin/google-chrome node cockpit/test/smoke.js
 *
 * SCREENSHOTS=DIR also saves the tabs as PNG files.
 */
"use strict";
const fs = require("fs");
const os = require("os");
const path = require("path");

let chromium;
try {
    ({ chromium } = require("playwright-core"));
} catch {
    ({ chromium } = require("playwright"));
}

// The page loads ../base1/cockpit.js; lay it out as Cockpit does.
function site() {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "custos-cockpit-"));
    fs.mkdirSync(path.join(root, "base1"));
    fs.copyFileSync(path.join(__dirname, "cockpit-stub.js"), path.join(root, "base1", "cockpit.js"));
    fs.cpSync(path.join(__dirname, "..", "custos"), path.join(root, "custos"), { recursive: true });
    return root;
}

const cockpit_path = u => (new URL(u).hash.replace(/^#\/?/, "").split("/")[0]);

(async () => {
    const root = site();
    const browser = await chromium.launch(process.env.CHROME ? { executablePath: process.env.CHROME } : {});
    const page = await browser.newPage({ viewport: { width: 1400, height: 960 } });
    const errors = [];
    page.on("pageerror", e => { errors.push(e.message); if (process.env.DEBUG) console.error("page error:", e.message) });
    page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
    const url = "file://" + path.join(root, "custos", "index.html");
    const check = (cond, what) => {
        console.log((cond ? "ok: " : "FAIL: ") + what);
        if (!cond) process.exitCode = 1;
    };
    const calls = () => page.evaluate(() => window.__calls);
    const shots = process.env.SCREENSHOTS;

    // Activity tab, patch-pilot's delegated activity selected.
    await page.goto(url + "#/activity/patch-pilot/e-7c21beef");
    await page.waitForSelector("#activity-patch-pilot-e-7c21beef");
    await page.waitForSelector("#activity-detail h2");
    check(await page.isVisible("#tab-activity"), "the Activity tab is shown");
    check((await page.textContent("#whoami")).includes("carol"), "signed in as the person");
    check((await page.textContent("#stat-running")) === "2", "running now: 2");
    check((await page.textContent("#stat-waiting")) === "1", "waiting for approval: 1");
    check((await page.textContent("#stat-next-note")).includes("compliance, morning-check"), "next scheduled names both schedules");
    check((await page.textContent("#tabs button[data-tab=activity] .count")) === "2", "Activity count");
    const order = await page.$$eval("#activities .activity", es => es.map(e => e.id));
    check(order.indexOf("activity-patch-pilot-e-7c21beef") === order.indexOf("activity-triage-e-5a10f00d") + 1 &&
          await page.$eval("#activity-patch-pilot-e-7c21beef", e => e.classList.contains("child")),
          "the delegated activity is under the one that delegated it");
    const detail = await page.textContent("#activity-detail");
    check(detail.includes("schedule · morning-check") && detail.includes("triage e-5a10") && detail.includes("patch-pilot e-7c21"),
          "the chain back to the schedule");
    check(detail.includes("Review approval a-42") && detail.includes("Rationale (the agent's)"), "the waiting call and its rationale");
    check((await page.textContent("#agents-note")).includes("disk-watch: no access"), "an unreachable agent is named");
    check(!(await calls()).some(c => c[1].includes("not_an_agent")), "only valid agent names are asked");
    check((await calls()).filter(c => c[0] === "GET").every(c => c[4] === ""), "reads go as the person, not root");
    if (shots) await page.screenshot({ path: path.join(shots, "activity.png") });

    // Filters.
    await page.click("#filters button[data-filter=chat]");
    await page.waitForFunction(() => document.querySelectorAll("#activities .activity").length === 1);
    check((await page.textContent("#activities")).includes("3 patches pending"), "the chat filter shows the chat activity");
    await page.click("#filters button[data-filter=all]");

    // Approvals tab: the item joins the gateway's call and the agent's account.
    await page.click("#activity-detail button:has-text('Review approval a-42')");
    await page.waitForSelector("#approval-a-42");
    const item = await page.textContent("#approval-a-42");
    check(item.includes("zypp__install_patches") && item.includes("agent-patch-pilot (uid 981, by SO_PEERCRED)"), "the gateway's half");
    check(item.includes("SUSE-SLE-16-2026-4711"), "the exact arguments, from the gateway");
    check(item.includes("self-asserted") && item.includes("openssl-3") && item.includes("triage e-5a10"), "the agent's half");
    check(await page.$eval("#approval-a-42", e => e.classList.contains("highlight")), "the linked approval is highlighted");
    check((await page.textContent("#decided")).includes("snapper__delete_snapshots"), "decided today, from the journal");
    if (shots) await page.screenshot({ path: path.join(shots, "approvals.png") });
    await page.click("#approval-a-42 button:has-text('Approve for 1 h')");
    await page.waitForFunction(() => document.querySelector("#approvals").textContent.includes("not pending"));
    const post = (await calls()).find(c => c[0] === "POST" && c[2] === "/v1/approvals/a-42");
    check(post && JSON.parse(post[3]).decision === "approve" && JSON.parse(post[3]).scope === "1h" &&
          JSON.parse(post[3]).via === "cockpit", "approve posts the decision and scope to the gateway");
    check(post && post[1] === "/run/mcp-gateway/control.sock" && post[4] === "", "the decision goes as the person, never root");

    // Cancel: not the person's activity, so through the root bridge.
    await page.goto(url + "?reload#/activity/triage/e-5a10f00d"); // a new page, not only a new hash
    await page.waitForSelector("#activity-detail button:has-text('Cancel activity')");
    await page.click("#activity-detail button:has-text('Cancel activity')");
    await page.waitForFunction(() => document.querySelector("#activity-triage-e-5a10f00d").textContent.includes("cancelled"));
    const cancels = (await calls()).filter(c => c[2] === "/v1/activities/e-5a10f00d/cancel");
    check(cancels.length === 2 && cancels[0][4] === "" && cancels[1][4] === "try",
          "cancel tries as the person, then through the root bridge");

    // Agents tab: the list, one agent's posture, and its controls (root bridge).
    const spawned = async (cmd) => (await calls()).filter(c => c[0] === "spawn" && c[1] === cmd);
    await page.goto(url + "?agents#/agents/patch-pilot");
    await page.waitForSelector("#boundaries");
    const rows = await page.$$eval("#agents tbody tr", es => es.map(e => e.id));
    check(rows.join(",") === "agent-row-disk-watch,agent-row-old-agent,agent-row-patch-pilot,agent-row-sandboxed,agent-row-triage",
          "the agents: on /run/agents, and those with gateway data only");
    check(!rows.includes("agent-row-gone-agent"), "a deleted agent's leftover directory (no socket, no gateway data) is not listed");
    check((await page.textContent("#agent-row-old-agent")).includes("stopped"),
          "an agent whose directory is there without a socket is stopped (systemd's RemoveOnStop)");
    const effect = async tool => page.$eval("#boundaries", (t, tool) =>
        [...t.querySelectorAll("tbody tr")].find(r => r.cells[0].textContent === tool).cells[3].textContent, tool);
    check(await effect("zypp__list_*") === "allowed" && await effect("zypp__install_patches") === "needs approval" &&
          await effect("systemd__restart_unit") === "needs approval", "supervised: read-only runs, changes need approval");
    check((await page.textContent("#boundaries")).includes("unit ~ ^nginx\\.service$"), "argument constraints are shown");
    if (shots) await page.screenshot({ path: path.join(shots, "agents.png"), fullPage: true });
    await page.click("button.mode[data-mode=bounded]");
    await page.waitForFunction(() => document.querySelector("button.mode[data-mode=bounded]").getAttribute("aria-checked") === "true");
    const mode = await spawned("agent-admin mode patch-pilot bounded");
    check(mode.length === 1 && mode[0][4] === "require" && mode[0][2] === "SUDO_USER=carol",
          "the mode is set with agent-admin through the root bridge, naming the person");
    await page.waitForFunction(() => document.querySelector("#boundaries").textContent.includes("allowed, no approval"));
    check(await effect("systemd__restart_unit") === "allowed, no approval", "bounded: autonomous changes run without approval");
    await page.click("#agent-detail button:text-is('Restart')");
    await page.waitForTimeout(300);
    const restarted = await spawned("systemctl restart agent@patch-pilot.service");
    check(restarted.length === 1 && restarted[0][4] === "require", "restart restarts the agent's service through the root bridge");
    await page.click("#agent-detail button:has-text('Logs')");
    const jumps = (await calls()).filter(c => c[0] === "jump");
    check(jumps.length === 1 && jumps[0][1] === "/system/logs#/?priority=info&service=agent%40patch-pilot.service",
          "logs opens Cockpit's journal for the agent's unit");
    await page.click("#agent-detail button:has-text('Pause')");
    await page.waitForFunction(() => document.querySelector("#boundaries").textContent.includes("denied (paused)"));
    check((await spawned("agent-admin pause patch-pilot")).length === 1, "pause runs agent-admin pause");
    check(await effect("zypp__list_*") === "denied (paused)", "paused: the gateway denies everything");
    await page.click("#agent-detail summary");
    await page.fill("#permissions-yaml", '- {"server":"zypp","tool":"list_*","read_only":true}\n');
    await page.click("#agent-detail button:has-text('Save permissions')");
    await page.waitForFunction(() => document.querySelectorAll("#boundaries tbody tr").length === 1);
    const perms = (await calls()).find(c => c[1] === "agent-admin permissions patch-pilot");
    check(perms && perms[4] === "require", "permissions are saved with agent-admin through the root bridge");

    // Credentials (§17): names only, through the root bridge; a missing
    // one shows in the list; a secret is stored for this agent and, by
    // choice, for others whose model uses the same name.
    check((await page.textContent("#agent-row-patch-pilot")).includes("credential missing") &&
          !(await page.textContent("#agent-row-disk-watch")).includes("credential missing"),
          "the list shows an agent whose model credential is not stored");
    check((await spawned("agent-admin credentials")).every(c => c[4] === "try"), "the credentials are listed through the root bridge");
    const creds = await page.textContent("#credentials");
    check(creds.includes("model-key") && creds.includes("missing: the agent does not start") && creds.includes("old-key") &&
          (await page.textContent("#credential-old-key")).includes("nothing"), "the agent's credentials: the model's missing, an unused one");
    check(!(await page.textContent("body")).includes("sk-old-value"), "no value is shown");
    if (shots) await (await page.$("#credentials")).screenshot({ path: path.join(shots, "credentials.png") });
    await page.click("#credential-model-key button:text-is('Set')");
    check(await page.$eval("#credentials input[name=credential]", e => e.value) === "model-key" &&
          await page.evaluate(() => document.activeElement.name === "secret"), "Set fills the name and goes to the secret");
    check((await page.textContent("#credentials .also")).includes("disk-watch") &&
          !(await page.textContent("#credentials .also")).includes("triage"), "offered for the other agents whose model uses model-key");
    await page.fill("#credentials input[name=secret]", "sk-new-value");
    await page.check("#credentials .also input[value=disk-watch]");
    await page.click("#credentials button:text-is('Store')");
    await page.waitForFunction(() => document.querySelector("#credentials .credentials-edit .actions").textContent.includes("Stored model-key"));
    const stored = await page.evaluate(() => window.__credentials);
    check(stored["patch-pilot.model-key"] === "sk-new-value" && stored["disk-watch.model-key"] === "sk-new-value" &&
          (await spawned("agent-admin set-credential patch-pilot model-key")).every(c => c[4] === "require"),
          "set-credential stores it for each agent through the root bridge, the secret on stdin");
    check((await spawned("systemctl try-restart agent@patch-pilot.service")).length === 1 &&
          (await spawned("systemctl try-restart agent@disk-watch.service")).length === 1, "the agents that run are restarted");
    check(await page.$eval("#credentials input[name=secret]", e => e.value) === "", "the page keeps no secret");
    await page.waitForFunction(() => !document.querySelector("#agent-row-patch-pilot").textContent.includes("credential missing"));
    check(true, "stored, it is no longer missing");
    await page.click("#credential-old-key button:has-text('Remove')");
    await page.click("#credentials button:text-is('Remove old-key')");
    await page.waitForFunction(() => !document.querySelector("#credential-old-key"));
    const removed = await spawned("agent-admin remove-credential patch-pilot old-key");
    check(removed.length === 1 && removed[0][4] === "require", "an unused credential is removed, without --force");
    await page.click("#credential-model-key button:has-text('Remove')");
    check((await page.textContent("#credentials .confirm")).includes("does not start without it"), "removing the model's says what follows");
    await page.click("#credentials button:text-is('Remove model-key')");
    await page.waitForFunction(() => document.querySelector("#agent-row-patch-pilot").textContent.includes("credential missing"));
    check((await spawned("agent-admin remove-credential patch-pilot model-key --force")).length === 1, "the model's is removed with --force");

    await page.click("#agent-row-triage button");
    await page.waitForSelector("#agent-detail button:has-text('Disable')");
    check(await page.$eval("#credential-gateway-token button:has-text('Remove')", b => b.hidden) &&
          (await page.textContent("#credential-gateway-token")).includes("held by OpenShell"),
          "an OpenShell agent's gateway token is OpenShell's: replaced, not removed");
    check((await page.textContent("#agent-detail")).includes("morning-check") &&
          (await page.textContent("#agent-detail")).includes("*-*-* 06:00"), "the schedules with their calendar");
    await page.click("#agent-detail button:has-text('Disable')");
    await page.waitForSelector("#agent-detail button:has-text('Enable')");
    const dis = await spawned("systemctl disable --now agent-schedule@triage_morning-check.timer");
    check(dis.length === 1 && dis[0][4] === "require", "a schedule is disabled with systemctl through the root bridge");

    await page.click("#agent-row-old-agent button");
    await page.waitForSelector("#agent-detail button:text-is('Start')");
    check(await page.$eval("#agent-detail button:text-is('Restart')", b => b.hidden), "a stopped agent offers Start, not Restart");
    await page.click("#agent-detail button:text-is('Start')");
    await page.waitForTimeout(300);
    check((await spawned("systemctl start agent@old-agent.socket")).length === 1, "a stopped agent is started by its socket");

    // A stopped OpenShell agent: its relay's socket, which the page finds
    // from systemd since the agent cannot say.
    await page.click("#agent-row-sandboxed button");
    await page.waitForSelector("#agent-detail button:text-is('Start')");
    await page.click("#agent-detail button:text-is('Start')");
    await page.waitForTimeout(300);
    check((await spawned("systemctl start agent-openshell@sandboxed.socket")).length === 1,
          "a stopped OpenShell agent is started by its relay's socket");

    // Stop stops an OpenShell agent's socket too, or the next request
    // would start it again.
    await page.click("#agent-row-triage button");
    await page.waitForSelector("#agent-detail button:text-is('Stop')");
    await page.click("#agent-detail button:text-is('Stop')");
    await page.waitForTimeout(300);
    check((await spawned("systemctl stop agent-openshell@triage.socket agent-openshell@triage.service")).length === 1,
          "Stop stops an OpenShell agent's socket and service");

    // Task categories (§6a): a promoted category runs without approval
    // only within its window; the others need one.
    await page.click("#agent-row-disk-watch button");
    await page.waitForSelector("#categories");
    const lastMinute = new Date().getUTCHours() === 23 && new Date().getUTCMinutes() === 59;
    if (!lastMinute) {
        check(await effect("snapper__delete_snapshots") === "allowed, no approval (in its window)",
              "a promoted category within its window runs without approval");
    }
    check(await effect("zypp__patch") === "needs approval (outside its window)", "a promoted category outside its window needs approval");
    check(await effect("firewalld__block") === "needs approval", "a category not promoted needs approval, on a bounded agent too");
    check((await page.textContent("#boundaries")).includes("changes · patch-errata"), "a permission shows its category");
    // The categories with their readiness (agent-admin readiness, through
    // the root bridge), and promote and demote.
    await page.waitForFunction(() => !document.querySelector("#category-patch-errata").cells[3].textContent.startsWith("…"));
    const catRow = await page.$eval("#category-patch-errata", r => [...r.cells].map(c => c.textContent));
    check(catRow[1] === "bounded (promoted)" && catRow[2].includes("02:00–04:00 Europe/Berlin"), "the categories: mode and windows");
    check(catRow[3].startsWith("not ready") && catRow[3].includes("override rate 16.7 % above 5.0 %") &&
          catRow[4].startsWith("16.7 % (2) of max 5.0 %") && catRow[6].startsWith("12 in 3.2 days"),
          "a category's readiness: not ready, what is unmet, its override rate and proposals");
    // The diagnostics history beside the readiness (§6b): evidence, not a
    // threshold.
    await page.waitForSelector("#diagnostics-evidence");
    const dev = await page.textContent("#diagnostics-evidence");
    check(dev.includes("12 reports, 19 hypotheses; 28 of 31 quotes verified by Custos") &&
          dev.includes("8 of 12 reports with feedback: 9 hypotheses confirmed, 1 rejected, 2 unsure (90.0 % confirmed)") &&
          dev.includes("plans: 6 useful, 1 not useful, 5 carried out") &&
          dev.includes("counts in every category's readiness above: at least 10 reports with feedback, and at least 70.0 %"),
          "the diagnostics evidence: reports, verified quotes, feedback, plans; counted on a host that started diagnostics-only");
    check((await page.textContent("#category-config-lifecycle")).includes("ready") &&
          !(await page.textContent("#category-config-lifecycle")).includes("not ready"), "a ready category");
    const sec = await page.$eval("#category-security-response", r => [...r.cells].map(c => c.textContent));
    check(sec[1] === "supervised" && sec[7].includes("Promote…"),
          "a category named only by a permission is listed, supervised, with Promote…");
    check((await spawned("agent-admin readiness disk-watch")).some(c => c[4] === "require"), "readiness is read through the root bridge");
    // The after-the-fact review of what config-lifecycle did without an approval.
    await page.waitForSelector("#review-e-0d90-c1");
    check((await page.textContent("#review-e-0d91-c1")).includes("failed") &&
          (await page.textContent("#category-reviews")).includes("config-lifecycle: 1 of 3 reviewed this week"),
          "the actions to review, with their outcome and the week's count");
    await page.click("#review-e-0d90-c1 button:text-is('Should not have run')");
    await page.waitForFunction(() => document.querySelector("#error").textContent.includes("needs --reason"));
    check(true, "Should not have run needs a reason");
    await page.click("#review-e-0d90-c1 button:text-is('Fine')");
    await page.waitForFunction(() => !document.querySelector("#review-e-0d90-c1"));
    check((await spawned("agent-admin review disk-watch config-lifecycle e-0d90 c1 ok")).some(c => c[4] === "require"),
          "Fine records the verdict through the root bridge");
    await page.fill("#category-reason", "a patch broke nginx");
    await page.click("#category-config-lifecycle button:text-is('Demote')");
    await page.waitForFunction(() => document.querySelector("#category-config-lifecycle").cells[1].textContent === "supervised");
    check((await spawned("agent-admin demote disk-watch config-lifecycle --reason a patch broke nginx")).length === 1,
          "Demote demotes through agent-admin, with the reason");
    await page.click("#category-security-response button:text-is('Promote…')");
    check(await page.$eval("#promote-form", d => d.open) && (await page.inputValue("#category-select")) === "security-response" &&
          (await page.inputValue("#category-envelope")).includes("windows:"), "Promote… opens the form for that category, with an envelope");
    await page.click("#promote-form button:text-is('Promote')");
    await page.waitForFunction(() => document.querySelector("#error").textContent.includes("is not ready"));
    check((await page.textContent("#error")).includes("security-response is not ready (observed 0.0 days of 14"),
          "a category that is not ready is refused, saying why");
    await page.click("#agent-row-disk-watch button");
    await page.waitForSelector("#category-security-response");
    await page.click("#category-security-response button:text-is('Promote…')");
    await page.check("#category-despite");
    await page.fill("#category-reason", "a year by hand, the journal keeps a week");
    await page.click("#promote-form button:text-is('Promote')");
    await page.waitForFunction(() => document.querySelector("#category-security-response").cells[1].textContent === "bounded (promoted)");
    const prom = (await calls()).filter(c => c[0] === "spawn" && c[1].startsWith("agent-admin promote disk-watch security-response"));
    check(prom.length === 2 && prom[1][1] === "agent-admin promote disk-watch security-response --envelope - --despite-readiness --reason a year by hand, the journal keeps a week" &&
          prom[1][4] === "require", "promote despite its readiness, with the reason, through the root bridge");

    // Policy tab: the posture and the what-if, on fresh data.
    await page.goto(url + "?policy#/policy");
    await page.waitForSelector("#posture-patch-pilot");
    const pp = await page.$eval("#posture-patch-pilot", r => [...r.cells].map(c => c.textContent));
    check(pp[1] === "supervised" && pp[2] === "1" && pp[3] === "2" && pp[4] === "–",
          "posture: patch-pilot has 1 read-only and 2 approval-needing permissions");
    check(pp[5].includes("role:admin") && pp[5].includes("group:agent-clients"), "posture: the approvers from the gateway's rules");
    await page.selectOption("#whatif select", "patch-pilot");
    await page.fill("#whatif input", "zypp__list_updates");
    check((await page.textContent("#whatif-result")).startsWith("allowed"), "what-if: a read-only tool is allowed");
    await page.fill("#whatif input", "zypp__install_patches");
    check((await page.textContent("#whatif-result")).startsWith("needs approval"), "what-if: a change needs approval");
    await page.fill("#whatif input", "fs__write_file");
    check((await page.textContent("#whatif-result")).includes("no permission lists it"), "what-if: an unlisted tool is denied");
    await page.selectOption("#whatif select", "triage");
    await page.fill("#whatif input", "zypp__install_patches");
    check((await page.textContent("#whatif-result")).startsWith("denied (advisory mode)"), "what-if: advisory denies a listed change");
    check(!pp[5].includes("self"), "posture: self is not an agent's approver");
    if (shots) await page.screenshot({ path: path.join(shots, "policy.png"), fullPage: true });

    // The emergency stop: confirmed, through the root bridge, naming the
    // person; the banner, then Release.
    await page.goto(url + "?stop#/activity");
    await page.waitForSelector("#activities .activity");
    check(!(await page.isVisible("#stop-confirm")) && !(await page.isVisible("#stopped")), "no stop and no confirmation at first");
    await page.click("header #stop-all");
    check(await page.isVisible("#stop-confirm"), "Stop all agents asks first");
    check((await spawned("agent-admin stop-all")).length === 0, "nothing is stopped before the confirmation");
    await page.click("#stop-cancel");
    check(!(await page.isVisible("#stop-confirm")), "Cancel closes the confirmation");
    await page.click("header #stop-all");
    await page.click("#stop-go");
    await page.waitForSelector("#stopped:not([hidden])");
    const stop = await spawned("agent-admin stop-all");
    check(stop.length === 1 && stop[0][4] === "require" && stop[0][2] === "SUDO_USER=carol",
          "stop-all runs through the root bridge, naming the person");
    check((await page.textContent("#stopped")).includes("All agents stopped by carol"), "the banner says who stopped them");
    check(await page.$eval("#stop-all", b => b.disabled), "Stop all agents is off while stopped");
    if (shots) await page.screenshot({ path: path.join(shots, "stopped.png") });
    await page.click("#stopped button:has-text('Release…')");
    check((await page.textContent("#stopped")).includes("Paused agents stay paused"), "Release says what it does first");
    check((await spawned("agent-admin release-all")).length === 0, "nothing is released before the confirmation");
    await page.click("#stopped button:has-text('Release all agents')");
    await page.waitForFunction(() => document.querySelector("#error").textContent.includes("Releasing all agents failed: the gateway's policy did not load"));
    check(await page.isVisible("#stopped") && (await page.textContent("#stop-note")) === "",
          "a refused release says why, and the agents stay stopped");
    await page.waitForTimeout(5500); // a refresh keeps it
    check((await page.textContent("#error")).includes("Releasing all agents failed"), "a refresh does not clear an action's error");
    await page.click("#error button:has-text('Dismiss')");
    check(!(await page.isVisible("#error")), "Dismiss clears it");
    await page.click("#stopped button:has-text('Release…')");
    await page.click("#stopped button:has-text('Release all agents')");
    await page.waitForSelector("#stopped", { state: "hidden" });
    const rel = await spawned("agent-admin release-all");
    check(rel.length === 2 && rel[0][4] === "require" && rel[0][2] === "SUDO_USER=carol", "release-all names the person too");
    check(!(await page.$eval("#stop-all", b => b.disabled)), "Stop all agents is back");

    // Chat: the person's own thread over AG-UI, as the person.
    await page.goto(url + "?chat#/agents/patch-pilot");
    await page.waitForSelector("#chat-patch-pilot .chat-input");
    await page.fill("#chat-patch-pilot .chat-input", "Patch the host.");
    await page.click("#chat-patch-pilot button:has-text('Send')");
    await page.waitForSelector("#chat-patch-pilot .msg-approval");
    const chat = await page.textContent("#chat-patch-pilot .chat-log");
    check(chat.includes("Patch the host.") && chat.includes("You asked: Patch the host.. Installing 1 patch."),
          "the agent's streamed text, from events split across chunks");
    check(chat.includes("zypp__install_patches") && chat.includes("Waiting for approval") && chat.includes("a-42"),
          "a tool call and its pending approval in the chat");
    const agui = (await calls()).find(c => c[0] === "POST" && c[2] === "/agui");
    const input = agui && JSON.parse(agui[3]);
    check(agui && agui[1] === "/run/agents/patch-pilot/api.sock" && agui[4] === "", "chat goes to the agent's socket as the person");
    check(input && input.messages.length === 1 && input.messages[0].role === "user" && input.threadId.startsWith("t-carol-"),
          "only the new message is sent, on the person's thread");
    await page.waitForTimeout(5500); // the tab's refresh must keep the conversation
    check((await page.textContent("#chat-patch-pilot .chat-log")).includes("Installing 1 patch."), "the conversation survives a refresh");
    await page.fill("#chat-patch-pilot .chat-input", "And then?");
    await page.click("#chat-patch-pilot button:has-text('Send')");
    await page.waitForFunction(() => document.querySelectorAll("#chat-patch-pilot .msg-approval").length === 2);
    const aguis = (await calls()).filter(c => c[2] === "/agui").map(c => JSON.parse(c[3]));
    check(aguis.length === 2 && aguis[0].threadId === aguis[1].threadId && aguis[0].runId !== aguis[1].runId,
          "the next message continues the thread in a new run");
    await page.click("#chat-patch-pilot .msg-approval button:has-text('Review in Approvals')");
    await page.waitForSelector("#approval-a-42");
    check(await page.$eval("#approval-a-42", e => e.classList.contains("highlight")), "Review in Approvals opens the approval");
    if (shots) {
        await page.goto(url + "?chatshot#/agents/patch-pilot");
        await page.waitForSelector("#chat-patch-pilot .chat-input");
        await page.fill("#chat-patch-pilot .chat-input", "Patch the host.");
        await page.click("#chat-patch-pilot button:has-text('Send')");
        await page.waitForSelector("#chat-patch-pilot .msg-approval");
        await page.$eval("#chat-patch-pilot", e => e.scrollIntoView());
        await page.screenshot({ path: path.join(shots, "chat.png") });
    }
    await page.goto(url + "?nochat#/agents/triage");
    await page.waitForSelector("#agent-detail h2");
    check((await page.textContent("#agent-detail")).includes("does not offer chat"), "an agent without AG-UI says so");

    // An agent that never answers (its socket held while its service fails
    // to start) is listed as such; the others are listed as usual.
    await page.goto(url + "?hang#/agents");
    await page.waitForSelector("#agent-row-hung", { timeout: 20000 });
    check((await page.textContent("#agent-row-hung")).includes("not answering") &&
          (await page.textContent("#agent-row-hung")).includes("see its Logs"), "an agent that does not answer says so");
    check(await page.isVisible("#agent-row-patch-pilot") && await page.isVisible("#agent-row-triage"), "the other agents are listed");
    check((await calls()).some(c => c[0] === "close" && c[1] === "hung"), "the unanswered request is closed");
    await page.waitForFunction(() => window.__calls.filter(c => c[0] === "GET" && c[1] === "/run/agents/hung/api.sock").length >= 2, null, { timeout: 30000 });
    check(true, "the page keeps refreshing");

    // Creating and deleting agents.
    await page.goto(url + "?create#/agents");
    await page.waitForSelector("#agents tbody tr");
    await page.click("#create-open");
    await page.waitForFunction(() => document.querySelector("#create-agent textarea").value.includes("version: 1"));
    check((await spawned("agent-admin draft new-agent")).length === 1, "a blank configuration is agent-admin draft's");
    await page.fill("#create-name", "web-watch");
    check((await page.inputValue("#create-agent textarea")).includes("\nname: web-watch\n"), "the name goes into the configuration");
    await page.fill("#create-agent textarea", (await page.inputValue("#create-agent textarea")).replace(/^model:/m, "modle:"));
    await page.click("#create-agent button[name=check]");
    await page.waitForFunction(() => document.querySelector("#create-note").textContent.includes("model: missing"));
    const chk = await spawned("agent-admin check -");
    check(chk.length === 1 && chk[0][4] === "", "check runs agent-admin check - as the person");
    await page.fill("#create-agent textarea", (await page.inputValue("#create-agent textarea")).replace(/^modle:/m, "model:"));
    await page.click("#create-agent button[name=check]");
    await page.waitForFunction(() => document.querySelector("#create-note").textContent.includes("valid"));
    if (shots) await page.screenshot({ path: path.join(shots, "create.png"), fullPage: true });
    await page.fill("#create-name", "Bad Name");
    await page.click("#create-agent button[name=create]");
    check((await page.textContent("#create-note")).includes("lower-case"), "an invalid name is refused before agent-admin");
    check((await spawned("agent-admin create Bad Name --from -")).length === 0, "nothing is created with an invalid name");
    await page.fill("#create-name", "web-watch");
    await page.click("#create-agent button[name=create]");
    await page.waitForSelector("#agent-row-web-watch");
    const created = await spawned("agent-admin create web-watch --from -");
    check(created.length === 1 && created[0][4] === "require" && created[0][2] === "SUDO_USER=carol",
          "create runs agent-admin create --from - through the root bridge, naming the person");
    await page.waitForSelector("#agent-detail h2:has-text('web-watch')");
    check(await page.isVisible("#agent-created") && (await page.textContent("#agent-created")).includes("no permissions"),
          "the new agent is shown, supervised and without permissions");
    check((await page.textContent("#agent-detail")).includes("No permissions: the agent can do nothing."), "its tool boundaries are empty");

    await page.click("#agent-detail button:has-text('Delete…')");
    check(await page.$eval("#agent-detail .confirm-delete button.danger", b => b.disabled), "Delete waits for the name to be typed");
    await page.fill("#agent-detail .confirm-delete input:not([type=checkbox])", "web-watc");
    await page.waitForTimeout(5500); // a refresh keeps the confirmation
    check(await page.isVisible("#agent-detail .confirm-delete") &&
          await page.$eval("#agent-detail .confirm-delete button.danger", b => b.disabled), "a refresh keeps the confirmation, still locked");
    await page.fill("#agent-detail .confirm-delete input:not([type=checkbox])", "web-watch");
    await page.check("#agent-detail .confirm-delete input[type=checkbox]");
    if (shots) await page.screenshot({ path: path.join(shots, "delete.png") });
    await page.click("#agent-detail .confirm-delete button.danger");
    await page.waitForFunction(() => !document.querySelector("#agent-row-web-watch"));
    const deleted = await spawned("agent-admin delete web-watch --keep-state");
    check(deleted.length === 1 && deleted[0][4] === "require", "delete runs agent-admin delete --keep-state through the root bridge");
    check(cockpit_path(await page.url()) === "agents", "after deleting, the list is shown");

    // Creating an agent from a shipped template (§14a).
    await page.goto(url + "?template#/agents");
    await page.waitForSelector("#agents tbody tr");
    await page.click("#create-open");
    await page.waitForSelector("#create-from-row:not([hidden])");
    const tl = await spawned("agent-admin templates");
    check(tl.length === 1 && tl[0][4] === "", "the templates are listed as the person (agent-admin templates)");
    const opts = await page.$$eval("#create-from option", os => os.map(o => [o.value, o.disabled, o.textContent]));
    check(opts[0][0] === "" && opts[1][0] === "service-doctor" && !opts[1][1], "a blank configuration first, then this system's templates");
    check(opts.some(o => o[0] === "patch-pilot-transactional" && o[1] && o[2].includes("for transactional")),
          "a template for transactional systems is offered disabled on a classic one");
    check(opts.some(o => o[0] === "cleaner" && o[1] && o[2].includes("cannot be used")), "a broken template says why");
    await page.selectOption("#create-from", "service-doctor");
    await page.waitForFunction(() => document.querySelector("#create-agent textarea").value.includes("# Agent template service-doctor"));
    check(await page.inputValue("#create-name") === "service-doctor", "the template's name is the agent's until changed");
    const desc = await page.textContent("#create-template");
    check(desc.includes("flapping") && desc.includes("config-lifecycle") && desc.includes("systemd-mcp") &&
          desc.includes("/usr/share/custos/templates/service-doctor/envelopes/"), "the template's description, categories and needs are shown");
    const tread = await spawned("agent-admin draft service-doctor --template service-doctor");
    check(tread.length === 1 && tread[0][4] === "", "the template's configuration is agent-admin draft's, as the person");
    await page.fill("#create-name", "web-doctor");
    check((await page.inputValue("#create-agent textarea")).includes("\nname: web-doctor\n"), "the name goes into the template's configuration");
    if (shots) await page.screenshot({ path: path.join(shots, "create-template.png"), fullPage: true });
    await page.click("#create-agent button[name=create]");
    await page.waitForSelector("#agent-row-web-doctor");
    const tc = await spawned("agent-admin create web-doctor --template service-doctor --from -");
    check(tc.length === 1 && tc[0][4] === "require", "create from a template runs agent-admin create --template --from - through the root bridge");
    check((await page.textContent("#agent-created")).includes("from the template Service doctor"), "the new agent says where it came from");
    await page.click("#create-open");
    await page.selectOption("#create-from", "");
    check(!(await page.inputValue("#create-agent textarea")).includes("Agent template") && !(await page.isVisible("#create-template")),
          "a blank configuration again");

    // Model defaults for new agents (§14b), and the new agent's credential.
    await page.click("#create-agent button[name=cancel]");
    await page.click("#defaults-open");
    check(await page.$eval("#model-defaults input[name=model]", e => e.disabled) &&
          await page.$eval("#model-defaults button[name=save]", e => e.disabled),
          "the fields wait for the current defaults (typing before would be overwritten)");
    await page.waitForFunction(() => document.querySelector("#defaults-note").textContent.includes("No defaults yet"));
    check(!(await page.$eval("#model-defaults input[name=model]", e => e.disabled)), "then they can be filled");
    check((await spawned("agent-admin defaults")).every(c => c[4] === ""), "the defaults are read as the person");
    await page.selectOption("#model-defaults select[name=provider]", "gemini");
    check(!(await page.isVisible("#model-defaults input[name=endpoint]")), "no endpoint for gemini");
    await page.selectOption("#model-defaults select[name=provider]", "openai");
    await page.fill("#model-defaults input[name=endpoint]", "http://ai.example.com:8000/v1");
    await page.click("#model-defaults button[name=save]");
    await page.waitForFunction(() => document.querySelector("#defaults-note").textContent.includes("model.name: missing"));
    check(true, "agent-admin's refusal of incomplete defaults is shown");
    await page.fill("#model-defaults input[name=model]", "granite-4");
    check((await page.textContent("#model-defaults")).includes("Credential name (not the key)"),
          "the credential field asks for a name, not the key");
    await page.fill("#model-defaults input[name=credential]", "sk-litellm-local");
    await page.click("#model-defaults button[name=save]");
    await page.waitForFunction(() => document.querySelector("#defaults-note").textContent.includes("looks like a key"));
    check(!(await page.textContent("#defaults-note")).includes("litellm"), "a key given as the credential is refused, without repeating it");
    await page.fill("#model-defaults input[name=credential]", "model-key");
    await page.fill("#model-defaults input[name=temperature]", "warm");
    await page.click("#model-defaults button[name=save]");
    check((await page.textContent("#defaults-note")).includes("a number"), "a temperature that is no number is refused");
    await page.fill("#model-defaults input[name=temperature]", "");
    await page.click("#model-defaults button[name=save]");
    await page.waitForFunction(() => document.querySelector("#defaults-note").textContent.includes("Saved"));
    const saved = await spawned("agent-admin defaults --from -");
    check(saved.length === 3 && saved.every(c => c[4] === "require"), "the defaults are saved with agent-admin defaults --from - through the root bridge");
    if (shots) await page.screenshot({ path: path.join(shots, "model-defaults.png"), fullPage: true });
    await page.click("#create-open");
    await page.selectOption("#create-from", "service-doctor");
    await page.fill("#create-name", "web-doc2");
    await page.waitForFunction(() => document.querySelector("#create-agent textarea").value.includes("name: granite-4"));
    const drafted = await page.inputValue("#create-agent textarea");
    check(drafted.includes("\nname: web-doc2\n") && drafted.includes("the host's defaults") && !drafted.includes("qwen3"),
          "a template's configuration starts with the host's model defaults");
    await page.click("#create-agent button[name=create]");
    await page.waitForSelector("#agent-created form input[type=password]");
    check((await page.textContent("#agent-created")).includes("uses the credential model-key"), "the new agent's credential is asked for");
    await page.fill("#agent-created input[type=password]", "s3cret");
    await page.click("#agent-created form button");
    await page.waitForFunction(() => document.querySelector("#agent-created").textContent.includes("is stored"));
    const sc = await spawned("agent-admin set-credential web-doc2 model-key");
    check(sc.length === 1 && sc[0][4] === "require" && (await page.evaluate(() => window.__credentials["web-doc2.model-key"])) === "s3cret",
          "the secret goes to agent-admin set-credential on stdin, through the root bridge");
    await page.click("#defaults-open");
    await page.waitForFunction(() => document.querySelector("#model-defaults input[name=model]").value === "granite-4");
    await page.click("#model-defaults button[name=clear]");
    await page.waitForFunction(() => document.querySelector("#defaults-note").textContent.includes("Removed"));
    check((await spawned("agent-admin defaults --clear")).length === 1, "the defaults are removed with agent-admin defaults --clear");

    // Editing an agent's configuration.
    await page.goto(url + "?edit#/agents/patch-pilot");
    await page.waitForSelector("#config-patch-pilot button:has-text('Edit configuration')");
    await page.click("#config-patch-pilot button:has-text('Edit configuration')");
    await page.waitForSelector("#config-patch-pilot .config-yaml");
    const read = (await calls()).find(c => c[0] === "read" && c[1] === "/etc/custos/agents/patch-pilot/agent.yaml");
    check(read && read[4] === "require", "the configuration is read through the root bridge (root:agent-<name> 0640)");
    check((await page.inputValue("#config-patch-pilot .config-yaml")).includes("description: Keeps the host patched."),
          "the editor has the agent's configuration");
    await page.fill("#config-patch-pilot .config-yaml",
                    (await page.inputValue("#config-patch-pilot .config-yaml")).replace("name: patch-pilot", "name: other"));
    await page.click("#config-patch-pilot button:has-text('Save and apply')");
    await page.waitForFunction(() => document.querySelector("#config-patch-pilot").textContent.includes("another agent"));
    check(await page.isVisible("#config-patch-pilot .config-yaml"), "a configuration for another agent is refused, the editor stays");
    await page.fill("#config-patch-pilot .config-yaml",
                    (await page.inputValue("#config-patch-pilot .config-yaml")).replace("name: other", "name: patch-pilot")
                        .replace("Keeps the host patched.", "Keeps the host patched, gently."));
    await page.waitForTimeout(5500); // a refresh keeps what is being edited
    check((await page.inputValue("#config-patch-pilot .config-yaml")).includes("gently"), "a refresh keeps the edit");
    await page.click("#config-patch-pilot button:has-text('Check')");
    await page.waitForFunction(() => document.querySelector("#config-patch-pilot").textContent.includes("valid"));
    await page.click("#config-patch-pilot button:has-text('Save and apply')");
    await page.waitForFunction(() => document.querySelector("#config-patch-pilot").textContent.includes("Saved"));
    const edits = await spawned("agent-admin edit patch-pilot --from -");
    check(edits.length === 2 && edits.every(e => e[4] === "require" && e[2] === "SUDO_USER=carol"),
          "save runs agent-admin edit --from - through the root bridge, naming the person");
    check(!(await page.isVisible("#config-patch-pilot .config-yaml")), "the editor closes after saving");
    await page.click("#config-patch-pilot button:has-text('Edit configuration')");
    await page.waitForSelector("#config-patch-pilot .config-yaml");
    check((await page.inputValue("#config-patch-pilot .config-yaml")).includes("gently"), "reopened, it has the saved configuration");
    check((await page.textContent("#config-patch-pilot")).includes("restarted if it runs") &&
          !(await page.textContent("#config-patch-pilot")).includes("sandbox"), "a systemd agent's editor says it restarts");

    // An OpenShell agent: what an edit does to its sandbox, and what
    // agent-admin says about it.
    await page.goto(url + "?editosh#/agents/triage");
    await page.waitForSelector("#config-triage button:has-text('Edit configuration')");
    await page.click("#config-triage button:has-text('Edit configuration')");
    await page.waitForSelector("#config-triage .config-yaml");
    const osh = await page.textContent("#config-triage");
    check(osh.includes("the sandbox is recreated") && osh.includes("until its credential is set again") &&
          osh.includes("cannot be changed here"), "an OpenShell agent's editor says what an edit does to its sandbox");
    await page.fill("#config-triage .config-yaml", (await page.inputValue("#config-triage .config-yaml")) +
                    "gateway: {oauth: {token_url: \"https://idp/token\", client_id: agent-triage}}\n");
    await page.click("#config-triage button:has-text('Save and apply')");
    await page.waitForFunction(() => document.querySelector("#config-triage").textContent.includes("Saved."));
    check((await page.textContent("#config-triage")).includes("stopped until: agent-admin set-credential triage gateway-client-secret"),
          "agent-admin's message after the edit is shown");

    // Audit tab: each state-changing action's history, and the records.
    await page.goto(url + "?audit#/audit");
    await page.waitForSelector("#action-a-17");
    const hist = async id => page.textContent("#action-" + id);
    const a17 = await hist("a-17"), a9 = await hist("a-9"), a42 = await hist("a-42");
    check(a17.includes("snapper__delete_snapshots") && a17.includes("Free 4 GiB") && a17.includes("declined") &&
          a17.includes("by admin") && a17.includes("not executed"), "a declined action: proposed, declined by the approver, not executed");
    check(a9.includes("approved") && a9.includes("by carol (once) via cockpit") && a9.includes("executed") && a9.includes("for alice"),
          "an approved action: who approved it, from the gateway's record, and that it ran");
    check(a42.includes("pending") && a42.includes("no decision yet") && !a42.includes("executed"), "a pending action");
    const actionOrder = await page.$$eval("#audit-actions .action", es => es.map(e => e.id));
    check(actionOrder.join(",") === "action-a-42,action-a-9,action-a-17", "newest action first");
    const recs = await page.$$eval("#audit-records tbody tr", rs => rs.map(r => r.textContent));
    check(recs.some(r => r.includes("gateway") && r.includes("zypp__install_patches") && r.includes("agent paused")),
          "the gateway's denial of an agent is a record");
    check(!recs.some(r => r.includes("fs__read_file")), "the gateway's records about people are left out");
    check(recs.some(r => r.includes("mcp-approval") && r.includes("declined") && r.includes("by admin")),
          "the gateway's approval record says approved or declined");
    await page.selectOption("#audit-filter select[name=kind]", "interventions");
    check((await page.$$eval("#audit-records tbody tr", rs => rs.length)) === 1 &&
          (await page.textContent("#audit-records tbody")).includes("agent.paused"), "the interventions filter");
    await page.selectOption("#audit-filter select[name=kind]", "all");
    await page.selectOption("#audit-filter select[name=agent]", "disk-watch");
    check((await page.$$eval("#audit-records tbody tr", rs => rs.map(r => r.cells[2].textContent))).every(a => a === "disk-watch"),
          "the agent filter");
    await page.selectOption("#audit-filter select[name=agent]", "");
    await page.fill("#audit-filter input[name=text]", "a-9");
    check((await page.$$eval("#audit-records tbody tr", rs => rs.length)) === 4, "the text filter: a-9's three agent records and the gateway's");
    check(!(await page.isVisible("#audit-gateway-note")), "no note while the gateway's records are readable");
    if (shots) await page.screenshot({ path: path.join(shots, "audit.png"), fullPage: true });
    await page.goto(url + "?nogw#/audit");
    await page.waitForSelector("#action-a-9");
    check(await page.isVisible("#audit-gateway-note") && (await hist("a-9")).includes("not readable here"),
          "without the gateway's records, the approver is said to be missing");
    check((await calls()).some(c => c[0] === "spawn" && c[1].includes("AGENT_CONTRACT=1") && c[4] === ""),
          "the journal is read as the person");

    // The gateway's real approval ids are long: shown short, the full one in the title.
    check(await page.evaluate(() => shortApproval("a-6c88df4c32d09c66eb46bc8941b53745") === "a-6c88df4c…" && shortApproval("a-42") === "a-42"),
          "long approval ids are shown short");

    // A diagnosis report (§4a): the agent's confidence next to how much of
    // its evidence Custos verified; feedback through the root bridge.
    await page.goto(url + "?report#/activity/patch-pilot/e-33b0cafe");
    await page.waitForSelector("#report");
    const rep = await page.textContent("#report");
    check(rep.includes("/var is 98 % full") && rep.includes("playbook disk-pressure"), "the report's summary and playbook");
    check((await page.textContent("#hypothesis-h1")).includes("agent's confidence: high") &&
          (await page.textContent("#hypothesis-h1")).includes("1 of 2 quotes verified") &&
          (await page.textContent("#hypothesis-h2")).includes("1 of 1 quotes verified"),
          "each hypothesis: the agent's confidence and how much of its evidence is verified");
    check((await page.textContent("#hypothesis-h1 #evidence-e1")).includes("✓ verified") &&
          (await page.textContent("#hypothesis-h1 #evidence-e2")).includes("the agent's claim alone"),
          "each quote marked verified, or the agent's claim alone");
    check((await page.textContent("#report .plan")).includes("journalctl --vacuum-size=2G") &&
          await page.isVisible("#report .plan button:has-text('Copy')"), "the plan's commands, to copy");
    if (shots) await (await page.$("#report")).screenshot({ path: path.join(shots, "report.png") });
    await page.click("#report .report-feedback button[type=submit]");
    check((await page.textContent("#report .report-feedback")).includes("Give a verdict"), "an empty feedback is not sent");
    await page.selectOption("#report select[name=h-h1]", "confirmed");
    await page.selectOption("#report select[name=h-h2]", "rejected");
    await page.selectOption("#report select[name=plan]", "useful");
    await page.check("#report input[name=executed]");
    await page.fill("#report input[name=comment]", "journald.conf had no limit");
    await page.waitForTimeout(3500); // a refresh of the tab keeps what was filled in
    await page.click("#report .report-feedback button[type=submit]");
    await page.waitForFunction(() => document.querySelector("#report .report-feedback").textContent.includes("Recorded"));
    const fb = await spawned("agent-admin feedback patch-pilot e-33b0cafe --hypothesis h1=confirmed --hypothesis h2=rejected --plan useful --executed --comment journald.conf had no limit");
    check(fb.length === 1 && fb[0][4] === "require", "feedback is recorded with agent-admin feedback through the root bridge, as filled in");

    // Running a playbook from an agent's view (§14c), as the person.
    await page.goto(url + "?playbooks#/agents/patch-pilot");
    await page.waitForFunction(() => document.querySelector("#playbook-run option[value=disk-pressure]"));
    const pbOpts = await page.$$eval("#playbook-run option", os => os.map(o => o.value));
    check(pbOpts.includes("selinux-denials") && !pbOpts.includes("broken"), "the playbooks are offered, not a broken one");
    await page.selectOption("#playbook-run select[name=playbook]", "selinux-denials");
    await page.click("#playbook-run button[type=submit]");
    await page.waitForFunction(() => document.querySelector("#playbook-run").textContent.includes("does not offer"));
    check(true, "a playbook the gateway does not offer the tools of is refused, with the agent's reason");
    await page.selectOption("#playbook-run select[name=playbook]", "disk-pressure");
    await page.fill("#playbook-run input[name=request]", "/var");
    await page.click("#playbook-run button[type=submit]");
    await page.waitForFunction(() => location.hash.startsWith("#/activity/patch-pilot/e-new"));
    const started = (await calls()).filter(c => c[0] === "POST" && c[2] === "/v1/activities" && c[3].includes("disk-pressure"));
    check(started.length === 1 && JSON.parse(started[0][3]).prompt === "/var" && started[0][4] === "",
          "the playbook runs as the person, with the request, and its activity opens");

    // Owners (O1): who may approve the agent's calls, set through the
    // root bridge.
    await page.goto(url + "?owners#/agents/patch-pilot");
    await page.waitForSelector("#owners-input");
    check((await page.textContent("#owners")).includes("No owners"), "without owners, the servers' approvers decide");
    await page.fill("#owners-input", "user:alice group:web-team");
    await page.click("#owners button:text-is('Save owners')");
    await page.waitForFunction(() => document.querySelector("#owners").textContent.includes("user:alice, group:web-team may approve"));
    const ow = await spawned("agent-admin owners patch-pilot user:alice group:web-team");
    check(ow.length === 1 && ow[0][4] === "require", "owners are set with agent-admin owners through the root bridge");
    await page.fill("#owners-input", "user:agent-triage");
    await page.click("#owners button:text-is('Save owners')");
    await page.waitForFunction(() => document.querySelector("#error") && !document.querySelector("#error").hidden);
    check((await page.textContent("#error")).includes("an agent never approves"), "an agent as owner is refused, with agent-admin's reason");

    check(errors.length === 0, "no page errors" + (errors.length ? ": " + errors.join("; ") : ""));
    await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
