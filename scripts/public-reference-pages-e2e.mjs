import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "../backend/worker/node_modules/playwright/index.mjs";

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:23000").replace(/\/$/, "");

const routeCases = [
  ["/", "ChatGPT 订阅"],
  ["/faq", "有什么可以帮到你？"],
  ["/guide", "ChatGPT 充值卡密代充指引"],
  ["/order", "订单中心"],
  ["/orders", "订单中心"],
  ["/gptpro", "ChatGPT Pro"],
  ["/chatgpt-plus", "ChatGPT Plus"],
  ["/chatgpt-pro", "ChatGPT Pro"],
  ["/plus-price", "ChatGPT Plus"],
  ["/gemini-pro", "Gemini"],
  ["/grok", "Grok"],
  ["/codex", "OpenAI Codex"],
  ["/help", "遇到问题？先从这里开始"],
  ["/blog", "教程与博客"],
  ["/blog/session-safety-guide", "Session 是什么"],
  ["/blog/tag/ChatGPT", "标签：ChatGPT"],
  ["/about", "关于 KC ChatGPT"],
  ["/privacy", "隐私政策"],
  ["/terms", "服务条款"],
];

async function run() {
  const configuredExecutable = process.env.E2E_CHROMIUM_EXECUTABLE_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const launchOptions = { headless: true };
  if (existsSync(configuredExecutable)) launchOptions.executablePath = configuredExecutable;
  const browser = await chromium.launch(launchOptions);
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const documentNavigations = [];
  const apiFailures = [];
  const pageErrors = [];

  page.on("request", (request) => {
    if (request.isNavigationRequest() && request.resourceType() === "document") documentNavigations.push(request.url());
  });
  page.on("response", (response) => {
    if (!response.url().includes("/platform-api/") && !response.url().includes("/legacy-api/")) return;
    if (response.status() >= 500) apiFailures.push(`${response.status()} ${response.request().method()} ${response.url()}`);
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));

  async function visit(path) {
    await page.goto(`${baseURL}${path}`, { waitUntil: "domcontentloaded" });
    await page.locator("main").waitFor({ state: "visible", timeout: 15000 });
    await page.waitForTimeout(250);
    const text = await page.locator("body").innerText();
    assert.equal(text.includes("[object Object]"), false, `${path} contains [object Object]`);
    assert.equal(text.includes("当前账单地区："), false, `${path} exposes the billing region hint`);
  }

  try {
    for (const [path, heading] of routeCases) {
      await visit(path);
      assert.match(await page.locator("body").innerText(), new RegExp(heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), `${path} is missing its page heading`);
    }

    await visit("/");
    await page.locator(".hero-price-card").first().waitFor({ state: "visible", timeout: 10000 });
    assert.equal(await page.locator(".reference-support-button").count(), 1, "public pages are missing the support entry");
    assert.deepEqual(await page.locator(".public-header-nav__link").allTextContents(), ["首页", "常见问题", "教程"], "public header contains redundant product navigation");
    const homeDocumentCount = documentNavigations.length;
    await page.getByRole("tab", { name: /2\s*提交账号信息/ }).click();
    assert.match(await page.locator(".reference-flow-panel").innerText(), /提交账号信息/);
    assert.equal(await page.getByRole("tab", { name: /2\s*提交账号信息/ }).getAttribute("aria-selected"), "true");

    await page.locator("nav.public-header-nav").getByRole("link", { name: "常见问题", exact: true }).click();
    await page.waitForURL(/\/faq$/);
    assert.equal(documentNavigations.length, homeDocumentCount, "header navigation performed a full document reload");

    await page.goto(`${baseURL}/faq#aftersale`, { waitUntil: "domcontentloaded" });
    await page.locator("#aftersale.is-active").waitFor({ state: "visible", timeout: 15000 });
    const faqQuestion = page.locator(".faq-reference-item > button").first();
    await faqQuestion.click();
    assert.equal(await page.locator(".faq-reference-answer").count(), 1, "FAQ question did not expand");
    const faqDocumentCount = documentNavigations.length;
    await page.locator(".faq-help-callout").click();
    await page.waitForURL(/\/help$/);
    assert.equal(documentNavigations.length, faqDocumentCount, "FAQ help link performed a full document reload");

    await page.getByLabel("搜索帮助").fill("开通失败");
    await page.getByRole("button", { name: "搜索帮助", exact: true }).click();
    assert.match(await page.locator(".help-search-result").innerText(), /开通失败/);
    await page.locator("a.help-card").filter({ hasText: "充值失败处理" }).click();
    await page.waitForURL(/\/faq#aftersale$/);
    await page.locator("#aftersale.is-active").waitFor({ state: "visible", timeout: 15000 });

    const rechargePage = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    await rechargePage.route("**/platform-api/recharge/verify", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ valid: true, status: "available", cdk: { code: "TEST-CDK", plan: { code: "plus", name: "ChatGPT Plus", currency: "CNY", price: 20, description: "测试套餐" } } }),
      });
    });
    const plansResponse = rechargePage.waitForResponse((response) => response.url().includes("/platform-api/plans") && response.request().method() === "GET", { timeout: 15000 });
    await rechargePage.goto(`${baseURL}/recharge`, { waitUntil: "domcontentloaded" });
    await plansResponse;
    await rechargePage.locator("#cdkInput").fill("TEST-CDK");
    await rechargePage.getByRole("button", { name: "兑换卡密" }).click();
    await rechargePage.locator(".session-guide").waitFor({ state: "visible", timeout: 10000 });
    assert.equal(await rechargePage.locator(".session-guide-url a").getAttribute("href"), "https://chatgpt.com/api/auth/session", "redeem form is missing the official Session URL");
    assert.equal(await rechargePage.locator(".session-guide-link").getAttribute("target"), "_blank", "redeem form Session link should open in a new tab");
    await rechargePage.close();

    await visit("/guide");
    assert.equal(await page.locator("video").count(), 0, "guide must not embed a third-party or copied video");
    assert.equal(await page.locator(".guide-article img").count(), 0, "guide must not embed copied screenshots");
    const guideSessionLink = page.locator(".guide-session-link-card__action");
    assert.equal(await guideSessionLink.getAttribute("href"), "https://chatgpt.com/api/auth/session", "guide Session link does not point to the official endpoint");
    assert.equal(await guideSessionLink.getAttribute("target"), "_blank", "guide Session link should open in a new tab");
    await page.locator('a[href="#step2"]').click();
    await page.waitForURL(/\/guide#step2$/);
    assert.equal(await page.locator("#step2").count(), 1, "guide step anchor target is missing");
    const guideDocumentCount = documentNavigations.length;
    await page.getByRole("link", { name: /立即开始充值/ }).click();
    await page.waitForURL(/\/recharge\?tab=purchase/);
    assert.equal(documentNavigations.length, guideDocumentCount, "guide CTA performed a full document reload");

    await visit("/blog");
    await page.locator(".blog-card").first().waitFor({ state: "visible", timeout: 5000 });
    const firstTag = page.locator(".blog-card__tags a").first();
    const tagHref = await firstTag.getAttribute("href");
    assert.match(tagHref || "", /\/blog\/tag\//, "blog tags are not links");
    await firstTag.click();
    await page.waitForURL(/\/blog\/tag\//);
    assert.equal(await page.locator(".blog-card").count() > 0, true, "blog tag page did not filter to a result");
    assert.match(await page.locator(".blog-reference-heading").innerText(), /#/);
    await page.locator(".blog-card > a").first().click();
    await page.waitForURL(/\/blog\/[^/]+$/);
    assert.equal(await page.locator(".blog-article h1").count(), 1, "blog article route did not render the article view");

    await visit("/gptpro");
    const productCTA = page.locator(".product-hero__actions .reference-primary-button");
    assert.match(await productCTA.getAttribute("href") || "", /\/recharge\?tab=purchase/);
    await productCTA.click();
    await page.waitForURL(/\/recharge\?tab=purchase/);

    await visit("/chatgpt-pro");
    assert.equal(await page.locator(".public-header-nav__link").count(), 3, "product alias reintroduced redundant header navigation");
    assert.equal(await page.locator("nav.public-header-nav__link.is-current").count(), 0, "product alias unexpectedly highlighted a removed header link");
    await visit("/orders");
    assert.equal(await page.locator("a.public-header-link.is-current").getAttribute("href"), "/order", "orders alias did not highlight the order navigation");

    await page.setViewportSize({ width: 390, height: 844 });
    await visit("/");
    await page.waitForLoadState("load");
    await page.waitForTimeout(250);
    const menuToggle = page.getByRole("button", { name: "打开菜单" });
    await menuToggle.click();
    await page.locator(".public-mobile-menu").waitFor({ state: "visible", timeout: 5000 });
    assert.equal(await page.getByRole("button", { name: "关闭菜单" }).count(), 1, "mobile menu did not switch to close state");
    await page.locator(".public-mobile-menu a").filter({ hasText: "查询订单" }).click();
    await page.waitForURL(/\/order$/);
    assert.equal(await page.locator(".public-mobile-menu").count(), 0, "mobile menu stayed open after navigation");

    assert.deepEqual(apiFailures, [], `public pages returned server errors: ${apiFailures.join(", ")}`);
    assert.deepEqual(pageErrors, [], `public pages raised browser errors: ${pageErrors.join(" | ")}`);
    console.log(`Public reference pages E2E passed: ${routeCases.length} routes, internal navigation, anchors, tags, CTA, and mobile menu.`);
  } finally {
    await page.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exitCode = 1;
});
