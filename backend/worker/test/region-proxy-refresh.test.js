import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { refreshAssignedProxyIp } = require("../src/legacy/pricing-checkout.js");

function withEnv(values, fn) {
  const previous = {};
  for (const [key, value] of Object.entries(values)) {
    previous[key] = process.env[key];
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      for (const [key, value] of Object.entries(previous)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    });
}

test("refreshAssignedProxyIp calls API refreshProxy by asset id", async () => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = async (url, options = {}) => {
    calls.push({ url: String(url), method: options.method || "GET", body: options.body || "" });
    return {
      ok: true,
      status: 200,
      text: async () => JSON.stringify({ ok: true, refreshed: true }),
    };
  };
  try {
    await withEnv({
      PROXY_REFRESH_URL: undefined,
      PROXY_ASSET_ID: "proxy_1",
      API_BASE_URL: "http://api.test",
      WORKER_API_TOKEN: "tok",
    }, async () => {
      assert.equal(await refreshAssignedProxyIp({ waitMs: 0 }), true);
    });
    assert.equal(calls.length, 1);
    assert.equal(calls[0].method, "POST");
    assert.equal(calls[0].url, "http://api.test/api/v1/internal/store/refreshProxy");
    assert.match(String(calls[0].body), /proxy_1/);
  } finally {
    global.fetch = originalFetch;
  }
});

test("refreshAssignedProxyIp returns false without refresh config", async () => {
  await withEnv({
    PROXY_REFRESH_URL: undefined,
    PROXY_ASSET_ID: undefined,
    API_BASE_URL: undefined,
    WORKER_API_TOKEN: undefined,
  }, async () => {
    assert.equal(await refreshAssignedProxyIp({ waitMs: 0 }), false);
  });
});
