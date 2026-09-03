import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "../backend/worker/node_modules/playwright/index.mjs";

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:23000").replace(/\/$/, "");
const adminEmail = process.env.E2E_ADMIN_EMAIL || "admin@example.com";
const adminPassword = process.env.E2E_ADMIN_PASSWORD || "admin123";

const routeCases = [
  ["/admin", "概览中心"],
  ["/admin/settings", "系统配置"],
  ["/admin/proxies", "代理池"],
  ["/admin/browser_pool", "浏览器池"],
  ["/admin/tax_addresses", "免税地址"],
  ["/admin/checkout_debug", "支付链接调试"],
  ["/admin/cards", "银行卡池管理"],
  ["/admin/cdks", "CDK 激活码管理"],
  ["/admin/store_products", "售卡商品"],
  ["/admin/pool_emails", "邮箱池"],
  ["/admin/phones", "手机号池"],
  ["/admin/products", "成品号库"],
  ["/admin/sessions", "Session 管理"],
  ["/admin/cancel_renewal", "续费管理"],
  ["/admin/logs", "任务管理"],
  ["/admin/automation_tasks", "自动化任务"],
  ["/admin/billing", "账单记录"],
  ["/admin/runtime_logs", "运行日志"],
  ["/admin/admin_login_logs", "后台登录日志"],
];

const menuNavigationCases = [
  ["/admin/settings", "系统配置", "系统配置", false],
  ["/admin/proxies", "代理池", "代理池", false],
  ["/admin/browser_pool", "浏览器池", "浏览器池", false],
  ["/admin/tax_addresses", "免税地址", "免税地址", false],
  ["/admin/checkout_debug", "支付链接调试", "支付链接调试", true],
  ["/admin/cards", "银行卡池", "银行卡池管理", true],
  ["/admin/cdks", "CDK 管理", "CDK 激活码管理", true],
  ["/admin/store_products", "售卡商品", "售卡商品", true],
  ["/admin/sessions", "Session 管理", "Session 管理", true],
  ["/admin/billing", "账单记录", "账单记录", true],
  ["/admin/cancel_renewal", "续费管理", "续费管理", false],
  ["/admin/logs", "任务管理", "任务管理", false],
  ["/admin/automation_tasks", "自动化任务", "自动化任务", false],
  ["/admin/runtime_logs", "运行日志", "运行日志", false],
  ["/admin/admin_login_logs", "登录日志", "后台登录日志", false],
  ["/admin", "概览中心", "概览中心", false],
];

async function assertNoPageError(page, route) {
  const text = await page.locator("body").innerText();
  assert.equal(text.includes("[object Object]"), false, `${route} contains [object Object]`);
  assert.equal(text.includes("请求失败，请稍后重试"), false, `${route} contains a request failure`);
  const errorAlert = page.locator(".admin-message-container[role=alert]");
  assert.equal(await errorAlert.count(), 0, `${route} contains an admin error alert: ${await errorAlert.allTextContents()}`);
}

async function ensureDetailsOpen(section) {
  if ((await section.getAttribute("open")) === null) {
    await section.locator("summary").click();
  }
}

