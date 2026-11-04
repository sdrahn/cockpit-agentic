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

(async () => {
    const root = site();
    const browser = await chromium.launch(process.env.CHROME ? { executablePath: process.env.CHROME } : {});
    const page = await browser.newPage({ viewport: { width: 1400, height: 960 } });
    const errors = [];
    page.on("pageerror", e => errors.push(e.message));
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
    check(rows.join(",") === "agent-row-disk-watch,agent-row-old-agent,agent-row-patch-pilot,agent-row-triage",
          "the agents: on /run/agents, and those with gateway data only");
    check((await page.textContent("#agent-row-old-agent")).includes("stopped"), "an agent without a socket is stopped");
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

    await page.click("#agent-row-triage button");
    await page.waitForSelector("#agent-detail button:has-text('Disable')");
    check((await page.textContent("#agent-detail")).includes("morning-check") &&
          (await page.textContent("#agent-detail")).includes("*-*-* 06:00"), "the schedules with their calendar");
    await page.click("#agent-detail button:has-text('Disable')");
    await page.waitForSelector("#agent-detail button:has-text('Enable')");
    const dis = await spawned("systemctl disable --now agent-schedule@triage_morning-check.timer");
    check(dis.length === 1 && dis[0][4] === "require", "a schedule is disabled with systemctl through the root bridge");

    await page.click("#agent-row-old-agent button");
    await page.waitForSelector("#agent-detail button:has-text('Start')");
    await page.click("#agent-detail button:has-text('Start')");
    await page.waitForTimeout(300);
    check((await spawned("systemctl start agent@old-agent.socket")).length === 1, "a stopped agent is started by its socket");

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

    check(errors.length === 0, "no page errors" + (errors.length ? ": " + errors.join("; ") : ""));
    await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
