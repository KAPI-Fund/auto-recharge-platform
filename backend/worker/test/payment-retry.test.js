import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  executePaymentWithRetry,
  isProviderIssuedCard,
  shouldRotateToNextCard,
} = require("../src/legacy/payment-retry.js");

function testAddress() {
  return {
    id: 7,
    line1: "1 Main St",
    city: "Dover",
    state: "DE",
    postal_code: "19901",
    country: "US",
    generated: false,
  };
}

function kimooxCard(last4) {
  return {
    id: `card_${last4}`,
    card_number: `400416000000${last4}`,
    card_expiry: "12/29",
    card_cvc: "123",
    card_holder: "Test Holder",
    provider: "KIMOOX",
    provider_card_id: `vc_${last4}`,
    allocationId: `alloc_${last4}`,
  };
}

function localCard(last4) {
  return {
    id: `card_${last4}`,
    card_number: `424242000000${last4}`,
    card_expiry: "12/29",
    card_cvc: "123",
    card_holder: "Local Holder",
    provider: "LOCAL_TEXT",
    provider_card_id: `local_${last4}`,
    allocationId: `alloc_${last4}`,
  };
}

const declinedPay = {
  success: false,
  declined: true,
  canRetryCard: true,
  paymentSubmitted: true,
  error: "银行卡被拒绝: Your card was declined",
};

async function runDeclineRetry(cards) {
  const reserved = [];
  const billing = [];
  const settled = [];
  const previousOverride = process.env.PAYMENT_REGION_OVERRIDE;
  delete process.env.PAYMENT_REGION_OVERRIDE;

  const store = {
    async getPaymentRegion() {
      return "PH";
    },
    async getAppConfigValue() {
      return null;
    },
    async reserveCard(ownerKey) {
      reserved.push(ownerKey);
      return cards[reserved.length - 1] || null;
    },
    async createBillingRecord(row) {
      billing.push(row);
    },
    async bindCardPaymentProfile() {
      throw new Error("decline path must not bind a payment profile");
    },
    async recordCardFailure() {
      throw new Error("decline path must settle, not recordCardFailure");
    },
  };

  try {
    const result = await executePaymentWithRetry({}, {
      planType: "plus",
      cdkCode: "cdk_test",
      email: "user@example.com",
      deps: {
        store,
        completeStripeCardPayment: async () => declinedPay,
        readCheckoutDueAmount: async () => ({ amount: 15.69, currency: "USD" }),
        pickBillingAddressForCheckout: async () => testAddress(),
        markAddressBound: async () => {
          throw new Error("decline path must not mark address bound");
        },
        settleOneTimeCard: async (_store, card, outcome = {}) => {
          settled.push({ id: card.id, outcome });
          return { ok: true, accepted: true };
        },
        releaseCardReservation: async () => ({ ok: true, accepted: true }),
      },
    });
    return { result, reserved, billing, settled };
  } finally {
    if (previousOverride === undefined) {
      delete process.env.PAYMENT_REGION_OVERRIDE;
    } else {
      process.env.PAYMENT_REGION_OVERRIDE = previousOverride;
    }
  }
}

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

test("declined Kimoox card is recorded once and does not reserve another card", async () => {
  const { result, reserved, billing, settled } = await runDeclineRetry([
    kimooxCard("4311"),
    kimooxCard("8421"),
  ]);

  assert.equal(reserved.length, 1);
  assert.equal(result.success, false);
  assert.equal(result.cardsDeclined, 1);
  assert.match(String(result.error), /4311|declined|拒绝/i);
  assert.equal(billing.length, 1);
  assert.equal(billing[0].error_code, "card_declined");
  assert.equal(billing[0].status, "failed");
  assert.equal(billing[0].card_last4, "4311");
  assert.equal(billing[0].provider, "KIMOOX");
  assert.equal(settled.length, 1);
  assert.equal(settled[0].id, "card_4311");
  assert.equal(settled[0].outcome.failureCode, "card_declined");
});

test("declined LOCAL_TEXT card still reserves the next pool card", async () => {
  const { result, reserved, billing } = await runDeclineRetry([
    localCard("1111"),
    localCard("2222"),
    localCard("3333"),
  ]);

  assert.equal(reserved.length, 3);
  assert.equal(result.success, false);
  assert.equal(result.cardsDeclined, 3);
  assert.equal(billing.length, 3);
  assert.deepEqual(billing.map((row) => row.card_last4), ["1111", "2222", "3333"]);
  assert.ok(billing.every((row) => row.error_code === "card_declined"));
});
