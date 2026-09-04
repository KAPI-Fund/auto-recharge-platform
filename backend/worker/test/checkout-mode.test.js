import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  CHECKOUT_MODES,
  normalizeCheckoutMode,
  shouldFallbackToUi,
  formatCheckoutApiFailure,
} = require("../src/legacy/checkout-mode.js");

test("checkout mode defaults to API and only explicit api_then_ui enables fallback", () => {
  assert.deepEqual(CHECKOUT_MODES, ["api", "ui", "api_then_ui"]);
  assert.equal(normalizeCheckoutMode(), "api");
  assert.equal(shouldFallbackToUi("api"), false);
  assert.equal(shouldFallbackToUi("ui"), false);
  assert.equal(shouldFallbackToUi("api_then_ui"), true);
});

test("invalid checkout mode fails before starting a browser flow", () => {
  assert.throws(() => normalizeCheckoutMode("api-or-magic"), /不支持的 CHECKOUT_MODE/);
});

test("API checkout error keeps the provider's original detail", () => {
  assert.equal(
    formatCheckoutApiFailure(new Error('{"type":"value_error","loc":["body","plan_name"],"msg":"Invalid value for enum PlanName"}')),
    'API 创建 Checkout 失败: {"type":"value_error","loc":["body","plan_name"],"msg":"Invalid value for enum PlanName"}',
  );
  assert.equal(formatCheckoutApiFailure(new Error("API 创建 Checkout 失败: upstream detail")), "API 创建 Checkout 失败: upstream detail");
});
