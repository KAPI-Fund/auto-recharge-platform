import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require("playwright");
const { switchToNewCardIfSavedExists } = require("../src/legacy/stripe-payment.js");

const LOCAL_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

function launchRealBrowser() {
  return chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_BROWSERS_PATH === "0" ? {} : { executablePath: LOCAL_CHROME }),
  });
}

test("clicks Card when Saved and Card both exist", async (t) => {
  const browser = await launchRealBrowser();
  t.after(async () => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html>
    <html><body>
      <h1>Configure your plan</h1>
      <p>Pay with</p>
      <div role="tablist">
        <button role="tab" aria-selected="true" id="saved">Saved</button>
        <button role="tab" aria-selected="false" id="card">Card</button>
      </div>
      <div id="saved-panel">Link Visa •••• 6417</div>
      <div id="card-panel" hidden>Card number</div>
      <script>
        window.__clicked = '';
        const saved = document.getElementById('saved');
        const card = document.getElementById('card');
        card.addEventListener('click', () => {
          window.__clicked = 'card';
          saved.setAttribute('aria-selected', 'false');
          card.setAttribute('aria-selected', 'true');
          document.getElementById('saved-panel').hidden = true;
          document.getElementById('card-panel').hidden = false;
        });
      </script>
    </body></html>`);

  const switched = await switchToNewCardIfSavedExists(page);
  assert.equal(switched, true);
  assert.equal(await page.evaluate(() => window.__clicked), "card");
  assert.equal(await page.locator("#card").getAttribute("aria-selected"), "true");
  await page.close();
});

test("does nothing when only Card exists", async (t) => {
  const browser = await launchRealBrowser();
  t.after(async () => browser.close());
  const page = await browser.newPage();
  await page.setContent(`<!doctype html>
    <html><body>
      <button type="button" id="card">Card</button>
      <script>window.__clicked = false; document.getElementById('card').onclick = () => { window.__clicked = true; }</script>
    </body></html>`);

  const switched = await switchToNewCardIfSavedExists(page);
  assert.equal(switched, false);
  assert.equal(await page.evaluate(() => window.__clicked), false);
  await page.close();
});
