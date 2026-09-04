import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  COUNTRY_SELECTOR_TRIGGER_SELECTORS,
  REGION_UI_LABELS,
  REGION_PRICE_PATTERNS,
  isTargetRegionPricingText,
} = require("../src/legacy/pricing-checkout.js");
const { REGION_CONFIG, getRegionUiLabels } = require("../src/legacy/region-config.js");
const { resolveBillingRegion, resolveGptApiBilling } = require("../src/legacy/billing-region.js");
const { mapGptApiPlanKey } = require("../src/legacy/gpt-api-contract.js");

test("pricing region contract includes India and United States", () => {
  assert.equal(
    COUNTRY_SELECTOR_TRIGGER_SELECTORS[0],
    '[data-testid="country-selector-in-pricing-modal"] button[role="combobox"]',
  );
  assert.ok(REGION_UI_LABELS.IN.includes("印度"));
  assert.ok(REGION_UI_LABELS.IN.includes("India"));
  assert.ok(REGION_UI_LABELS.IN.includes("INR"));
  assert.ok(REGION_UI_LABELS.IN.includes("₹"));
  assert.ok(REGION_UI_LABELS.US.includes("United States of America"));
  assert.ok(REGION_UI_LABELS.US.includes("USD"));
  assert.ok(REGION_UI_LABELS.PH.includes("Philippines"));
  assert.ok(REGION_PRICE_PATTERNS.IN.some((pattern) => pattern.test("₹ 0 / month")));
  assert.ok(REGION_PRICE_PATTERNS.US.some((pattern) => pattern.test("$ 0 / month")));
});

test("pricing metadata is owned by the single region config", () => {
  assert.equal(REGION_CONFIG.IN.currency, "INR");
  assert.equal(REGION_CONFIG.US.currency, "USD");
  assert.equal(REGION_CONFIG.PH.currency, "PHP");
  assert.deepEqual(REGION_UI_LABELS.IN, getRegionUiLabels("IN"));
});

test("zero-priced special offers still require the selected billing country", () => {
  assert.equal(
    isTargetRegionPricingText("India Personal Go ₹ 0 / month", "IN", "India"),
    true,
  );
  assert.equal(
    isTargetRegionPricingText("India Personal Go ₹ 0 / month", "IN", ""),
    false,
  );
  assert.equal(
    isTargetRegionPricingText("Philippines Personal Go ₱ 0 / month", "US", "Philippines"),
    false,
  );
});

test("protocol billing prefers task or product region over global config", () => {
  assert.equal(
    resolveBillingRegion({
      task: { country: "IN" },
      cfg: { country: "PH" },
    }),
    "IN",
  );
  assert.deepEqual(
    resolveGptApiBilling({
      region: "IN",
      task: { paymentRegion: "US" },
      cfg: { country: "PH", currency: "PHP" },
    }),
    { country: "IN", currency: "INR" },
  );
  assert.deepEqual(
    resolveGptApiBilling({
      task: { paymentRegion: "US" },
      cfg: { country: "PH", currency: "PHP" },
    }),
    { country: "US", currency: "USD" },
  );
  assert.deepEqual(
    resolveGptApiBilling({ cfg: { country: "PH", currency: "PHP" } }),
    { country: "PH", currency: "PHP" },
  );
});

test("protocol plan mapping includes ChatGPT Go", () => {
  assert.equal(mapGptApiPlanKey("go"), "go");
  assert.equal(mapGptApiPlanKey("pro_5x"), "pro5x");
  assert.equal(mapGptApiPlanKey("unknown"), "plus");
});
