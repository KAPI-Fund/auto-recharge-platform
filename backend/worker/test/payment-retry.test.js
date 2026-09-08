import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  isProviderIssuedCard,
  shouldRotateToNextCard,
} = require("../src/legacy/payment-retry.js");

test("Kimoox and other virtual cards are provider-issued", () => {
  assert.equal(isProviderIssuedCard({ provider: "KIMOOX", provider_card_id: "vc_1" }), true);
  assert.equal(isProviderIssuedCard({ provider: "kimoox", providerCardId: "vc_1" }), true);
  assert.equal(isProviderIssuedCard({ provider: "AIRWALLEX", provider_card_id: "card_1" }), true);
  assert.equal(isProviderIssuedCard({ provider: "KIMOOX" }), true);
});

test("LOCAL_TEXT pool cards can still rotate after a decline", () => {
  assert.equal(isProviderIssuedCard({ provider: "LOCAL_TEXT", provider_card_id: "local_1" }), false);
  assert.equal(isProviderIssuedCard({ provider: "", provider_card_id: "" }), false);
});

test("a remote card id without LOCAL_TEXT is treated as provider-issued", () => {
  assert.equal(isProviderIssuedCard({ provider: "", provider_card_id: "226146" }), true);
  assert.equal(isProviderIssuedCard({ providerCardId: "vc_1" }), true);
});

test("declined virtual card does not consume another card attempt", () => {
  const card = { provider: "KIMOOX", provider_card_id: "226146" };
  assert.equal(shouldRotateToNextCard(card, 1, { canRetryCard: true }), false);
  assert.equal(shouldRotateToNextCard(card, 2, { canRetryCard: true }), false);
});

test("declined LOCAL_TEXT card can rotate until max attempts", () => {
  const card = { provider: "LOCAL_TEXT", provider_card_id: "local_1" };
  assert.equal(shouldRotateToNextCard(card, 1, { canRetryCard: true }), true);
  assert.equal(shouldRotateToNextCard(card, 3, { canRetryCard: true }), false);
  assert.equal(shouldRotateToNextCard(card, 1, { canRetryCard: false }), false);
});
