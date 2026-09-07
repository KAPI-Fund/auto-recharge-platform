import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { REGION_CONFIG } = require("../src/legacy/region-config.js");
const { resolveBillingRegion, resolveGptApiBilling } = require("../src/legacy/billing-region.js");
const { mapGptApiPlanKey } = require("../src/legacy/gpt-api-contract.js");

test("region config still has PH US and IN currencies", () => {
  assert.equal(REGION_CONFIG.IN.currency, "INR");
  assert.equal(REGION_CONFIG.US.currency, "USD");
  assert.equal(REGION_CONFIG.PH.currency, "PHP");
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
