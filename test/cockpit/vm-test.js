/* The Cockpit page against a real Cockpit, in the test VM (test/vm/run-vm.sh
 * with SUITE=cockpit, after test/cockpit/vm-setup.sh): logged in as a
 * person (carol), everything through Cockpit's own bridges, checked in the
 * VM over ssh where it matters (the gateway's audit, the agent data, the
 * emergency stop, SELinux). With SHOTS=DIR it saves the screenshots of the
 * real page that the documentation uses.
 *
 * Environment: COCKPIT_URL, COCKPIT_USER, COCKPIT_PASSWORD; SSH (the ssh
 * command to run a command in the VM, as a JSON array); WAITING_ACTIVITY,
 * WAITING_APPROVAL, DECLINED_APPROVAL, T0 (from vm-setup.sh); CHROME
 * (optional); SHOTS (optional). Prints PASS/FAIL lines and exits 1 if any
 * failed.
 */
"use strict";
const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");
let chromium;
try {
    ({ chromium } = require("playwright-core"));
} catch {
    ({ chromium } = require("playwright"));
}

const env = k => {
    if (!process.env[k]) throw new Error("missing " + k);
    return process.env[k];
};
const url = env("COCKPIT_URL");
const user = env("COCKPIT_USER");
const password = env("COCKPIT_PASSWORD");
const sshCmd = JSON.parse(env("SSH"));
const waiting = env("WAITING_ACTIVITY");
const approval = env("WAITING_APPROVAL");
const declined = env("DECLINED_APPROVAL");
const t0 = env("T0");
const shots = process.env.SHOTS;
if (shots) fs.mkdirSync(shots, { recursive: true });

let failed = 0, total = 0;
function check(cond, what, detail) {
    total++;
    if (cond) {
        console.log("PASS: " + what);
    } else {
        failed++;
        console.log("FAIL: " + what + (detail ? "\n    | " + String(detail).split("\n").join("\n    | ") : ""));
    }
}

/* vm runs a shell command in the VM as root; returns its output, or null. */
function vm(cmd) {
    try {
        return execFileSync(sshCmd[0], [...sshCmd.slice(1), cmd], { encoding: "utf8", timeout: 60000 });
    } catch {
        return null;
    }
}
/* vmEventually waits until cmd succeeds in the VM. */
async function vmEventually(seconds, cmd) {
    for (let i = 0; i < seconds; i++) {
        if (vm(cmd) !== null) return true;
        await new Promise(r => setTimeout(r, 1000));
    }
    return false;
}
const gwAudit = jq => `journalctl -u mcp-gateway.service -o cat --no-pager --since @${t0} | grep '"audit":true' | jq -e 'select(${jq})' >/dev/null`;

