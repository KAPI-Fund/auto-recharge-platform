import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { chromium } from "../backend/worker/node_modules/playwright/index.mjs";

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:23000").replace(/\/$/, "");
const apiURL = (process.env.E2E_API_URL || "http://127.0.0.1:28080").replace(/\/$/, "");
const adminEmail = process.env.E2E_ADMIN_EMAIL || "admin@example.com";
const adminPassword = process.env.E2E_ADMIN_PASSWORD || "admin123";
const workerToken = process.env.WORKER_API_TOKEN || "dev-worker-token";

function listen(server) {
  return new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
}

function close(server) {
  return new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
}

async function run() {
  const refreshHits = [];
  const refreshServer = createServer((request, response) => {
    refreshHits.push(`${request.method} ${request.url}`);
    response.writeHead(200, { "Content-Type": "text/plain" });
    response.end("ok");
  });
  await listen(refreshServer);
  const refreshPort = refreshServer.address().port;
  const refreshURL = `http://127.0.0.1:${refreshPort}/rotate`;
  const stamp = Date.now();
  const proxyURL = `http://e2e-refresh-${stamp}.example:18080`;

  const configuredExecutable = process.env.E2E_CHROMIUM_EXECUTABLE_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const launchOptions = { headless: true };
  if (existsSync(configuredExecutable)) launchOptions.executablePath = configuredExecutable;
  const browser = await chromium.launch(launchOptions);
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const apiFailures = [];
  const missingTraceIds = [];
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    const url = response.url();
    if (!url.includes("/legacy-api/") && !url.includes("/platform-api/")) return;
    if (!response.headers()["x-trace-id"]) missingTraceIds.push(`${response.request().method()} ${url}`);
    if (response.status() >= 500) apiFailures.push(`${response.status()} ${response.request().method()} ${url}`);
  });

  let createdId = "";
  const deactivated = [];
  try {
    await page.goto(`${baseURL}/admin-login`, { waitUntil: "domcontentloaded" });
    await page.locator("#admin-email").waitFor({ state: "visible", timeout: 10_000 });
    await page.locator("#admin-email").fill(adminEmail);
    await page.locator("#admin-password").fill(adminPassword);
    await page.getByRole("button", { name: "继续", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin`, { timeout: 15_000 });
    await page.getByRole("heading", { name: "概览中心", exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });

    await page.goto(`${baseURL}/admin/proxies`, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "代理池" }).waitFor({ state: "visible", timeout: 20_000 });
    await page.getByTestId("proxy-refresh-help").waitFor({ state: "visible", timeout: 15_000 });
    assert.equal(await page.getByRole("columnheader", { name: "稳定率", exact: true }).count(), 1, "stability column is missing");
    assert.match(await page.getByTestId("proxy-refresh-help").innerText(), /刷新按钮直接切换 IP/);
    await page.getByTestId("proxy-refresh-url").waitFor({ state: "visible" });
    await page.getByTestId("proxy-refresh-timeout").waitFor({ state: "visible" });
    await page.getByTestId("proxy-refresh-wait").waitFor({ state: "visible" });

    const originalTimeout = await page.getByTestId("proxy-refresh-timeout").inputValue();
    const temporaryTimeout = String(Number(originalTimeout) === 60 ? 59 : Number(originalTimeout || 15) + 1);
    await page.getByTestId("proxy-refresh-timeout").fill(temporaryTimeout);
    const saveConfigRequest = page.waitForRequest((request) => request.url().includes("/legacy-api/admin/config") && request.method() === "POST");
    const saveConfigResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
    await page.getByTestId("proxy-save-refresh-settings").click();
    const configBody = JSON.parse((await saveConfigRequest).postData() || "{}");
    assert.equal(configBody.proxy_refresh_timeout_seconds, temporaryTimeout, "save omitted refresh timeout");
    await saveConfigResponse;
    await page.getByTestId("proxy-refresh-timeout").fill(originalTimeout || "15");
    const restoreConfig = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
    await page.getByTestId("proxy-save-refresh-settings").click();
    await restoreConfig;

    const addRequest = page.waitForRequest((request) => request.url().includes("/legacy-api/admin/proxies") && request.method() === "POST");
    const addResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/proxies") && response.request().method() === "POST" && response.status() === 200);
    await page.locator(".proxy-area").fill(proxyURL);
    await page.getByTestId("proxy-refresh-url").fill(refreshURL);
    await page.getByRole("button", { name: "保存到代理池", exact: true }).click();
    const addBody = JSON.parse((await addRequest).postData() || "{}");
    assert.equal(addBody.refresh_url, refreshURL, "add proxy omitted refresh_url");
    const added = await (await addResponse).json();
    assert.equal(added.success, true, `add proxy failed: ${JSON.stringify(added)}`);
    assert.equal(added.added, 1, `expected 1 added proxy, got ${JSON.stringify(added)}`);
    createdId = String(added.ids?.[0] || "");
    assert.ok(createdId, "add proxy did not return an id");

    await page.getByText(proxyURL, { exact: false }).waitFor({ state: "visible", timeout: 10_000 });
    const refreshButton = page.getByTestId(`proxy-row-refresh-${createdId}`);
    await refreshButton.waitFor({ state: "visible", timeout: 10_000 });
    assert.equal(await refreshButton.isDisabled(), false, "refresh button disabled for configured URL");

    const rowRefresh = page.waitForResponse((response) => response.url().includes(`/legacy-api/admin/proxies/${createdId}/refresh`) && response.request().method() === "POST");
    await refreshButton.click();
    const refreshPayload = await (await rowRefresh).json();
    assert.equal(refreshPayload.success, true, `manual refresh failed: ${JSON.stringify(refreshPayload)}`);
    assert.ok(refreshHits.some((hit) => hit === "GET /rotate"), `refresh URL was not requested: ${JSON.stringify(refreshHits)}`);

    const listResponse = await page.request.get(`${baseURL}/legacy-api/admin/proxies`, {
      headers: {
        "X-Admin-Token": await page.evaluate(() => window.localStorage.getItem("kc_admin_token") || ""),
        "X-Trace-ID": `e2e-proxy-${stamp}`,
      },
    });
    assert.equal(listResponse.ok(), true, `list proxies HTTP ${listResponse.status()}`);
    const listed = await listResponse.json();
    const row = (listed.proxies || []).find((item) => item.id === createdId);
    assert.ok(row, "created proxy missing from list API");
    assert.equal(row.refresh_url, refreshURL);
    assert.equal(row.has_refresh_url, true);

    for (const item of listed.proxies || []) {
      if (item.id === createdId || item.is_active === false) continue;
      deactivated.push(item.id);
      const toggle = await page.request.post(`${baseURL}/legacy-api/admin/proxies/${item.id}/toggle`, {
        headers: {
          "X-Admin-Token": await page.evaluate(() => window.localStorage.getItem("kc_admin_token") || ""),
          "X-Trace-ID": `e2e-proxy-toggle-${item.id}`,
        },
      });
      assert.equal(toggle.ok(), true, `failed to deactivate ${item.id}`);
    }

    const beforeHits = refreshHits.length;
    const claim = await fetch(`${apiURL}/api/v1/internal/store/getActiveProxy`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Worker-Token": workerToken,
        "X-Trace-ID": `e2e-proxy-claim-${stamp}`,
      },
      body: "{}",
    });
    const claimText = await claim.text();
    assert.equal(claim.ok, true, `getActiveProxy HTTP ${claim.status} ${claimText}`);
    const claimed = JSON.parse(claimText);
    const claimedProxy = claimed.proxy || claimed.proxy_url || claimed.payload?.proxy;
    assert.equal(claimedProxy, proxyURL, `claimed proxy = ${JSON.stringify(claimed)}`);
    assert.ok(refreshHits.length > beforeHits, "task start did not request refresh URL");

    assert.equal(pageErrors.length, 0, `page errors: ${pageErrors.join("; ")}`);
    assert.equal(apiFailures.length, 0, `API 5xx: ${apiFailures.join("; ")}`);
    assert.equal(missingTraceIds.length, 0, `missing trace ids: ${missingTraceIds.join("; ")}`);
  } finally {
    const admin = await page.evaluate(() => window.localStorage.getItem("kc_admin_token") || "").catch(() => "");
    for (const id of deactivated) {
      await fetch(`${baseURL}/legacy-api/admin/proxies/${id}/toggle`, {
        method: "POST",
        headers: { "X-Admin-Token": admin, "X-Trace-ID": `e2e-proxy-restore-${id}` },
      }).catch(() => {});
    }
    if (createdId) {
      await fetch(`${baseURL}/legacy-api/admin/proxies/${createdId}`, {
        method: "DELETE",
        headers: { "X-Admin-Token": admin, "X-Trace-ID": `e2e-proxy-delete-${stamp}` },
      }).catch(() => {});
    }
    await browser.close();
    await close(refreshServer);
  }
}

run().catch((error) => {
  console.error(error);
  process.exit(1);
});
