import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require("playwright");
const {
  selectPricingRegion,
  pageShowsTargetRegionPricing,
  clickPlanUpgrade,
} = require("../src/legacy/pricing-checkout.js");

const LOCAL_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

function launchRealBrowser() {
  return chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_BROWSERS_PATH === "0" ? {} : { executablePath: LOCAL_CHROME }),
  });
}

function pricingFixture() {
  return `<!doctype html>
  <html><head><style>.hidden{display:none}</style></head><body>
    <div role="dialog" style="height:400px;overflow:auto">
      <div style="height:300px">套餐区</div>
      <div data-testid="pricing-modal-footer" style="position:relative;height:100px">
        <span data-testid="country-selector-in-pricing-modal">
          <span class="sr-only">国家/地区和货币</span>
          <button type="button" role="combobox" aria-expanded="false"><span id="selected-country">Philippines</span></button>
        </span>
        <div id="price">₱ 0 / month</div>
      </div>
    </div>
    <script>
      const regions = {
        PH: {country:'Philippines', currency:'₱'},
        US: {country:'United States', currency:'$'}
      };
      const trigger = document.querySelector('[data-testid="country-selector-in-pricing-modal"] button');
      const selected = document.querySelector('#selected-country');
      const price = document.querySelector('#price');
      trigger.addEventListener('click', () => {
        let list = document.querySelector('[role="listbox"]');
        if (list) return;
        list = document.createElement('div');
        list.setAttribute('role','listbox');
        list.innerHTML = '<input type="search" placeholder="Search countries" />' +
          Object.values(regions).map(r => '<div role="option">'+r.country+'</div>').join('');
        document.body.appendChild(list);
        list.querySelectorAll('[role="option"]').forEach(option => option.addEventListener('click', () => {
          const region = Object.values(regions).find(r => r.country === option.textContent);
          selected.textContent = region.country;
          price.textContent = region.currency + ' 0 / month';
          list.remove();
        }));
      });
    </script>
  </body></html>`;
}

test("real Chromium browser flow selects PH and US", async (t) => {
  const browser = await launchRealBrowser();
  t.after(async () => browser.close());

  for (const [region, expectedCountry, expectedCurrency] of [
    ["PH", "Philippines", "₱"],
    ["US", "United States", "$"]
  ]) {
    const page = await browser.newPage();
    await page.setContent(pricingFixture());
    await selectPricingRegion(page, region);
    assert.equal(await pageShowsTargetRegionPricing(page, region), true, region);
    assert.equal(await page.locator("#selected-country").innerText(), expectedCountry);
    assert.match(await page.locator("#price").innerText(), new RegExp(`\\${expectedCurrency} 0`));
    await page.close();
  }
});

test("Plus plan still uses the Plus upgrade button", async (t) => {
  const browser = await launchRealBrowser();
  t.after(async () => browser.close());
  const page = await browser.newPage();
  const clicks = [];

  await page.setContent(`<!doctype html>
    <html><body>
      <main>
        <section data-plan="plus">
          <h2>ChatGPT Plus</h2>
          <button type="button" onclick="window.__clicks.push('plus')">Upgrade to Plus</button>
        </section>
        <section data-plan="pro">
          <h2>ChatGPT Pro</h2>
          <button type="button" onclick="window.__clicks.push('pro')">Upgrade to Pro</button>
        </section>
      </main>
      <script>window.__clicks = [];</script>
    </html>`);

  await clickPlanUpgrade(page, "plus");
  clicks.push(...await page.evaluate(() => window.__clicks));

  assert.deepEqual(clicks, ["plus"]);
  await page.close();
});
