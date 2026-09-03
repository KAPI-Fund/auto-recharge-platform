import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { syncLegacyArtifacts } from "../src/worker.js";

const require = createRequire(import.meta.url);
const store = require("../src/legacy-go/store-adapter.cjs");
const taxFreeAddress = require("../src/legacy-go/tax-free-address.cjs");

function listen(server) {
  return new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
}

function close(server) {
  return new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

test("Go store bridge preserves worker token, trace id, and legacy action payloads", async () => {
  const requests = [];
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
    requests.push({ path: request.url, headers: request.headers, body });
    response.setHeader("Content-Type", "application/json");
    if (request.url.endsWith("/getPaymentRegion")) response.end(JSON.stringify({ region: "US" }));
    else if (request.url.endsWith("/reserveCard")) response.end(JSON.stringify({
      card: { id: "card_1", last4: "4242" },
      provider: "STRIPE_ISSUING",
      provider_card_id: "issuing-card-1",
      pool_id: "pool_us",
      allocation_id: "allocation-1",
    }));
    else response.end(JSON.stringify({ ok: true }));
  });
  await listen(server);
  const address = server.address();
  const previous = {
    api: process.env.API_BASE_URL,
    token: process.env.WORKER_API_TOKEN,
    trace: process.env.TRACE_ID,
  };
  process.env.API_BASE_URL = `http://127.0.0.1:${address.port}`;
  process.env.WORKER_API_TOKEN = "worker-contract-token";
  process.env.TRACE_ID = "trace-contract-1";
  try {
    assert.equal(await store.getPaymentRegion(), "US");
    const reserved = await store.reserveCard("owner-1");
    assert.equal(reserved.last4, "4242");
    assert.equal(reserved.provider, "STRIPE_ISSUING");
    assert.equal(reserved.providerCardId, "issuing-card-1");
    assert.equal(reserved.provider_card_id, "issuing-card-1");
    assert.equal(reserved.poolId, "pool_us");
    assert.equal(reserved.pool_id, "pool_us");
    assert.equal(reserved.allocationId, "allocation-1");
    await store.createBillingRecord({ plan_type: "plus", amount: 1.25 });
  } finally {
    if (previous.api === undefined) delete process.env.API_BASE_URL;
    else process.env.API_BASE_URL = previous.api;
    if (previous.token === undefined) delete process.env.WORKER_API_TOKEN;
    else process.env.WORKER_API_TOKEN = previous.token;
    if (previous.trace === undefined) delete process.env.TRACE_ID;
    else process.env.TRACE_ID = previous.trace;
    await close(server);
  }

  assert.equal(requests.length, 3);
  for (const request of requests) {
    assert.equal(request.headers["x-worker-token"], "worker-contract-token");
    assert.equal(request.headers["x-trace-id"], "trace-contract-1");
  }
  assert.deepEqual(requests[1].body, { ownerKey: "owner-1" });
  assert.equal(requests[2].body.data.trace_id, "trace-contract-1");
});

test("Go pool email bridge keeps UUID storage ids usable by the legacy numeric child contract", async () => {
  const requests = [];
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
    requests.push({ path: request.url, body });
    response.setHeader("Content-Type", "application/json");
    if (request.url.endsWith("/reservePoolEmail")) {
      response.end(JSON.stringify({ email: { id: "mail_uuid_1", email: "pool@example.com" } }));
      return;
    }
    response.end(JSON.stringify({ ok: true }));
  });
  await listen(server);
  const address = server.address();
  const previous = {
    api: process.env.API_BASE_URL,
    rawId: process.env.POOL_EMAIL_ID_RAW,
  };
  process.env.API_BASE_URL = `http://127.0.0.1:${address.port}`;
  delete process.env.POOL_EMAIL_ID_RAW;
  try {
    const reserved = await store.reservePoolEmail("product-owner");
    assert.match(String(reserved.id), /^\d+$/);
    await store.releasePoolEmailReservation(reserved.id);
    await store.markPoolEmailRegistered(reserved.id);
  } finally {
    if (previous.api === undefined) delete process.env.API_BASE_URL;
    else process.env.API_BASE_URL = previous.api;
    if (previous.rawId === undefined) delete process.env.POOL_EMAIL_ID_RAW;
    else process.env.POOL_EMAIL_ID_RAW = previous.rawId;
    await close(server);
  }

  assert.deepEqual(requests.map((request) => request.body), [
    { ownerKey: "product-owner" },
    { id: "mail_uuid_1" },
    { id: "mail_uuid_1" },
  ]);
});

test("Go tax-free bridge preserves legacy address selection and generated fallback", async () => {
  const server = createServer((request, response) => {
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ addresses: [] }));
  });
  await listen(server);
  const address = server.address();
  const previous = process.env.API_BASE_URL;
  process.env.API_BASE_URL = `http://127.0.0.1:${address.port}`;
  try {
    const result = await taxFreeAddress.pickBillingAddressForCheckout(null);
    assert.equal(result.country, "US");
    assert.equal(result.generated, true);
    assert.ok(result.line1);
    assert.ok(result.state);
  } finally {
    if (previous === undefined) delete process.env.API_BASE_URL;
    else process.env.API_BASE_URL = previous;
    await close(server);
  }
});

test("legacy screenshots and videos are copied into the Go runtime media root", () => {
  const sourceRoot = path.resolve("src/legacy/debug_screenshots");
  const relative = `contract-${randomUUID()}/sample.png`;
  const source = path.join(sourceRoot, relative);
  const runtimeDir = path.join(os.tmpdir(), `auto-recharge-runtime-${randomUUID()}`);
  mkdirSync(path.dirname(source), { recursive: true });
  writeFileSync(source, "media-contract");
  try {
    syncLegacyArtifacts([source], runtimeDir);
    const target = path.join(runtimeDir, "legacy", "debug_screenshots", relative);
    assert.equal(readFileSync(target, "utf8"), "media-contract");
  } finally {
    rmSync(path.join(sourceRoot, relative.split(path.sep)[0]), { recursive: true, force: true });
    rmSync(runtimeDir, { recursive: true, force: true });
  }
});
