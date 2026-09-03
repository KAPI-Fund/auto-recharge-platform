import { mkdir } from "node:fs/promises";
import path from "node:path";
import { chromium } from "playwright";

export async function runBrowser({ taskId, session, config, onProgress }) {
  if (!config.browserCheckoutUrl) {
    throw new Error("未配置 BROWSER_CHECKOUT_URL");
  }
  await onProgress(10, "启动 Playwright 浏览器 Worker");
  const browser = await chromium.launch({ headless: config.browserHeadless });
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await page.goto(config.browserCheckoutUrl, { waitUntil: "domcontentloaded", timeout: 45_000 });
    await onProgress(35, "Checkout 页面已打开");

    // 业务自动化的具体选择器和支付适配器独立于队列运行时，避免污染 API 进程。
    await page.evaluate((value) => {
      window.localStorage.setItem("recharge_task_session_preview", value.slice(0, 12));
    }, String(session));

    let screenshot = "";
    await mkdir(config.browserScreenshotDir, { recursive: true });
    screenshot = path.join(config.browserScreenshotDir, `${taskId}-checkout.png`);
    await page.screenshot({ path: screenshot, fullPage: true });

    if (config.browserSuccessSelector) {
      const success = page.locator(config.browserSuccessSelector).first();
      if (await success.isVisible({ timeout: 5_000 }).catch(() => false)) {
        return { status: "succeeded", message: "浏览器流程完成", screenshot };
      }
    }
    return { status: "manual", message: "浏览器流程已打开，等待支付适配器或人工确认", screenshot };
  } finally {
    await context.close().catch(() => {});
    await browser.close().catch(() => {});
  }
}
