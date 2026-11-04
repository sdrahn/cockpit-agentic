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

    check(errors.length === 0, "no page errors" + (errors.length ? ": " + errors.join("; ") : ""));
    await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
