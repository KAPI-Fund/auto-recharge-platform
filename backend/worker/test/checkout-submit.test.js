import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require("playwright");
const {
  isCheckoutSubmitLabel,
  pickCheckoutSubmitCandidate,
  createCheckoutSubmitGuard,
  installCheckoutSubmitGuardInBrowser,
  clickVisibleCheckoutSubmitInBrowser,
} = require("../src/legacy/checkout-submit.js");

const LOCAL_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

function launchBrowser() {
  return chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_BROWSERS_PATH === "0" ? {} : { executablePath: LOCAL_CHROME }),
  });
}

test("Subscribe / 订阅 / Pay are checkout submit labels", () => {
  assert.equal(isCheckoutSubmitLabel("Subscribe"), true);
  assert.equal(isCheckoutSubmitLabel("订阅"), true);
  assert.equal(isCheckoutSubmitLabel("Pay"), true);
  assert.equal(isCheckoutSubmitLabel("  Subscribe  "), true);
  assert.equal(isCheckoutSubmitLabel("Subscribe to Plus"), false);
  assert.equal(isCheckoutSubmitLabel("Upgrade"), false);
});

test("picks a single footer submit when sticky and form Subscribe both exist", () => {
  const picked = pickCheckoutSubmitCandidate([
    { text: "Subscribe", visible: true, type: "button", y: 80 },
    { text: "Subscribe", visible: true, type: "submit", y: 640 },
    { text: "Upgrade", visible: true, type: "button", y: 20 },
  ]);
  assert.equal(picked.type, "submit");
  assert.equal(picked.y, 640);
});

test("ignores hidden duplicates and prefers the last visible Subscribe", () => {
  const picked = pickCheckoutSubmitCandidate([
    { text: "Subscribe", visible: false, type: "submit", y: 10 },
    { text: "Subscribe", visible: true, type: "button", y: 200 },
    { text: "Subscribe", visible: true, type: "button", y: 400 },
  ]);
  assert.equal(picked.y, 400);
});

test("checkout submit guard allows the first click and blocks a second click", () => {
  const guard = createCheckoutSubmitGuard();
  assert.equal(guard.allow("click"), true);
  assert.equal(guard.allow("click"), false);
});

test("paired form submit after the first click is still allowed once", () => {
  const guard = createCheckoutSubmitGuard();
  assert.equal(guard.allow("click"), true);
  assert.equal(guard.allow("submit"), true);
  assert.equal(guard.allow("submit"), false);
  assert.equal(guard.allow("click"), false);
});

test("checkout submit guard reset allows the next card attempt to submit once", () => {
  const guard = createCheckoutSubmitGuard();
  assert.equal(guard.allow("click"), true);
  guard.reset();
  assert.equal(guard.allow("click"), true);
  assert.equal(guard.allow("click"), false);
});

test("visible checkout submit clicks the footer Subscribe once", async (t) => {
  const browser = await launchBrowser();
  t.after(async () => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html>
    <html><body>
      <button type="button" id="sticky">Subscribe</button>
      <button type="submit" id="footer">Subscribe</button>
      <script>window.__clicks = [];</script>
    </body></html>`);
  await page.evaluate(() => {
    document.querySelectorAll("button").forEach((el) => {
      el.addEventListener("click", () => window.__clicks.push(el.id));
    });
  });

  const clicked = await page.evaluate(clickVisibleCheckoutSubmitInBrowser);
  const clicks = await page.evaluate(() => window.__clicks);

  assert.equal(clicked.ok, true);
  assert.equal(clicked.count, 2);
  assert.deepEqual(clicks, ["footer"]);
});

test("checkout submit guard blocks a second Subscribe click in the page", async (t) => {
  const browser = await launchBrowser();
  t.after(async () => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html>
    <html><body>
      <button type="button" id="sticky">Subscribe</button>
      <button type="submit" id="footer">Subscribe</button>
      <script>window.__clicks = [];</script>
    </body></html>`);
  await page.evaluate(() => {
    document.querySelectorAll("button").forEach((el) => {
      el.addEventListener("click", () => window.__clicks.push(el.id));
    });
  });

  await page.evaluate(installCheckoutSubmitGuardInBrowser);
  const first = await page.evaluate(clickVisibleCheckoutSubmitInBrowser);
  const second = await page.evaluate(clickVisibleCheckoutSubmitInBrowser);
  const clicks = await page.evaluate(() => window.__clicks);

  assert.equal(first.ok, true);
  assert.equal(second.ok, true);
  assert.deepEqual(clicks, ["footer"]);
});
