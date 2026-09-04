import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "../backend/worker/node_modules/playwright/index.mjs";

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:23000").replace(/\/$/, "");
const adminEmail = process.env.E2E_ADMIN_EMAIL || "admin@example.com";
const adminPassword = process.env.E2E_ADMIN_PASSWORD || "admin123";

async function run() {
  const configuredExecutable = process.env.E2E_CHROMIUM_EXECUTABLE_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const launchOptions = { headless: true };
  if (existsSync(configuredExecutable)) launchOptions.executablePath = configuredExecutable;
  const browser = await chromium.launch(launchOptions);
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const pageErrors = [];
  const apiFailures = [];
  let productCode = "";

  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (!response.url().includes("/platform-api/") && !response.url().includes("/legacy-api/")) return;
    if (response.status() >= 500) apiFailures.push(`${response.status()} ${response.request().method()} ${response.url()}`);
  });

  async function waitForHeading(name) {
    await page.getByRole("heading", { name, exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });
  }

  async function unpublishTestProduct() {
    if (!productCode) return;
    try {
      await page.goto(`${baseURL}/admin/store_products`, { waitUntil: "domcontentloaded" });
      await waitForHeading("售卡商品");
      const row = page.locator("tbody tr").filter({ hasText: productCode }).first();
      if (await row.count() === 0) return;
      const unpublishButton = row.getByRole("button", { name: "下架", exact: true });
      if (await unpublishButton.count() === 0) return;
      const response = page.waitForResponse((item) =>
        item.url().includes(`/platform-api/admin/store/products/${productCode}/toggle`) &&
        item.request().method() === "POST" &&
        item.status() === 200,
      );
      await unpublishButton.click();
      await response;
    } catch {
      // Cleanup is best effort; the test result itself is reported below.
    }
  }

  try {
    await page.goto(`${baseURL}/admin-login`, { waitUntil: "domcontentloaded" });
    await page.locator("#admin-email").fill(adminEmail);
    await page.locator("#admin-password").fill(adminPassword);
    await page.getByRole("button", { name: "继续", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin`, { timeout: 15_000 });
    await waitForHeading("概览中心");

    await page.goto(`${baseURL}/admin/store_products`, { waitUntil: "domcontentloaded" });
    await waitForHeading("售卡商品");
    const formPanel = page.locator("section.panel").filter({ hasText: "发布售卡商品" }).first();
    productCode = `stock-e2e-${Date.now().toString(36)}`;
    await formPanel.locator("label").filter({ hasText: "商品编码" }).locator("input").fill(productCode);
    await formPanel.locator("label").filter({ hasText: "商品名称" }).locator("input").fill("库存闭环测试商品");
    await formPanel.locator("label").filter({ hasText: "价格" }).locator("input").fill("1");
    await formPanel.locator("label").filter({ hasText: "可售数量" }).locator("input").fill("1");
    await formPanel.locator("label").filter({ hasText: "排序" }).locator("input").fill("999");
    const publishedCheckbox = formPanel.locator('input[type="checkbox"]');
    if (await publishedCheckbox.isChecked()) await publishedCheckbox.uncheck();

    const createResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    await formPanel.getByRole("button", { name: "保存商品", exact: true }).click();
    await createResponse;
    await page.getByText(`商品 ${productCode} 已保存`, { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    let productRow = page.locator("tbody tr").filter({ hasText: productCode }).first();
    await productRow.waitFor({ state: "visible", timeout: 10_000 });
    assert.match(await productRow.innerText(), /已下架/, "new inventory test product was not initially unpublished");
    assert.match(await productRow.innerText(), /1 \/ 1/, "new inventory test product did not persist saleLimit=1");

    const publishResponse = page.waitForResponse((response) =>
      response.url().includes(`/platform-api/admin/store/products/${productCode}/toggle`) &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const publishListRefresh = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await productRow.getByRole("button", { name: "发布", exact: true }).click();
    await publishResponse;
    await publishListRefresh;
    await page.getByText(`商品 ${productCode} 已发布`, { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    productRow = page.locator("tbody tr").filter({ hasText: productCode }).first();
    await productRow.waitFor({ state: "visible", timeout: 10_000 });
    assert.match(await productRow.innerText(), /已发布/, "test product publish did not persist");

    const purchaseURL = `${baseURL}/recharge?tab=purchase&plan=${encodeURIComponent(productCode)}#plans`;
    const plansResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/plans") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await page.goto(purchaseURL, { waitUntil: "domcontentloaded" });
    await plansResponse;
    await page.locator("main.recharge-page").waitFor({ state: "visible" });
    const testPlan = page.locator(".purchase-plan").filter({ hasText: "库存闭环测试商品" }).first();
    await testPlan.waitFor({ state: "visible", timeout: 15_000 });
    assert.equal(await testPlan.isDisabled(), false, "published inventory test product was disabled before sale");
    assert.match(await testPlan.innerText(), /剩余 1 件/, "public storefront did not show one available item");

    await page.locator("#purchase-email").fill(`stock-e2e-${Date.now()}@example.com`);
    await page.locator("#purchase-phone").fill("13900001234");
    const purchaseResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/store/orders") &&
      response.request().method() === "POST" &&
      response.status() === 201,
    );
    await page.getByRole("button", { name: "购买并获取 CDK", exact: true }).click();
    const purchasePayload = await (await purchaseResponse).json();
    assert.equal(purchasePayload.debug, true, "inventory E2E requires STORE_DEBUG_MODE=1");
    assert.equal(purchasePayload.order?.status, "paid", "debug purchase did not settle as paid");
    assert.ok(String(purchasePayload.order?.cdkCode || "").trim(), "debug purchase did not issue a CDK");

    const publicPlans = await page.request.get(`${baseURL}/platform-api/plans`);
    assert.equal(publicPlans.status(), 200, "public plan refresh failed after purchase");
    const publicPlanPayload = await publicPlans.json();
    const soldProduct = publicPlanPayload.plans.find((item) => item.code === productCode);
    assert.ok(soldProduct, "sold inventory product disappeared from public plan response");
    assert.equal(soldProduct.saleLimit, 1, "saleLimit changed after purchase");
    assert.equal(soldProduct.soldCount, 1, "soldCount was not incremented after purchase");
    assert.equal(soldProduct.remainingQuantity, 0, "remainingQuantity was not reduced to zero");
    assert.equal(soldProduct.soldOut, true, "backend did not mark product soldOut");
    assert.equal(soldProduct.purchaseEnabled, false, "backend left sold-out product purchasable");
    assert.equal(soldProduct.availabilityLabel, "已售罄，补货中", "backend sold-out label is incorrect");

    await page.goto(purchaseURL, { waitUntil: "domcontentloaded" });
    await page.locator("main.recharge-page").waitFor({ state: "visible" });
    const soldOutPlan = page.locator(".purchase-plan").filter({ hasText: "库存闭环测试商品" }).first();
    await soldOutPlan.waitFor({ state: "visible", timeout: 15_000 });
    assert.equal(await soldOutPlan.isDisabled(), true, "sold-out product remained clickable in the storefront");
    assert.match(await soldOutPlan.innerText(), /已售罄，补货中/, "storefront did not show sold-out label");
    const purchaseButton = page.getByRole("button", { name: "已售罄，补货中", exact: true });
    assert.equal(await purchaseButton.isDisabled(), true, "main purchase button remained enabled after inventory reached zero");

    const secondPurchase = await page.request.post(`${baseURL}/platform-api/store/orders`, {
      headers: { "Content-Type": "application/json", "X-Trace-ID": `stock-e2e-${Date.now()}` },
      data: {
        planCode: productCode,
        email: `stock-e2e-second-${Date.now()}@example.com`,
        phoneCountryCode: "+86",
        phoneNumber: "13900004321",
      },
    });
    assert.equal(secondPurchase.status(), 409, `second purchase returned HTTP ${secondPurchase.status()}`);
    const secondPurchasePayload = await secondPurchase.json();
    assert.equal(secondPurchasePayload.message, "商品已售罄，补货中", "second purchase did not return the sold-out business error");

    await page.goto(`${baseURL}/admin/store_products`, { waitUntil: "domcontentloaded" });
    await waitForHeading("售卡商品");
    productRow = page.locator("tbody tr").filter({ hasText: productCode }).first();
    await productRow.waitFor({ state: "visible", timeout: 15_000 });
    assert.match(await productRow.innerText(), /0 \/ 1/, "admin inventory did not show remainingQuantity=0");
    assert.match(await productRow.innerText(), /已售罄，补货中/, "admin inventory did not show sold-out state");

    assert.deepEqual(apiFailures, [], `Go API returned server errors: ${apiFailures.join(", ")}`);
    assert.deepEqual(pageErrors, [], `browser page errors: ${pageErrors.join(" | ")}`);
    console.log(`Store inventory E2E passed: create -> publish -> purchase/CDK -> sold out -> UI disabled -> HTTP 409.`);
  } finally {
    await unpublishTestProduct();
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exitCode = 1;
});
