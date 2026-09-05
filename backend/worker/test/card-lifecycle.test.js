import test from "node:test";
import assert from "node:assert/strict";
import { releaseCardReservation, settleOneTimeCard } from "../src/legacy/card-lifecycle.js";

test("single-use settlement uses the Go closeout command once", async () => {
  const calls = [];
  const store = {
    async settleCard(...args) {
      calls.push(args);
      return { ok: true, status: "CANCELLED" };
    },
  };

  const result = await settleOneTimeCard(store, { id: "card-1", allocationId: "allocation-1" }, {
    failureCode: "card_declined",
    failureMessage: "declined",
  });

  assert.equal(result.ok, true);
  assert.equal(result.accepted, true);
  assert.deepEqual(calls, [["card-1", "allocation-1", "card_declined", "declined"]]);
});

test("single-use settlement keeps a received Go error retryable by the API", async () => {
  const error = Object.assign(new Error("provider cancellation pending"), { status: 502 });
  const store = { async settleCard() { throw error; } };

  const result = await settleOneTimeCard(store, { id: "card-1", allocationId: "allocation-1" });

  assert.equal(result.ok, false);
  assert.equal(result.accepted, true);
  assert.equal(result.error, error);
});

test("legacy fallback attempts usage and release even when failure recording fails", async () => {
  const calls = [];
  const store = {
    async recordCardFailure() { calls.push("failure"); throw new Error("failure ledger unavailable"); },
    async recordCardUsage() { calls.push("usage"); },
    async releaseCard() { calls.push("release"); },
  };

  const result = await settleOneTimeCard(store, { id: "card-1", allocation_id: "allocation-1" }, {
    failureCode: "payment_failed",
    failureMessage: "failed",
  });

  assert.equal(result.ok, false);
  assert.deepEqual(calls, ["failure", "usage", "release"]);
});

test("automation-blocked payment releases only the reservation", async () => {
  const calls = [];
  const store = {
    async releaseCardReservation(...args) {
      calls.push(["releaseReservation", ...args]);
      return { ok: true };
    },
    async settleCard() {
      calls.push(["settle"]);
      return { ok: true };
    },
  };

  const result = await releaseCardReservation(store, { id: "card-1", allocationId: "allocation-1" });

  assert.equal(result.ok, true);
  assert.equal(result.accepted, true);
  assert.deepEqual(calls, [["releaseReservation", "card-1", "allocation-1"]]);
});
