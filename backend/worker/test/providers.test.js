import test from "node:test";
import assert from "node:assert/strict";

test("worker package exposes the expected queue contract", async () => {
  const { loadConfig } = await import("../src/config.js");
  const config = loadConfig();
  assert.equal(config.queueName, "recharge:tasks");
  assert.equal(config.apiBaseUrl, "http://127.0.0.1:8080");
});
