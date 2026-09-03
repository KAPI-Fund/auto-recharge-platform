import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "../backend/worker/node_modules/playwright/index.mjs";

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:23000").replace(/\/$/, "");

async function run() {
  const configuredExecutable = process.env.E2E_CHROMIUM_EXECUTABLE_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const launchOptions = { headless: true };
  if (existsSync(configuredExecutable)) launchOptions.executablePath = configuredExecutable;
  const browser = await chromium.launch(launchOptions);
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const missingTraceIds = [];
  const apiFailures = [];
  const pageErrors = [];
  const documentNavigations = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    if (request.isNavigationRequest() && request.resourceType() === "document") documentNavigations.push(request.url());
  });
  page.on("response", (response) => {
    if (!response.url().includes("/legacy-api/") && !response.url().includes("/platform-api/")) return;
    if (!response.headers()["x-trace-id"]) missingTraceIds.push(`${response.request().method()} ${response.url()}`);
    if (response.status() >= 500) apiFailures.push(`${response.status()} ${response.request().method()} ${response.url()}`);
  });

  async function assertStorefrontStyles(label) {
    const styles = await page.evaluate(() => {
      const main = document.querySelector("main.recharge-page");
      const primaryButton = document.querySelector("main.recharge-page .btn-primary");
      return {
        legacyStylesheetCount: document.querySelectorAll('link[href="/style.css"]').length,
        dynamicStorefrontCount: document.querySelectorAll('link[href="/storefront.css"]').length,
        bodyBackground: getComputedStyle(document.body).backgroundColor,
        mainBackground: main ? getComputedStyle(main).backgroundColor : "",
        primaryButton: primaryButton ? getComputedStyle(primaryButton).backgroundColor : "",
      };
    });
    assert.equal(styles.legacyStylesheetCount, 0, `${label} still loads the legacy blue stylesheet`);
    assert.equal(styles.dynamicStorefrontCount, 0, `${label} still injects a dynamic storefront stylesheet`);
    assert.equal((await page.locator("body").innerText()).includes("当前账单地区："), false, `${label} still exposes the billing region hint`);
    if (styles.mainBackground) assert.equal(styles.mainBackground, "rgb(243, 246, 252)", `${label} has the wrong page background`);
    if (styles.primaryButton) {
      assert.equal(styles.primaryButton, "rgb(231, 111, 81)", `${label} has the wrong primary button color`);
      assert.notEqual(styles.primaryButton, "rgb(15, 118, 110)", `${label} regressed to the old green theme`);
    }
  }

  try {
    const homePlansLoad = page.waitForResponse((response) =>
      response.url().includes("/platform-api/plans") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await page.goto(`${baseURL}/`, { waitUntil: "domcontentloaded" });
    await homePlansLoad;
    await assertStorefrontStyles("homepage");
    assert.equal(await page.locator(".public-runtime-panel").count(), 0, "homepage still exposes runtime metrics");
    assert.ok(await page.locator(".hero-price-card").count() > 0, "homepage did not render API-backed plan cards");

    const headerDocumentsBefore = documentNavigations.length;
    await page.locator(".public-header-cta").click();
    await page.waitForURL(/\/recharge\?tab=purchase$/);
    await page.locator("main.recharge-page").waitFor({ state: "visible" });
    await page.locator(".purchase-plan").first().waitFor({ state: "visible" });
    await assertStorefrontStyles("header purchase CTA");
    assert.equal(await page.locator(".plan-showcase").count(), 0, "purchase mode still renders the duplicate outer product cards");
    assert.ok(await page.locator(".purchase-plans .purchase-plan").count() > 0, "purchase mode did not render its product choices");
    assert.equal(await page.locator(".faq-panel").evaluate((element) => element.open), true, "FAQ panel is not expanded by default");
    assert.equal(documentNavigations.length, headerDocumentsBefore, "header purchase CTA performed a document navigation");

    await page.goto(`${baseURL}/`, { waitUntil: "domcontentloaded" });
    await page.locator(".hero-price-card").first().waitFor({ state: "visible" });
    const heroDocumentsBefore = documentNavigations.length;
    await page.locator(".hero-price-card").first().click();
    await page.waitForURL(/\/recharge\?tab=purchase&plan=/);
    await page.locator("main.recharge-page").waitFor({ state: "visible" });
    await assertStorefrontStyles("hero purchase CTA");
    assert.equal(await page.locator(".plan-showcase").count(), 0, "selected purchase mode still renders the duplicate outer product cards");
    assert.equal(documentNavigations.length, heroDocumentsBefore, "hero purchase CTA performed a document navigation");
    await page.locator(".purchase-plan.selected").waitFor({ state: "visible" });
    await page.locator(".purchase-plan").nth(1).click();
    await page.waitForURL(/\/recharge\?tab=purchase&plan=pro_5x#plans/);
    assert.match(await page.locator(".purchase-plan.selected").innerText(), /Pro 5x/);
    await page.getByRole("button", { name: "购买卡密", exact: true }).click();

    const suffix = Date.now().toString().slice(-8);
    const email = `public-e2e-${suffix}@example.com`;
    const phone = `139${suffix}`;
    await page.locator("#purchase-email").fill(email);
    await page.locator("#purchase-phone").fill(phone);
    const orderResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/store/orders") &&
      response.request().method() === "POST" &&
      response.status() === 201,
    );
    await page.getByRole("button", { name: "购买并获取 CDK", exact: true }).click();
    const orderPayload = await (await orderResponse).json();
    assert.equal(orderPayload.debug, true, "public E2E requires STORE_DEBUG_MODE=1");
    const cdkCode = String(orderPayload.order?.cdkCode || "");
    assert.ok(cdkCode, "debug purchase did not return a CDK");
    const redeemedCodeInput = page.locator("#cdkInput");
    await redeemedCodeInput.waitFor({ state: "visible" });
    assert.equal(await page.locator("label[for=cdkInput]").innerText(), "输入您的充值码", "redeem label is still tied to Plus");
    assert.equal(await page.locator(".tab-btn.active").innerText(), "兑换开通", "completed purchase did not return to the redeem step");
    assert.equal(new URL(page.url()).searchParams.get("tab"), null, "completed purchase did not clear the purchase tab from the URL");
    assert.equal(await redeemedCodeInput.inputValue(), cdkCode, "debug purchase did not fill the redeem input");

    const verifyResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/recharge/verify") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    await page.getByRole("button", { name: "兑换卡密", exact: true }).click();
    await verifyResponse;
    await page.getByText(/兑换码有效/).waitFor({ state: "visible" });
    await page.locator("#tokenInput").waitFor({ state: "visible" });
    const sessionGuide = page.locator(".session-guide");
    assert.equal(await sessionGuide.count(), 1, "redeem flow did not render the Session guide");
    assert.match(await sessionGuide.innerText(), /请先登录 ChatGPT.*官方 Session 页面/s, "Session guide is missing the first instruction");
    assert.match(await sessionGuide.innerText(), /全选复制页面中的完整 JSON.*返回本页粘贴/s, "Session guide is missing the return-and-paste instruction");
    const sessionLink = sessionGuide.getByRole("link", { name: /打开 Session 页面/ });
    assert.equal(await sessionLink.getAttribute("href"), "https://chatgpt.com/api/auth/session", "Session guide points to the wrong endpoint");
    assert.equal(await sessionLink.getAttribute("target"), "_blank", "Session guide should open the official endpoint in a new tab");
    const sessionPopupPromise = page.waitForEvent("popup");
    await sessionLink.click();
    const sessionPopup = await sessionPopupPromise;
    assert.equal(new URL(sessionPopup.url()).href, "https://chatgpt.com/api/auth/session", "Session guide opened the wrong page");
    assert.match(page.url(), /\/recharge/, "opening Session guide navigated away from the redeem page");
    await sessionPopup.close();
    await page.waitForTimeout(1800);
    assert.equal(await page.locator("#tokenInput").count(), 1, "order polling cleared the verified Session form");

    await page.locator("#tokenInput").fill("not-a-session");
    const invalidTaskResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/recharge/tasks") &&
      response.request().method() === "POST",
    );
    await page.getByRole("button", { name: "确认并启动自动化开通", exact: true }).click();
    const invalidTask = await invalidTaskResponse;
    assert.equal(invalidTask.status(), 400, `invalid Session request returned HTTP ${invalidTask.status()}`);
    const invalidTaskPayload = await invalidTask.json();
    assert.equal(invalidTaskPayload.success, false, "Go accepted an invalid Session");

    // The tab and submit controls share the same accessible name.
    await page.getByRole("button", { name: "查询状态", exact: true }).first().click();
    await page.locator("#queryInput").fill(cdkCode);
    const queryResponse = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/cdk/query") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await page.getByRole("button", { name: "查询状态", exact: true }).last().click();
    await queryResponse;
    await page.getByText("自助激活码", { exact: true }).waitFor({ state: "visible" });

    const bodyText = await page.locator("body").innerText();
    assert.equal(bodyText.includes("[object Object]"), false, "public page contains [object Object]");
    assert.deepEqual(apiFailures, [], `Go API returned server errors: ${apiFailures.join(", ")}`);
    assert.deepEqual(missingTraceIds, [], `Go API responses missing X-Trace-ID: ${missingTraceIds.join(", ")}`);
    assert.deepEqual(pageErrors, [], `browser page errors: ${pageErrors.join(" | ")}`);
    console.log(`Public E2E passed: debug purchase -> CDK verify -> status query, trace IDs present.`);
  } finally {
    await page.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exitCode = 1;
});
