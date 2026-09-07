import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  attachPageDebugCapture,
  configuredLogLevel,
  normalizeLogLevel,
  redactSensitive,
  shouldLogAt,
} = require("../src/legacy/page-debug.js");

class FakePage {
  constructor() {
    this.listeners = new Map();
  }

  on(event, handler) {
    const handlers = this.listeners.get(event) || [];
    handlers.push(handler);
    this.listeners.set(event, handlers);
    return this;
  }

  off(event, handler) {
    this.listeners.set(event, (this.listeners.get(event) || []).filter((item) => item !== handler));
  }

  emit(event, value) {
    for (const handler of this.listeners.get(event) || []) handler(value);
  }
}

function withEnv(values, callback) {
  const previous = {
    WORKER_LOG_LEVEL: process.env.WORKER_LOG_LEVEL,
    BROWSER_DEBUG_LOGS: process.env.BROWSER_DEBUG_LOGS,
  };
  for (const [key, value] of Object.entries(values)) {
    if (value == null) delete process.env[key];
    else process.env[key] = value;
  }
  try {
    return callback();
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value == null) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

test("worker browser log levels normalize and default to info", () => {
  assert.equal(normalizeLogLevel("OFF"), "off");
  assert.equal(normalizeLogLevel("info"), "info");
  assert.equal(normalizeLogLevel("1"), "debug");
  assert.equal(normalizeLogLevel("unexpected"), "info");
  assert.equal(configuredLogLevel({}), "info");
  assert.equal(configuredLogLevel({ WORKER_LOG_LEVEL: "debug" }), "debug");
  assert.equal(configuredLogLevel({ WORKER_LOG_LEVEL: "off", BROWSER_DEBUG_LOGS: "1" }), "off");
  assert.equal(configuredLogLevel({ BROWSER_DEBUG_LOGS: "0" }), "off");
  assert.equal(shouldLogAt("info", { WORKER_LOG_LEVEL: "info" }), true);
  assert.equal(shouldLogAt("debug", { WORKER_LOG_LEVEL: "info" }), false);
  assert.equal(shouldLogAt("debug", { WORKER_LOG_LEVEL: "debug" }), true);
  assert.equal(shouldLogAt("info", { WORKER_LOG_LEVEL: "off" }), false);
});

test("off disables page listeners and debug output", () => {
  withEnv({ WORKER_LOG_LEVEL: "off", BROWSER_DEBUG_LOGS: null }, () => {
    const page = new FakePage();
    const session = attachPageDebugCapture(page, { label: "off" });
    assert.equal(page.listeners.size, 0);
    assert.equal(typeof session.summarize, "function");
  });
});

test("info keeps failures but suppresses successful network chatter", () => {
  withEnv({ WORKER_LOG_LEVEL: "info", BROWSER_DEBUG_LOGS: null }, () => {
    const page = new FakePage();
    const lines = [];
    const originalLog = console.log;
    console.log = (line) => lines.push(String(line));
    try {
      attachPageDebugCapture(page, { label: "info" });
      const response = (status, url) => ({
        status: () => status,
        url: () => url,
        request: () => ({ method: () => "GET" }),
      });
      page.emit("response", response(200, "https://checkout.stripe.com/session?client_secret=secret"));
      page.emit("response", response(500, "https://checkout.stripe.com/session?client_secret=secret"));
    } finally {
      console.log = originalLog;
    }
    const networkLines = lines.filter((line) => line.includes("[BrowserDebug][net]"));
    assert.equal(networkLines.length, 1);
    assert.match(networkLines[0], /500 GET checkout\.stripe\.com\/session/);
    assert.doesNotMatch(networkLines[0], /client_secret|\?/);
    assert.ok(lines.every((line) => !line.includes(" 200 ")));
  });
});

test("debug includes successful browser diagnostics and redacts sensitive text", () => {
  withEnv({ WORKER_LOG_LEVEL: "debug", BROWSER_DEBUG_LOGS: null }, () => {
    const page = new FakePage();
    const lines = [];
    const originalLog = console.log;
    console.log = (line) => lines.push(String(line));
    try {
      attachPageDebugCapture(page, { label: "debug" });
      page.emit("console", { type: () => "log", text: () => "checkout user@example.com" });
      page.emit("response", {
        status: () => 200,
        url: () => "https://checkout.stripe.com/session?client_secret=secret",
        request: () => ({ method: () => "GET" }),
      });
    } finally {
      console.log = originalLog;
    }
    assert.equal(redactSensitive("user@example.com 4242 4242 4242 4242"), "[redacted-email] [redacted-number]");
    assert.ok(lines.some((line) => line.includes("[BrowserDebug][console]")));
    assert.ok(lines.some((line) => line.includes("[BrowserDebug][net]")));
    assert.ok(lines.every((line) => !line.includes("client_secret") && !line.includes("?")));
  });
});
