// Exercise the actual embedded standalone UI with deterministic router RPC replies.
import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const origin = process.argv[2];
assert.ok(origin, "Provide the URL of the local Go panel");
const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 375, height: 820 } });
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const state = {
    hwid: "a".repeat(64), has_key: true, selected: 7, protocol: "auto",
    servers: [{ id: 7, name: "Local fixture" }],
    core: { core_responding: true, core_state: 0, policy_enabled: false }
  };
  let network = {
    revision: "1", enabled: false, device: "br-lan", dns: "1.1.1.1", doh_url: "", direction: "vpn",
    rules: "", sources: [], files: [], refresh_minutes: 360, catalog: [],
    exceptions: { rules: "", sources: [], files: [] }
  };
  const actions = [];
  let upload = Buffer.alloc(0), receivedContinuation = false;
  await page.route("**/router-rpc", async route => {
    const { id, method, params } = route.request().postDataJSON();
    let result;
    switch (method) {
      case "status": result = state; break;
      case "logs": result = ["info", "warn"].map(level => ({ level, time: "2026-01-01T12:00:00Z", message: level })); break;
      case "network.get": result = network; break;
      case "network.writepart": {
        assert.equal(params.offset, upload.length);
        assert.equal(params.transfer, params.offset ? "fixture-transfer" : "");
        if (params.offset) receivedContinuation = true;
        upload = Buffer.concat([upload, Buffer.from(params.chunk, "base64")]);
        if (params.final) {
          network = { ...network, ...JSON.parse(upload.toString()), revision: String(Number(network.revision) + 1) };
          upload = Buffer.alloc(0);
        }
        result = params.final ? true : { transfer: "fixture-transfer" };
        break;
      }
      case "diagnostics.ip":
        result = { time: "2026-01-01T12:00:00Z", wan: { ip: "192.0.2.1" }, tunnel: { ip: "198.51.100.1" } };
        break;
      case "tunnel.connect":
      case "tunnel.restart":
      case "tunnel.disconnect":
        actions.push([method, params]);
        state.core.core_state = method === "tunnel.disconnect" ? 0 : 2;
        state.core.policy_enabled = state.want_tunnel = state.core.core_state === 2;
        if (params.protocol) state.protocol = params.protocol;
        result = true;
        break;
      default: errors.push("Unexpected RPC: " + method);
    }
    await route.fulfill({ json: { jsonrpc: "2.0", id, result } });
  });

  await page.goto(origin);
  await page.waitForFunction(() => document.querySelector("#hwid")?.value.length === 64);
  assert.equal(await page.locator("#hwid").isDisabled(), true);
  const toolbar = page.locator(".sota-service-actions button");
  assert.deepEqual(await toolbar.allTextContents(), ["Старт", "Стоп", "Перезапуск"]);
  await page.locator("[data-language=en]").click();
  assert.deepEqual(await toolbar.allTextContents(), ["Start", "Stop", "Restart"]);
  const boxes = await toolbar.evaluateAll(buttons => buttons.map(b => [b.offsetTop, getComputedStyle(b).backgroundColor]));
  assert.equal(new Set(boxes.map(b => b[0])).size, 1);
  const [start, stop, restart] = boxes.map(b => b[1].match(/\d+/g).map(Number));
  assert.ok(start[1] > start[0] && start[1] > start[2], "green Start");
  assert.ok(stop[0] > stop[1] && stop[0] > stop[2], "red Stop");
  assert.ok(restart[0] >= restart[1] && restart[1] > restart[2] * 1.5, "yellow Restart");

  await page.locator("#protocol").selectOption("hysteria2");
  await page.locator("#connect").click();
  await page.locator("#restart").click();
  await page.locator("#checkip").click();
  await page.waitForFunction(() => document.querySelector("#tunnel").textContent === "198.51.100.1");
  assert.equal(await page.locator("#wan").textContent(), "192.0.2.1");
  await page.locator("#disconnect").click();
  await page.waitForFunction(() => !document.querySelector("#connect").disabled);
  assert.deepEqual(actions, [
    ["tunnel.connect", { id: 7, protocol: "hysteria2" }],
    ["tunnel.restart", { id: 7, protocol: "hysteria2" }], ["tunnel.disconnect", {}]
  ]);
  await page.locator("#level").selectOption("warn");
  assert.equal(await page.locator("#events .severity-warn").count(), 1);
  assert.equal(await page.locator("#events .severity-info").count(), 0);

  async function edit(section, selector, value, prepare) {
    await page.locator('[data-page="' + section + '"]').click();
    const form = page.locator("#network-editor form:visible");
    await form.locator("button.primary:enabled").waitFor();
    await form.locator(selector).fill(value);
    if (prepare) await prepare(form);
    await form.locator("button.primary").click();
    await form.getByText("Rules applied", { exact: true }).waitFor();
  }
  await edit("network", "#sota-doh_url", "https://resolver.example/dns-query");
  const listURL = "https://lists.example/routes.txt";
  const fileText = "#" + "x".repeat(15000) + "\n203.0.113.7/32";
  await edit("lists", "#sota-rules", "198.51.100.0/24\n*.example.org", async form => {
    await form.getByRole("button", { name: "+ Add list", exact: true }).click();
    await form.locator("input[type=url]").fill(listURL);
    await form.locator(".sota-files input[type=file]").setInputFiles({
      name: "ci.txt", mimeType: "text/plain", buffer: Buffer.from(fileText)
    });
    await form.getByText("ci.txt", { exact: true }).waitFor();
  });
  await edit("exceptions", "#sota-exception-rules", "direct.example.org");
  assert.equal(network.doh_url, "https://resolver.example/dns-query");
  assert.equal(network.rules, "198.51.100.0/24\n*.example.org");
  assert.deepEqual(network.sources, [listURL]);
  assert.equal(network.files[0].text, fileText);
  assert.ok(receivedContinuation);
  assert.equal(network.exceptions.rules, "direct.example.org");
  await page.reload();
  await page.locator('[data-language=en][aria-pressed=true]').waitFor();
  await page.waitForFunction(() => document.querySelector("#sota-exception-rules")?.value === "direct.example.org");
  assert.deepEqual(errors, []);
  console.log("PASS mobile RU/EN UI, service controls, transport, HWID, IP, logs, DoH and split-routing forms");
} finally {
  await browser.close();
}