(async () => {
    const browser = await chromium.launch(Object.assign({ args: ["--ignore-certificate-errors"] },
        process.env.CHROME ? { executablePath: process.env.CHROME } : {}));
    // Screenshots a person will look at: 2x.
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
    const page = await context.newPage();
    // Errors in the page's scripts (its frame's /custos/ files) fail the
    // test; Cockpit's own (the shell's, minified) are listed with where
    // they come from.
    const errors = [], shellErrors = [];
    page.on("pageerror", e => {
        const where = String(e.stack || "").split("\n").slice(0, 4).join(" | ");
        (/\/custos\/[a-z]+\.js/.test(e.stack || "") ? errors : shellErrors).push(e.message + " @ " + where);
    });
    // Cockpit's login sends the remembered choice of administrative access
    // (localStorage "superuser:<user>"): "any" starts the sudo bridge with
    // the login password, as when the person had turned it on before.
    await context.addInitScript(u => { try { localStorage.setItem("superuser:" + u, "any") } catch { /* none */ } }, user);
    const frame = () => page.frameLocator('iframe[name="cockpit1:localhost/custos"]');
    const shot = async name => { if (shots) await page.screenshot({ path: path.join(shots, name + ".png") }) };
    const step = async (name, fn) => {
        try {
            await fn();
        } catch (e) {
            check(false, name, e.message.split("\n")[0]);
            if (shots) await page.screenshot({ path: path.join(shots, "failed-" + name.replace(/\W+/g, "-") + ".png") }).catch(() => {});
        }
    };
    const go = async hash => {
        await page.goto(url + "/custos#" + hash);
        await frame().locator("#tabs").waitFor({ timeout: 60000 });
    };

    await step("log in to Cockpit as " + user, async () => {
        await page.goto(url + "/");
        await page.fill("#login-user-input", user);
        await page.fill("#login-password-input", password);
        await page.click("#login-button");
        await page.waitForSelector("iframe[name^='cockpit1:']", { timeout: 90000 });
        check(true, "log in to Cockpit as " + user);
    });

    await step("the Activity tab", async () => {
        await go("/activity/patch-pilot/" + waiting);
        const row = frame().locator("#activity-patch-pilot-" + waiting);
        await row.waitFor({ timeout: 60000 });
        check(true, "the page finds the agents and patch-pilot's waiting activity, as " + user);
        await frame().locator("#activity-detail h2").waitFor();
        const detail = await frame().locator("#activity-detail").textContent();
        check(detail.includes("schedule · morning-check") && detail.includes("triage"),
              "its chain goes back to triage's schedule", detail.slice(0, 300));
        check((await frame().locator("#whoami").textContent()).includes(user), "signed in as " + user);
        check(await row.evaluate(e => e.classList.contains("child")), "the delegated activity is under triage's");
        await shot("activity");
    });

    await step("the Approvals tab", async () => {
        await go("/approvals/" + approval);
        const item = frame().locator("#approval-" + approval);
        await item.waitFor({ timeout: 60000 });
        const t = await item.textContent();
        check(t.includes("custos-test__change") && t.includes("agent-patch-pilot") && t.includes("self-asserted") &&
              t.includes("Writing the test marker"), "the approval item joins the gateway's call and the agent's account", t.slice(0, 300));
        await frame().locator("#decided .decided").first().waitFor({ timeout: 30000 });
        check((await frame().locator("#decided").textContent()).includes(declined.slice(0, 10)), "today's decline is listed (the journal, as " + user + ")");
        await shot("approvals");
        await item.locator("button:has-text('Approve once')").click();
        await frame().locator("#approvals").getByText("not pending").waitFor({ timeout: 30000 });
        check(await vmEventually(30, gwAudit(`.event == "mcp-approval" and .id == "${approval}" and .ok == true and .by == "${user}" and .via == "cockpit"`)),
              "the gateway's audit names " + user + " as the approver, via cockpit, not root");
        check(await vmEventually(60, `[ "$(curl -s --unix-socket /run/agents/patch-pilot/api.sock http://a/v1/activities/${waiting} | jq -r .state)" = finished ]`),
              "the approved change ran and the activity finished");
    });

    await step("the Audit tab", async () => {
        await go("/audit");
        const a = frame().locator("#action-" + approval);
        await a.waitFor({ timeout: 60000 });
        const t = await a.textContent();
        check(t.includes("approved") && t.includes("by " + user) && t.includes("executed"), "the history: proposed, approved by " + user + ", executed", t);
        check((await frame().locator("#action-" + declined).textContent()).includes("declined"), "and the declined one");
        await shot("audit");
    });

    await step("the Agents tab", async () => {
        await go("/agents/patch-pilot");
        await frame().locator("#boundaries").waitFor({ timeout: 60000 });
        const b = await frame().locator("#boundaries").textContent();
        check(b.includes("custos-test__probe") && b.includes("needs approval"), "patch-pilot's tool boundaries, from agent-admin show", b);
        await shot("agents");
        await frame().locator("#agent-detail button:has-text('Pause')").click();
        check(await vmEventually(30, `grep -q 'paused_by: ${user} ' /etc/mcp-gateway/policy/agents/patch-pilot.yaml`),
              "Pause, with administrative access, records " + user + " in the gateway data");
        await frame().locator("#agent-detail button:has-text('Resume')").click({ timeout: 30000 });
        check(await vmEventually(30, "! grep -q 'paused: true' /etc/mcp-gateway/policy/agents/patch-pilot.yaml"), "Resume");
        // Restart: a new invocation of the agent's service (started now if
        // it had exited on idle).
        const before = vm("systemctl show -p InvocationID --value agent@patch-pilot.service").trim();
        await frame().locator("#agent-detail button:text-is('Restart')").click({ timeout: 30000 });
        check(await vmEventually(60, `id=$(systemctl show -p InvocationID --value agent@patch-pilot.service); [ -n "$id" ] && [ "$id" != "${before}" ] && systemctl is-active -q agent@patch-pilot.service`),
              "Restart restarts agent@patch-pilot.service");
        // Stop, then Start: on a real system the socket's directory stays
        // (RemoveOnStop= removes only the socket), and the page must still
        // see the agent as stopped and offer Start.
        await frame().locator("#agent-detail button:text-is('Stop')").click({ timeout: 30000 });
        check(await vmEventually(30, "! systemctl is-active -q agent@patch-pilot.socket && ! systemctl is-active -q agent@patch-pilot.service"),
              "Stop stops agent@patch-pilot's socket and service");
        await frame().locator("#agent-detail button:text-is('Start')").waitFor({ timeout: 60000 });
        check((await frame().locator("#agent-detail").textContent()).includes("stopped"), "the page shows patch-pilot stopped, with Start");
        await frame().locator("#agent-detail button:text-is('Start')").click();
        check(await vmEventually(30, "systemctl is-active -q agent@patch-pilot.socket"), "Start starts agent@patch-pilot.socket again");
        await frame().locator("#agent-detail button:text-is('Stop')").waitFor({ timeout: 60000 });
        // Logs: Cockpit's own journal page, for the agent's unit.
        await frame().locator("#agent-detail button:text-is('Logs')").click();
        await page.waitForURL(/\/system\/logs#\/\?.*service=agent%40patch-pilot\.service/, { timeout: 30000 });
        const logs = page.frameLocator("iframe[name='cockpit1:localhost/system/logs']");
        await logs.getByText(/agent-patch-pilot|Agent patch-pilot/).first().waitFor({ timeout: 60000 });
        check(true, "Logs opens Cockpit's journal with agent@patch-pilot.service's entries");
    });

    await step("chat", async () => {
        await go("/agents/patch-pilot");
        const chat = frame().locator("#chat-patch-pilot");
        await chat.locator(".chat-input").fill("PROBE the host, please.");
        await chat.locator("button:has-text('Send')").click();
        await chat.locator(".msg-agent", { hasText: "probe ok" }).waitFor({ timeout: 60000 });
        check(true, "chat over AG-UI through Cockpit: the agent's answer arrives");
        check(await vmEventually(20, `journalctl -o json --no-pager --since @${t0} AGENT_NAME=patch-pilot AGENT_EVENT=invocation.start | jq -e 'select(.AGENT_ORIGIN == "chat" and .AGENT_USER == "${user}")' >/dev/null`),
              "the chat ran as an activity of " + user + " (origin chat)");
        if (shots) await chat.screenshot({ path: path.join(shots, "chat.png") });
    });

    await step("the Policy tab", async () => {
        await go("/policy");
        await frame().locator("#posture-patch-pilot").waitFor({ timeout: 60000 });
        await frame().locator("#whatif select").selectOption("patch-pilot");
        await frame().locator("#whatif input").fill("custos-test__change");
        check((await frame().locator("#whatif-result").textContent()).startsWith("needs approval"), "what would the gateway decide");
        await shot("policy");
    });

    await step("the emergency stop", async () => {
        await go("/activity");
        await frame().locator("#stop-all").click();
        await frame().locator("#stop-go").click();
        await frame().locator("#stopped:not([hidden])").waitFor({ timeout: 90000 });
        check((await frame().locator("#stopped").textContent()).includes(user), "the banner names " + user);
        check(vm(`grep -q 'stopped: true' /etc/mcp-gateway/policy/agents/_control.yaml && grep -q 'by: "${user}"' /etc/mcp-gateway/policy/agents/_control.yaml`) !== null,
              "stop-all, with administrative access, records " + user);
        check(vm("! systemctl is-active -q agent@patch-pilot.socket") !== null, "systemd stopped the agents");
        await shot("stopped");
        await frame().locator("#stopped button:has-text('Release…')").click();
        await frame().locator("#stopped button:has-text('Release all agents')").click();
        await frame().locator("#stopped").waitFor({ state: "hidden", timeout: 90000 });
        check(vm("grep -q 'stopped: false' /etc/mcp-gateway/policy/agents/_control.yaml && systemctl is-active -q agent@patch-pilot.socket") !== null,
              "Release: allowed again, the sockets are back");
    });

    check(errors.length === 0, "no errors in the page's scripts", errors.join("\n"));
    if (shellErrors.length) console.log("    | errors outside the page (Cockpit's shell):\n    | " + shellErrors.join("\n    | "));
    // SELinux: no denial for the domains this test drove (enforcing).
    const avc = vm(`ausearch -m AVC,USER_AVC,SELINUX_ERR -ts $(date -d @${t0} '+%x %T') 2>/dev/null | grep -E 'denied|SELINUX_ERR' || true`) || "";
    const ours = avc.split("\n").filter(l => /scontext=[^ ]*:(agent_t|agent_relay_t|mcpsrv_agents_t|mcpgw_t|cockpit_[a-z_]+t):/.test(l));
    check(vm("[ \"$(getenforce)\" = Enforcing ]") !== null, "SELinux is enforcing");
    check(ours.length === 0, "no SELinux denial for the agents, the gateway or Cockpit", ours.slice(0, 10).join("\n"));

    await browser.close();
    console.log(total + " checks, " + failed + " failed");
    process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