async function run() {
  const configuredExecutable = process.env.E2E_CHROMIUM_EXECUTABLE_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const launchOptions = { headless: true };
  if (existsSync(configuredExecutable)) launchOptions.executablePath = configuredExecutable;
  const browser = await chromium.launch(launchOptions);
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const apiFailures = [];
  const missingTraceIds = [];
  const pageErrors = [];
  let loadEvents = 0;
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("load", () => { loadEvents += 1; });
  page.on("response", (response) => {
    const url = response.url();
    if (!url.includes("/legacy-api/") && !url.includes("/platform-api/")) return;
    const traceID = response.headers()["x-trace-id"];
    if (!traceID) missingTraceIds.push(`${response.request().method()} ${url}`);
    if (response.status() >= 500) apiFailures.push(`${response.status()} ${response.request().method()} ${url}`);
  });

  try {
    await page.goto(`${baseURL}/admin-login`, { waitUntil: "domcontentloaded" });
    await page.locator("#admin-email").waitFor({ state: "visible", timeout: 10_000 });
    await page.locator("#admin-email").fill(adminEmail);
    await page.locator("#admin-password").fill(adminPassword);
    await page.getByRole("button", { name: "继续", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin`, { timeout: 15_000 });
    await page.getByRole("heading", { name: "概览中心", exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });
    assert.equal(await page.getByRole("button", { name: "退出登录", exact: true }).count(), 1, "admin session was not established");
    await page.evaluate(() => {
      window.__adminShellIdentity = {
        sidebar: document.querySelector(".sidebar"),
        main: document.querySelector("main.admin-console"),
      };
    });

    for (const [path, label, heading, business] of menuNavigationCases) {
      const menuLink = page.getByRole("link", { name: label, exact: true });
      if (business && await menuLink.count() === 0) {
        await page.getByRole("button", { name: "支付与资产", exact: true }).click();
      }
      await menuLink.waitFor({ state: "visible", timeout: 10_000 });
      await menuLink.hover();
      const hoverTextDecoration = await menuLink.evaluate((element) => getComputedStyle(element).textDecorationLine);
      assert.equal(hoverTextDecoration, "none", `${label} has an underline on hover`);
      const loadEventsBeforeMenuSwitch = loadEvents;
      await menuLink.click();
      await page.waitForURL(`${baseURL}${path}`, { timeout: 15_000 });
      await page.getByRole("heading", { name: heading, exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });
      assert.equal(loadEvents, loadEventsBeforeMenuSwitch, `${label} triggered a full page load`);
      assert.equal(await page.locator(".sidebar").count(), 1, `${label} replaced the sidebar during client navigation`);
      const shellPreserved = await page.evaluate(() => {
        const identity = window.__adminShellIdentity;
        return identity?.sidebar === document.querySelector(".sidebar") &&
          identity.main === document.querySelector("main.admin-console");
      });
      assert.equal(shellPreserved, true, `${label} remounted the admin shell during client navigation`);
    }

    await page.getByRole("link", { name: "系统配置", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin/settings`);
    await page.getByRole("heading", { name: "系统配置", exact: true, level: 1 }).waitFor({ state: "visible" });
    await page.goto(`${baseURL}/admin`);
    await page.getByRole("button", { name: "支付与资产", exact: true }).click();
    await page.getByRole("link", { name: "售卡商品", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin/store_products`);
    await page.getByRole("heading", { name: "售卡商品", exact: true, level: 1 }).waitFor({ state: "visible" });

    for (const [route, heading] of routeCases) {
      await page.goto(`${baseURL}${route}`, { waitUntil: "domcontentloaded" });
      await page.getByRole("heading", { name: heading, exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });
      await page.waitForTimeout(250);
      await assertNoPageError(page, route);
    }

    await page.goto(`${baseURL}/admin/settings`, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "系统配置", exact: true, level: 1 }).waitFor({ state: "visible", timeout: 15_000 });
    await page.getByRole("button", { name: "绑定 Google Authenticator", exact: true }).click();
    const totpBox = page.locator(".config-totp-box");
    await totpBox.waitFor({ state: "visible", timeout: 15_000 });
    const qrImage = totpBox.locator("img[alt=\"Google Authenticator QR\"]");
    assert.equal(await qrImage.count(), 1, "TOTP setup did not render a QR image");
    assert.equal(await qrImage.isVisible(), true, "TOTP QR image is not visible");
    assert.match(await qrImage.getAttribute("src"), /api\.qrserver\.com\/v1\/create-qr-code\//, "TOTP QR image does not use the Go API response URL");
    assert.equal(await totpBox.getByText("请用 Google Authenticator 扫描上方二维码", { exact: true }).count(), 1, "TOTP scan instruction is missing");
    const totpSecretText = await totpBox.locator(".config-totp-secret").innerText();
    assert.match(totpSecretText, /手动密钥：/, "TOTP manual-key label is missing");
    const totpCode = totpBox.getByPlaceholder("输入 Authenticator 6 位码确认", { exact: true });
    assert.equal(await totpCode.getAttribute("type"), "text", "TOTP confirmation input type changed");
    assert.equal(await totpCode.getAttribute("inputmode"), "numeric", "TOTP confirmation input mode changed");
    assert.equal(await totpCode.getAttribute("maxlength"), "6", "TOTP confirmation input max length changed");
    assert.equal(await totpBox.getByRole("button", { name: "确认启用", exact: true }).count(), 1, "TOTP confirmation button is missing");
    await assertNoPageError(page, "/admin/settings after TOTP setup");

    const hiddenProviderConfigs = [
      ["Airwallex", page.getByTestId("airwallex-config")],
      ["Stripe Issuing", page.getByTestId("stripe-issuing-config")],
      ["PhotonPay", page.getByTestId("photonpay-config")],
      ["DogPay", page.getByTestId("dogpay-config")],
    ];
    const kimooxOnlyConfig = (await Promise.all(hiddenProviderConfigs.map(([, section]) => section.isHidden()))).every(Boolean);
    assert.equal(kimooxOnlyConfig, true, "non-Kimoox external Provider configuration is visible");
    if (kimooxOnlyConfig) {
      for (const [name, section] of hiddenProviderConfigs) {
        assert.notEqual(await section.getAttribute("hidden"), null, `${name} config must remain hidden`);
      }

      const localTextConfig = page.getByTestId("local-text-config");
      const kimooxConfig = page.getByTestId("kimoox-config");
      const visibleProviderSections = [["LOCAL_TEXT", localTextConfig], ["Kimoox", kimooxConfig]];
      await localTextConfig.waitFor({ state: "visible", timeout: 15_000 });
      await kimooxConfig.waitFor({ state: "visible", timeout: 15_000 });
      for (const [name, section] of visibleProviderSections) {
        assert.equal(await section.getAttribute("open"), null, `${name} config section should be collapsed by default`);
        await section.locator("summary").click();
        assert.notEqual(await section.getAttribute("open"), null, `${name} config section did not expand`);
        assert.equal(await section.locator("summary").getAttribute("aria-expanded"), "true", `${name} config summary should report expanded state`);
        assert.equal(await section.locator(".config-provider-content").isVisible(), true, `${name} config content did not become visible`);
        for (const [otherName, otherSection] of visibleProviderSections) {
          if (otherName === name) continue;
          assert.equal(await otherSection.getAttribute("open"), null, `${otherName} stayed open after expanding ${name}`);
        }
      }
      assert.equal(await localTextConfig.getByText("LOCAL_TEXT 不需要外部 API 凭据。", { exact: false }).count(), 1, "LOCAL_TEXT help is missing");
      assert.equal(await page.getByRole("heading", { name: "Kimoox 发卡配置", exact: true, level: 3 }).count(), 1, "Kimoox config heading is missing");
      assert.equal(await page.getByRole("button", { name: "保存全局配置", exact: true }).count(), 0, "duplicate global config save button is visible");

      const executionPanel = page.locator("section.panel").filter({ hasText: "并发与维护" }).first();
      const executionSaveButton = executionPanel.getByRole("button", { name: "保存并发与维护配置", exact: true });
      const queuedTimeout = executionPanel.getByRole("spinbutton", { name: /排队超时（秒）/ });
      assert.equal(await executionSaveButton.isEnabled(), true, "execution config save button is disabled");
      const originalQueuedTimeout = await queuedTimeout.inputValue();
      const temporaryQueuedTimeout = String(Number(originalQueuedTimeout) === 86400 ? 86399 : Number(originalQueuedTimeout) + 1);
      await queuedTimeout.fill(temporaryQueuedTimeout);
      const saveExecutionRequest = page.waitForRequest((request) => request.url().includes("/legacy-api/admin/config") && request.method() === "POST");
      const saveExecutionResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
      const saveExecutionReload = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "GET" && response.status() === 200);
      await executionSaveButton.click();
      const executionRequestBody = JSON.parse((await saveExecutionRequest).postData() || "{}");
      assert.equal(executionRequestBody.recharge_queued_timeout_seconds, temporaryQueuedTimeout, "execution save omitted queue timeout");
      assert.equal(Object.hasOwn(executionRequestBody, "card_pool_default_provider"), false, "execution save sent Provider settings");
      await Promise.all([saveExecutionResponse, saveExecutionReload]);
      assert.equal(await queuedTimeout.inputValue(), temporaryQueuedTimeout, "queue timeout was not retained after save");
      await queuedTimeout.fill(originalQueuedTimeout);
      const restoreExecutionResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
      const restoreExecutionReload = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "GET" && response.status() === 200);
      await executionSaveButton.click();
      await Promise.all([restoreExecutionResponse, restoreExecutionReload]);
      assert.equal(await queuedTimeout.inputValue(), originalQueuedTimeout, "queue timeout did not restore");

      const providerConfigPanel = page.locator("section.panel").filter({ has: kimooxConfig }).first();
      const routingSelect = providerConfigPanel.getByRole("combobox", { name: "路由策略", exact: true });
      const defaultProviderSelect = providerConfigPanel.getByRole("combobox", { name: "默认 Provider", exact: true });
      const creationModeSelect = providerConfigPanel.locator("label").filter({ hasText: "充值卡创建模式" }).locator("select");
      assert.deepEqual(await defaultProviderSelect.locator("option").evaluateAll((options) => options.map((option) => option.value)), ["", "LOCAL_TEXT", "KIMOOX"], "default Provider contains hidden options");
      assert.deepEqual(await creationModeSelect.locator("option").evaluateAll((options) => options.map((option) => option.value)), ["POOL_ONLY", "CREATE_ON_DEMAND"], "card creation mode options changed");
      const originalRouting = await routingSelect.inputValue();
      const originalDefaultProvider = await defaultProviderSelect.inputValue();
      assert.ok(["LOCAL_TEXT", "KIMOOX"].includes(originalDefaultProvider), `unexpected visible default Provider ${originalDefaultProvider}`);
      const toggleInput = (label) => providerConfigPanel.locator(".toggle-field").filter({ hasText: label }).locator('input[type="checkbox"]');
      const localToggle = toggleInput("启用 LOCAL_TEXT");
      const kimooxToggle = toggleInput("启用 KIMOOX");
      assert.equal(await localToggle.count(), 1, "LOCAL_TEXT toggle is missing");
      assert.equal(await kimooxToggle.count(), 1, "Kimoox toggle is missing");
      const originalLocalEnabled = await localToggle.isChecked();
      const originalKimooxEnabled = await kimooxToggle.isChecked();
      const originalDefaultToggle = originalDefaultProvider === "LOCAL_TEXT" ? localToggle : kimooxToggle;
      const alternateProvider = originalDefaultProvider === "LOCAL_TEXT" ? "KIMOOX" : "LOCAL_TEXT";
      const alternateToggle = alternateProvider === "LOCAL_TEXT" ? localToggle : kimooxToggle;
      const alternateOption = defaultProviderSelect.locator(`option[value="${alternateProvider}"]`);
      assert.equal(await originalDefaultToggle.isEnabled(), false, "default Provider can be disabled");
      if (!await alternateToggle.isChecked()) await alternateToggle.check();
      assert.equal(await alternateOption.isDisabled(), false, "enabled Provider is not selectable");
      await defaultProviderSelect.selectOption(alternateProvider);
      assert.equal(await defaultProviderSelect.inputValue(), alternateProvider, "default Provider did not switch in UI");
      assert.equal(await alternateToggle.isEnabled(), false, "new default Provider can be disabled");
      await defaultProviderSelect.selectOption(originalDefaultProvider);
      await localToggle.setChecked(originalLocalEnabled);
      await kimooxToggle.setChecked(originalKimooxEnabled);
      assert.equal(await defaultProviderSelect.inputValue(), originalDefaultProvider, "default Provider did not restore");

      await ensureDetailsOpen(kimooxConfig);
      const kimooxBaseURL = kimooxConfig.getByRole("textbox", { name: "Kimoox Base URL", exact: true });
      const kimooxAPIKey = kimooxConfig.getByRole("textbox", { name: "API Key", exact: true });
      const kimooxAPISecret = kimooxConfig.getByRole("textbox", { name: "API Secret", exact: true });
      assert.equal(await kimooxBaseURL.isEnabled(), true, "Kimoox Base URL is disabled");
      assert.equal(await kimooxAPIKey.getAttribute("type"), "password", "Kimoox API Key is not protected");
      assert.equal(await kimooxAPISecret.getAttribute("type"), "password", "Kimoox API Secret is not protected");
      const cardType = kimooxConfig.getByRole("combobox", { name: /卡类型/ });
      const cardGroupID = kimooxConfig.getByRole("textbox", { name: /Card Group ID/ });
      const budgetID = kimooxConfig.getByRole("textbox", { name: /Budget ID/ });
      const originalCardType = await cardType.inputValue();
      await cardType.selectOption("BUDGET");
      assert.equal(await cardGroupID.isEnabled(), true, "Card Group ID is disabled for BUDGET cards");
      assert.equal(await budgetID.isEnabled(), true, "Budget ID is disabled for BUDGET cards");
      await cardType.selectOption("PREPAID");
      assert.equal(await cardGroupID.isEnabled(), false, "Card Group ID is enabled for PREPAID cards");
      assert.equal(await budgetID.isEnabled(), false, "Budget ID is enabled for PREPAID cards");
      await cardType.selectOption(originalCardType);
      assert.equal(await kimooxConfig.getByText("当前仅支持新的多 BIN 配置字段。", { exact: false }).count(), 1, "multi-BIN configuration help is missing");

      const pollAttempts = kimooxConfig.getByRole("spinbutton", { name: "开卡轮询次数", exact: true });
      const originalPollAttempts = await pollAttempts.inputValue();
      const temporaryPollAttempts = String(Number(originalPollAttempts) === 300 ? 299 : Number(originalPollAttempts) + 1);
      await pollAttempts.fill(temporaryPollAttempts);
      const saveProviderRequest = page.waitForRequest((request) => request.url().includes("/legacy-api/admin/config") && request.method() === "POST");
      const saveProviderResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
      const saveProviderReload = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "GET" && response.status() === 200);
      await providerConfigPanel.getByRole("button", { name: "保存卡池与 Provider 配置", exact: true }).click();
      const providerRequestBody = JSON.parse((await saveProviderRequest).postData() || "{}");
      assert.equal(providerRequestBody.kimoox_apply_poll_attempts, temporaryPollAttempts, "Provider save omitted Kimoox polling value");
      assert.equal(providerRequestBody.card_pool_default_provider, originalDefaultProvider, "Provider save changed the restored default Provider");
      assert.equal(Object.hasOwn(providerRequestBody, "emailEnabled"), false, "Provider save sent email settings");
      await Promise.all([saveProviderResponse, saveProviderReload]);
      await ensureDetailsOpen(kimooxConfig);
      assert.equal(await pollAttempts.inputValue(), temporaryPollAttempts, "Kimoox polling value was not retained");
      await pollAttempts.fill(originalPollAttempts);
      const restoreProviderResponse = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "POST" && response.status() === 200);
      const restoreProviderReload = page.waitForResponse((response) => response.url().includes("/legacy-api/admin/config") && response.request().method() === "GET" && response.status() === 200);
      await providerConfigPanel.getByRole("button", { name: "保存卡池与 Provider 配置", exact: true }).click();
      await Promise.all([restoreProviderResponse, restoreProviderReload]);
      await ensureDetailsOpen(kimooxConfig);
      assert.equal(await pollAttempts.inputValue(), originalPollAttempts, "Kimoox polling value did not restore");
      assert.equal(await routingSelect.inputValue(), originalRouting, "routing strategy changed unexpectedly");
      assert.equal(await defaultProviderSelect.inputValue(), originalDefaultProvider, "default Provider changed unexpectedly");
      await assertNoPageError(page, "/admin/settings after Kimoox Provider config E2E");
    } else {
    const cardPoolPanel = page.locator('[data-testid="stripe-issuing-config"]').locator("..");
    const localTextConfig = page.getByTestId("local-text-config");
    const airwallexConfig = page.getByTestId("airwallex-config");
    const stripeIssuingConfig = page.getByTestId("stripe-issuing-config");
    const photonpayConfig = page.getByTestId("photonpay-config");
    const dogpayConfig = page.getByTestId("dogpay-config");
    const kimooxConfig = page.getByTestId("kimoox-config");
    const providerConfigSections = [
      ["LOCAL_TEXT", localTextConfig],
      ["Airwallex", airwallexConfig],
      ["Stripe Issuing", stripeIssuingConfig],
      ["PhotonPay", photonpayConfig],
      ["DogPay", dogpayConfig],
      ["Kimoox", kimooxConfig],
    ];
    await localTextConfig.waitFor({ state: "visible", timeout: 15_000 });
    await airwallexConfig.waitFor({ state: "visible", timeout: 15_000 });
    await stripeIssuingConfig.waitFor({ state: "visible", timeout: 15_000 });
    await photonpayConfig.waitFor({ state: "visible", timeout: 15_000 });
    await dogpayConfig.waitFor({ state: "visible", timeout: 15_000 });
    await kimooxConfig.waitFor({ state: "visible", timeout: 15_000 });
    for (const [name, section] of providerConfigSections) {
      assert.equal(await section.getAttribute("open"), null, `${name} config section should be collapsed by default`);
      assert.equal(await section.locator("summary").getAttribute("aria-expanded"), "false", `${name} config summary should report collapsed state`);
      assert.equal(await section.locator(".config-provider-content").isVisible(), false, `${name} config content should not occupy visible space while collapsed`);
      await section.locator("summary").click();
      assert.notEqual(await section.getAttribute("open"), null, `${name} config section did not expand`);
      assert.equal(await section.locator("summary").getAttribute("aria-expanded"), "true", `${name} config summary should report expanded state`);
      assert.equal(await section.locator(".config-provider-content").isVisible(), true, `${name} config content did not become visible after expanding`);
      for (const [otherName, otherSection] of providerConfigSections) {
        if (otherName === name) continue;
        assert.equal(await otherSection.getAttribute("open"), null, `${otherName} config section stayed open after expanding ${name}`);
        assert.equal(await otherSection.locator("summary").getAttribute("aria-expanded"), "false", `${otherName} config summary should report collapsed state after expanding ${name}`);
        assert.equal(await otherSection.locator(".config-provider-content").isVisible(), false, `${otherName} config content should stay hidden after expanding ${name}`);
      }
    }
    assert.equal(await localTextConfig.getByText("LOCAL_TEXT 不需要外部 API 凭据。", { exact: false }).count(), 1, "LOCAL_TEXT configuration help is missing");
    assert.equal(await page.getByRole("heading", { name: "Airwallex 发卡配置", exact: true, level: 3 }).count(), 1, "Airwallex config section heading is missing");
    assert.equal(await page.getByRole("heading", { name: "Stripe Issuing 发卡配置", exact: true, level: 3 }).count(), 1, "Stripe Issuing config section heading is missing");
    assert.equal(await page.getByRole("heading", { name: "PhotonPay 发卡配置", exact: true, level: 3 }).count(), 1, "PhotonPay config section heading is missing");
    assert.equal(await page.getByRole("heading", { name: "DogPay 发卡配置", exact: true, level: 3 }).count(), 1, "DogPay config section heading is missing");
    assert.equal(await page.getByRole("heading", { name: "Kimoox 发卡配置", exact: true, level: 3 }).count(), 1, "Kimoox config section heading is missing");
    assert.equal(await page.getByRole("button", { name: "保存全局配置", exact: true }).count(), 0, "duplicate global config save button is still visible");
    const executionPanel = page.locator("section.panel").filter({ hasText: "并发与维护" }).first();
    const executionSaveButton = executionPanel.getByRole("button", { name: "保存并发与维护配置", exact: true });
    assert.equal(await executionSaveButton.count(), 1, "execution config save button is missing");
    assert.equal(await executionSaveButton.isEnabled(), true, "execution config save button is disabled");
    const queuedTimeout = executionPanel.getByRole("spinbutton", { name: /排队超时（秒）/ });
    const originalQueuedTimeout = await queuedTimeout.inputValue();
    const temporaryQueuedTimeout = String(Number(originalQueuedTimeout) === 86400 ? 86399 : Number(originalQueuedTimeout) + 1);
    await queuedTimeout.fill(temporaryQueuedTimeout);
    const saveExecutionRequest = page.waitForRequest((request) =>
      request.url().includes("/legacy-api/admin/config") &&
      request.method() === "POST",
    );
    const saveExecutionResponse = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const saveExecutionReload = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await executionSaveButton.click();
    const executionRequestBody = JSON.parse((await saveExecutionRequest).postData() || "{}");
    assert.equal(executionRequestBody.recharge_queued_timeout_seconds, temporaryQueuedTimeout, "execution save did not send the edited queue timeout");
    assert.equal(Object.hasOwn(executionRequestBody, "card_pool_default_provider"), false, "execution save sent provider settings");
    await Promise.all([saveExecutionResponse, saveExecutionReload]);
    await page.getByText("并发与维护配置已保存", { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    assert.equal(await queuedTimeout.inputValue(), temporaryQueuedTimeout, "queue timeout was not retained after save");
    await queuedTimeout.fill(originalQueuedTimeout);
    const restoreExecutionResponse = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const restoreExecutionReload = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await executionSaveButton.click();
    await Promise.all([restoreExecutionResponse, restoreExecutionReload]);
    await page.getByText("并发与维护配置已保存", { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    assert.equal(await queuedTimeout.inputValue(), originalQueuedTimeout, "queue timeout did not restore");

    const routingSelect = page.getByRole("combobox", { name: "路由策略", exact: true });
    const defaultProviderSelect = page.getByRole("combobox", { name: "默认 Provider", exact: true });
    const originalRouting = await routingSelect.inputValue();
    const originalDefaultProvider = await defaultProviderSelect.inputValue();
    const toggleInput = (label) => cardPoolPanel.locator(".toggle-field").filter({ hasText: label }).locator('input[type="checkbox"]');
    const providerToggles = [
      ["启用 LOCAL_TEXT", toggleInput("启用 LOCAL_TEXT")],
      ["启用 AIRWALLEX", toggleInput("启用 AIRWALLEX")],
      ["启用 STRIPE_ISSUING", toggleInput("启用 STRIPE_ISSUING")],
      ["启用 PHOTONPAY", toggleInput("启用 PHOTONPAY")],
      ["启用 DOGPAY", toggleInput("启用 DOGPAY")],
      ["启用 KIMOOX", toggleInput("启用 KIMOOX")],
    ];
    const originalToggleStates = [];
    for (const [label, input] of providerToggles) {
      assert.equal(await input.count(), 1, `${label} control is missing`);
      const isCurrentDefault = label === `启用 ${originalDefaultProvider}`;
      assert.equal(await input.isEnabled(), !isCurrentDefault, `${label} default-provider lock state is incorrect`);
      originalToggleStates.push(await input.isChecked());
      if (isCurrentDefault) continue;
      await input.click();
      assert.notEqual(await input.isChecked(), originalToggleStates.at(-1), `${label} did not toggle`);
      await input.click();
      assert.equal(await input.isChecked(), originalToggleStates.at(-1), `${label} did not restore after toggle`);
    }
    await ensureDetailsOpen(airwallexConfig);
    const airwallexActivation = toggleInput("Airwallex 发卡后自动激活");
    assert.equal(await airwallexActivation.count(), 1, "Airwallex activation switch is missing");
    assert.equal(await airwallexActivation.isEnabled(), true, "Airwallex activation switch is disabled");
    const originalActivation = await airwallexActivation.isChecked();
    await airwallexActivation.click();
    assert.notEqual(await airwallexActivation.isChecked(), originalActivation, "Airwallex activation switch did not toggle");
    await airwallexActivation.click();
    assert.equal(await airwallexActivation.isChecked(), originalActivation, "Airwallex activation switch did not restore");
    assert.equal(await routingSelect.isEnabled(), true, "routing strategy select is disabled");
    await routingSelect.selectOption("FAILOVER");
    assert.equal(await routingSelect.inputValue(), "FAILOVER", "routing strategy did not select FAILOVER");
    assert.equal(await defaultProviderSelect.isEnabled(), true, "default Provider select is disabled");
    const temporaryProvider = originalDefaultProvider === "AIRWALLEX" ? "STRIPE_ISSUING" : "AIRWALLEX";
    const temporaryProviderOption = defaultProviderSelect.locator(`option[value="${temporaryProvider}"]`);
    const temporaryProviderToggle = toggleInput(`启用 ${temporaryProvider}`);
    const temporaryProviderInitiallyEnabled = await temporaryProviderToggle.isChecked();
    assert.equal((await temporaryProviderOption.getAttribute("disabled")) !== null, !temporaryProviderInitiallyEnabled, `${temporaryProvider} option is not linked to its enable switch`);
    if (!temporaryProviderInitiallyEnabled) await temporaryProviderToggle.setChecked(true);
    assert.equal(await temporaryProviderToggle.isChecked(), true, `${temporaryProvider} did not enable`);
    assert.equal(await temporaryProviderOption.getAttribute("disabled"), null, `${temporaryProvider} remains unavailable after enabling`);
    await defaultProviderSelect.selectOption(temporaryProvider);
    assert.equal(await defaultProviderSelect.inputValue(), temporaryProvider, "default Provider did not switch");
    const originalDefaultToggle = toggleInput(`启用 ${originalDefaultProvider}`);
    assert.equal(await originalDefaultToggle.isEnabled(), true, "previous default Provider did not become switchable");

    const providerFields = [
      [airwallexConfig, "Airwallex Base URL"],
      [airwallexConfig, "Airwallex Cardholder ID"],
      [airwallexConfig, "Airwallex 币种"],
      [airwallexConfig, "Airwallex 卡片类型"],
      [airwallexConfig, "Airwallex 卡片用途"],
      [airwallexConfig, "Airwallex 创建者标识"],
      [stripeIssuingConfig, "Stripe Issuing Base URL"],
      [stripeIssuingConfig, "Stripe Issuing Cardholder ID"],
      [stripeIssuingConfig, "Stripe Issuing 币种"],
      [photonpayConfig, "PhotonPay Base URL"],
      [photonpayConfig, "Cardholder ID"],
      [photonpayConfig, "卡 BIN"],
      [dogpayConfig, "DogPay Base URL"],
      [dogpayConfig, "Channel ID"],
      [dogpayConfig, "Cardholder ID"],
      [kimooxConfig, "Kimoox Base URL"],
      [kimooxConfig, "Card BIN ID"],
    ];
    let visibleProviderSection = null;
    for (const [section, label] of providerFields) {
      if (visibleProviderSection !== section) {
        await ensureDetailsOpen(section);
        visibleProviderSection = section;
      }
      const input = section.getByRole("textbox", { name: label, exact: true });
      assert.equal(await input.count(), 1, `${label} input is missing`);
      assert.equal(await input.isEnabled(), true, `${label} input is disabled`);
      await input.click();
      await input.press("End");
    }
    const formFactor = page.getByRole("combobox", { name: "Airwallex Form Factor", exact: true });
    await ensureDetailsOpen(airwallexConfig);
    assert.equal(await formFactor.isEnabled(), true, "Airwallex form factor select is disabled");
    const originalFormFactor = await formFactor.inputValue();
    await formFactor.selectOption(originalFormFactor === "VIRTUAL" ? "PHYSICAL" : "VIRTUAL");
    assert.notEqual(await formFactor.inputValue(), originalFormFactor, "Airwallex form factor did not toggle");
    await formFactor.selectOption(originalFormFactor);
    const airwallexTolerance = airwallexConfig.getByRole("spinbutton", { name: "Webhook 容差（秒）", exact: true });
    const stripeTolerance = stripeIssuingConfig.getByRole("spinbutton", { name: "Webhook 容差（秒）", exact: true });
    for (const [label, input] of [["Airwallex webhook tolerance", airwallexTolerance], ["Stripe Issuing webhook tolerance", stripeTolerance]]) {
      await ensureDetailsOpen(input === airwallexTolerance ? airwallexConfig : stripeIssuingConfig);
      assert.equal(await input.count(), 1, `${label} input is missing`);
      assert.equal(await input.isEnabled(), true, `${label} input is disabled`);
      await input.click();
      await input.press("End");
    }

    const airwallexSecret = airwallexConfig.getByRole("textbox", { name: "Airwallex API Key", exact: true });
    const stripeIssuingSecret = stripeIssuingConfig.getByRole("textbox", { name: "Stripe Issuing Secret Key", exact: true });
    const airwallexWebhookSecret = airwallexConfig.getByRole("textbox", { name: "Airwallex Webhook Secret", exact: true });
    const stripeIssuingWebhookSecret = stripeIssuingConfig.getByRole("textbox", { name: "Stripe Issuing Webhook Secret", exact: true });
    const photonpayAppID = photonpayConfig.getByRole("textbox", { name: "App ID", exact: true });
    const photonpayAppSecret = photonpayConfig.getByRole("textbox", { name: "App Secret", exact: true });
    const photonpayPrivateKey = photonpayConfig.getByRole("textbox", { name: "RSA Private Key", exact: true });
    const dogpayAppID = dogpayConfig.getByRole("textbox", { name: "App ID", exact: true });
    const dogpayAppSecret = dogpayConfig.getByRole("textbox", { name: "App Secret", exact: true });
    const dogpayPrivateKey = dogpayConfig.getByRole("textbox", { name: "RSA Private Key", exact: true });
    for (const [label, input, section] of [
      ["Airwallex API key", airwallexSecret, airwallexConfig],
      ["Stripe Issuing secret key", stripeIssuingSecret, stripeIssuingConfig],
      ["Airwallex webhook secret", airwallexWebhookSecret, airwallexConfig],
      ["Stripe Issuing webhook secret", stripeIssuingWebhookSecret, stripeIssuingConfig],
      ["PhotonPay app ID", photonpayAppID, photonpayConfig],
      ["PhotonPay app secret", photonpayAppSecret, photonpayConfig],
      ["PhotonPay private key", photonpayPrivateKey, photonpayConfig],
      ["DogPay app ID", dogpayAppID, dogpayConfig],
      ["DogPay app secret", dogpayAppSecret, dogpayConfig],
      ["DogPay private key", dogpayPrivateKey, dogpayConfig],
    ]) {
      await ensureDetailsOpen(section);
      assert.equal(await input.count(), 1, `${label} input is missing`);
      assert.equal(await input.isEnabled(), true, `${label} input is disabled`);
      await input.click();
      assert.equal(await input.getAttribute("type"), "password", `${label} is not a password input`);
    }

    await ensureDetailsOpen(airwallexConfig);
    const originalAirwallexTolerance = await airwallexTolerance.inputValue();
    await ensureDetailsOpen(stripeIssuingConfig);
    const originalStripeTolerance = await stripeTolerance.inputValue();
    const originalToleranceValues = [originalAirwallexTolerance, originalStripeTolerance];
    await ensureDetailsOpen(airwallexConfig);
    const temporaryTolerance = String(Number(originalToleranceValues[0]) === 86400 ? 86399 : Number(originalToleranceValues[0]) + 1);
    await airwallexTolerance.fill(temporaryTolerance);
    const saveProviderRequest = page.waitForRequest((request) =>
      request.url().includes("/legacy-api/admin/config") &&
      request.method() === "POST",
    );
    const saveProviderResponse = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const saveProviderReload = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    const providerConfigPanel = page.locator("section.panel").filter({ has: stripeIssuingConfig }).first();
    await providerConfigPanel.getByRole("button", { name: "保存卡池与 Provider 配置", exact: true }).click();
    const providerRequestBody = JSON.parse((await saveProviderRequest).postData() || "{}");
    assert.equal(providerRequestBody.airwallex_webhook_tolerance_seconds, temporaryTolerance, "provider config save did not send the edited Airwallex value");
    assert.equal(providerRequestBody.card_pool_routing, "FAILOVER", "provider config save did not send the edited routing strategy");
    assert.equal(providerRequestBody.card_pool_default_provider, temporaryProvider, "provider config save did not send the edited default Provider");
    const providerEnabledKey = {
      AIRWALLEX: "card_provider_airwallex_enabled",
      STRIPE_ISSUING: "card_provider_stripe_issuing_enabled",
    }[temporaryProvider];
    assert.equal(providerRequestBody[providerEnabledKey], "true", "provider config save did not send the edited Provider switch");
    assert.equal(Object.hasOwn(providerRequestBody, "emailEnabled"), false, "provider save sent email settings");
    assert.equal(Object.hasOwn(providerRequestBody, "stripeSuccessURL"), false, "provider save sent store payment settings");
    await Promise.all([saveProviderResponse, saveProviderReload]);
    await page.getByText("卡池与 Provider 配置已保存", { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    await page.getByRole("heading", { name: "系统配置", exact: true, level: 1 }).waitFor({ state: "visible" });
    await ensureDetailsOpen(airwallexConfig);
    assert.equal(await airwallexTolerance.inputValue(), temporaryTolerance, "edited Airwallex value was not retained after save");
    assert.equal(await routingSelect.inputValue(), "FAILOVER", "routing strategy was not retained after save");
    assert.equal(await defaultProviderSelect.inputValue(), temporaryProvider, "default Provider was not retained after save");
    assert.equal(await temporaryProviderToggle.isChecked(), true, `${temporaryProvider} was not retained after save`);

    await routingSelect.selectOption(originalRouting);
    await originalDefaultToggle.setChecked(originalToggleStates[providerToggles.findIndex(([label]) => label === `启用 ${originalDefaultProvider}`)]);
    await defaultProviderSelect.selectOption(originalDefaultProvider);
    for (const [index, [label, input]] of providerToggles.entries()) {
      if (label === `启用 ${originalDefaultProvider}`) continue;
      await input.setChecked(originalToggleStates[index]);
    }
    await ensureDetailsOpen(airwallexConfig);
    await airwallexTolerance.fill(originalToleranceValues[0]);
    const restoreResponse = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const restoreReload = page.waitForResponse((response) =>
      response.url().includes("/legacy-api/admin/config") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await providerConfigPanel.getByRole("button", { name: "保存卡池与 Provider 配置", exact: true }).click();
    await Promise.all([restoreResponse, restoreReload]);
    await page.getByText("卡池与 Provider 配置已保存", { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    await ensureDetailsOpen(airwallexConfig);
    assert.equal(await airwallexTolerance.inputValue(), originalToleranceValues[0], "Airwallex tolerance did not restore");
    await ensureDetailsOpen(stripeIssuingConfig);
    assert.equal(await stripeTolerance.inputValue(), originalToleranceValues[1], "Stripe Issuing tolerance changed unexpectedly");
    for (const [index, [label, input]] of providerToggles.entries()) {
      assert.equal(await input.isChecked(), originalToggleStates[index], `${label} changed unexpectedly after save`);
    }
    await assertNoPageError(page, "/admin/settings after Provider config E2E");
    }

    const storeProductsLoad = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await page.goto(`${baseURL}/admin/store_products`, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "售卡商品", exact: true, level: 1 }).waitFor({ state: "visible" });
    await storeProductsLoad;
    const formPanel = page.locator("section.panel").filter({ hasText: "发布售卡商品" }).first();
    const productCode = `e2e-${Date.now().toString(36)}`;
    await formPanel.locator("label").filter({ hasText: "商品编码" }).locator("input").fill(productCode);
    await formPanel.locator("label").filter({ hasText: "商品名称" }).locator("input").fill("E2E 测试商品");
    await formPanel.locator("label").filter({ hasText: "价格" }).locator("input").fill("1");
    await formPanel.locator("label").filter({ hasText: "排序" }).locator("input").fill("999");
    const published = formPanel.locator('input[type="checkbox"]');
    await published.waitFor({ state: "visible" });
    assert.equal(await published.isChecked(), true, "new product defaults to published");
    await published.uncheck();
    assert.equal(await published.isChecked(), false, "new product can be saved as unpublished");
    const createRequest = page.waitForRequest((request) =>
      request.url().includes("/platform-api/admin/store/products") &&
      request.method() === "POST",
    );
    const createListRefresh = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    const createResponse = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    await page.getByRole("button", { name: "保存商品", exact: true }).click();
    const createRequestBody = JSON.parse((await createRequest).postData() || "{}");
    assert.equal(createRequestBody.published, false, `create request published=${createRequestBody.published}`);
    await createResponse;
    await createListRefresh;
    await page.getByText(`商品 ${productCode} 已保存`, { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    const productRow = page.locator("tbody tr").filter({ hasText: productCode }).first();
    await productRow.waitFor({ state: "visible", timeout: 10_000 });
    assert.match(await productRow.innerText(), /已下架/);

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
    assert.match(await productRow.innerText(), /已发布/);

    const unpublishResponse = page.waitForResponse((response) =>
      response.url().includes(`/platform-api/admin/store/products/${productCode}/toggle`) &&
      response.request().method() === "POST" &&
      response.status() === 200,
    );
    const unpublishListRefresh = page.waitForResponse((response) =>
      response.url().includes("/platform-api/admin/store/products") &&
      response.request().method() === "GET" &&
      response.status() === 200,
    );
    await productRow.getByRole("button", { name: "下架", exact: true }).click();
    await unpublishResponse;
    await unpublishListRefresh;
    await page.getByText(`商品 ${productCode} 已下架`, { exact: true }).waitFor({ state: "visible", timeout: 10_000 });
    assert.match(await productRow.innerText(), /已下架/);
    await assertNoPageError(page, "/admin/store_products");

    assert.deepEqual(apiFailures, [], `Go API returned server errors: ${apiFailures.join(", ")}`);
    assert.deepEqual(missingTraceIds, [], `Go API responses missing X-Trace-ID: ${missingTraceIds.join(", ")}`);
    assert.deepEqual(pageErrors, [], `browser page errors: ${pageErrors.join(" | ")}`);
    console.log(`Admin E2E passed: ${routeCases.length} routes, UI product create/publish/unpublish, ${missingTraceIds.length === 0 ? "trace IDs present" : "trace IDs missing"}.`);
  } finally {
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exitCode = 1;
});
