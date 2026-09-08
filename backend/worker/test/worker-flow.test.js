import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { ApiClient } from "../src/api-client.js";
import { RechargeWorker, analyzeLegacyOutput, proxyAttemptOutcome } from "../src/worker.js";

test("browser worker skips protocol queue items before claiming", async () => {
  const worker = new RechargeWorker({
    api: {
      setTraceId() {},
      async claimTask() {
        throw new Error("should not claim protocol tasks");
      },
    },
    config: { workerId: "test-worker" },
  });
  await assert.rejects(
    () => worker.process({ taskId: "task_protocol", mode: "protocol", traceId: "trace_protocol" }),
    (error) => error.code === "skip_foreign_task",
  );
});

test("expired queue messages are consumed instead of retried forever", async () => {
  let claimCalls = 0;
  const worker = new RechargeWorker({
    api: {
      setTraceId() {},
      async claimTask() {
        claimCalls += 1;
        const error = new Error("task worker lease is no longer valid");
        error.code = "task_lease_lost";
        throw error;
      },
    },
    config: { workerId: "test-worker" },
  });

  await worker.process({ taskId: "task_expired", mode: "dry_run", traceId: "trace_expired" });
  assert.equal(claimCalls, 1, "expired task was claimed more than once");
});

test("legacy output maps protocol debug pause before charge", () => {
  const result = analyzeLegacyOutput("调试结束：已到达付款前最后一步，未向 Stripe 提交扣款\npayment_paused_before_submit", 0);
  assert.equal(result.status, "manual");
  assert.equal(result.errorCode, "payment_paused_before_submit");
  assert.equal(result.shouldRetry, false);
});

test("legacy output maps payment failures to the original manual branch", () => {
  const result = analyzeLegacyOutput("[Stripe] stripe_card_declined\nCheckout 页面已打开", 1);
  assert.equal(result.status, "manual");
  assert.equal(result.errorCode, "card_declined");
  assert.equal(result.deleteCard, true);
  assert.equal(result.shouldRetry, false);
});

test("legacy output retries transient proxy failures before payment", () => {
  const result = analyzeLegacyOutput("代理连接超时", 1);
  assert.equal(result.status, "retry");
  assert.equal(result.errorCode, "proxy_connection_failed");
  assert.equal(result.shouldRetry, true);
});

test("legacy output retries region switch failures by rotating proxy", () => {
  const result = analyzeLegacyOutput("❌ [运行时错误]: 无法将定价页切换到目标地区 PH（菲律宾），请检查后台支付地区设置。当前页面: What’s on your mind today? Think", 1);
  assert.equal(result.status, "retry");
  assert.equal(result.errorCode, "region_switch_failed");
  assert.equal(result.shouldRetry, true);
  assert.equal(proxyAttemptOutcome(result, "failed"), "failure");
});

test("proxy attempt outcome counts success and ignores unrelated failures", () => {
  assert.equal(proxyAttemptOutcome({ status: "success" }, "succeeded"), "success");
  assert.equal(proxyAttemptOutcome({ errorCode: "session_invalid" }, "failed"), "");
  assert.equal(proxyAttemptOutcome({ errorCode: "card_declined" }, "manual"), "");
  assert.equal(proxyAttemptOutcome({ errorCode: "proxy_connection_failed" }, "failed"), "failure");
});

test("dry-run worker completes with monotonic progress and trace propagation", async () => {
  const updates = [];
  const traces = [];
  const logs = [];
  const api = {
    setTraceId(value) {
      traces.push(value);
    },
    async claimTask() {
      return { claimed: true, traceId: "trace_from_queue", leaseToken: "lease_test", leaseTimeoutSeconds: 60 };
    },
    async heartbeatTask() {},
    async updateTask(taskId, body) {
      updates.push({ taskId, ...body });
    },
    async getTaskRuntime() {
      return { traceId: "trace_from_task", jobKey: "job_from_task", mode: "dry_run", cdkCode: "KC-TEST", planCode: "plus" };
    },
    async getTaskSecret() {
      return { traceId: "trace_from_task", jobKey: "job_from_task", session: "{}", token: "", cdkCode: "KC-TEST", planId: "plus" };
    },
    async getRuntimeConfig() {
      return { config: {} };
    },
    async appendRuntimeLog(entry) {
      logs.push(entry);
    },
  };
  const worker = new RechargeWorker({
    api,
    config: { workerId: "test-worker", runtimeDir: ".", legacyMaxAttempts: 1 },
  });

  await worker.process({ taskId: "task_test", mode: "dry_run", traceId: "trace_from_queue" });

  assert.deepEqual(traces.slice(0, 3), ["trace_from_queue", "trace_from_queue", "trace_from_task"]);
  assert.equal(updates[0].status, "running");
  assert.equal(updates.at(-1).status, "succeeded");
  assert.equal(updates.at(-1).progress, 100);
  assert.ok(logs.length > 0);
  assert.ok(logs.every((entry) => entry.traceId === "trace_from_task"));
  assert.ok(logs.every((entry) => entry.jobKey === "job_from_task"));
  const progress = updates.map((entry) => entry.progress).filter((value) => Number.isFinite(value));
  assert.deepEqual(progress, [...progress].sort((a, b) => a - b));
});

test("upstream mode delegates business execution to Go without reading task secrets", async () => {
  const calls = [];
  const api = {
    setTraceId(value) {
      calls.push(["trace", value]);
    },
    async claimTask(taskId, workerId) {
      calls.push(["claim", taskId, workerId]);
      return { claimed: true, traceId: "trace_go_upstream", leaseToken: "lease_go_upstream", leaseTimeoutSeconds: 60 };
    },
    async heartbeatTask(taskId, workerId, leaseToken) {
      calls.push(["heartbeat", taskId, workerId, leaseToken]);
    },
    async updateTask(taskId, body) {
      calls.push(["update", taskId, body]);
    },
    async getTaskRuntime(taskId) {
      calls.push(["runtime", taskId]);
      return { traceId: "trace_go_upstream", jobKey: "job_go_upstream", mode: "upstream" };
    },
    async getTaskSecret() {
      throw new Error("upstream worker must not read Session secrets");
    },
    async runUpstreamTask(taskId, workerId, leaseToken) {
      calls.push(["upstream", taskId, workerId, leaseToken]);
      return { status: "succeeded", traceId: "trace_go_upstream" };
    },
  };
  const worker = new RechargeWorker({
    api,
    config: { workerId: "go-boundary-test", runtimeDir: "." },
  });

  await worker.process({ taskId: "task_go_upstream", mode: "upstream", traceId: "trace_from_queue" });

  assert.deepEqual(calls.slice(0, 3).map((entry) => entry[0]), ["trace", "claim", "trace"]);
  assert.equal(calls.at(-1)[0], "upstream");
  assert.equal(calls.at(-1)[1], "task_go_upstream");
  assert.match(calls.at(-1)[2], /^go-boundary-test-/);
  assert.equal(calls.at(-1)[3], "lease_go_upstream");
  assert.ok(calls.some((entry) => entry[0] === "trace" && entry[1] === "trace_go_upstream"));
});

test("ApiClient sends worker and trace headers and parses API errors", async () => {
  const requests = [];
  const server = createServer((request, response) => {
    requests.push({ url: request.url, headers: request.headers });
    response.setHeader("Content-Type", "application/json");
    if (request.method === "GET" && request.url === "/api/v1/internal/config") {
      response.end(JSON.stringify({ config: { mode: "dry_run" } }));
      return;
    }
    response.statusCode = 409;
    response.end(JSON.stringify({ code: "task_lease_lost", message: "测试错误" }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  const api = new ApiClient({ baseUrl: `http://127.0.0.1:${address.port}`, workerToken: "worker-test", traceId: "trace-test" });
  try {
    const config = await api.getRuntimeConfig();
    assert.equal(config.config.mode, "dry_run");
    assert.equal(requests[0].headers["x-worker-token"], "worker-test");
    assert.equal(requests[0].headers["x-trace-id"], "trace-test");
    await assert.rejects(() => api.request("/internal/config", { method: "POST" }), (error) => {
      assert.equal(error.code, "task_lease_lost");
      assert.match(error.message, /测试错误/);
      return true;
    });
  } finally {
    await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});

test("ApiClient sends claim and heartbeat lease credentials with the task trace", async () => {
  const requests = [];
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    requests.push({ url: request.url, headers: request.headers, body: chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {} });
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ ok: true, claimed: true, leaseToken: "lease-1" }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  const api = new ApiClient({ baseUrl: `http://127.0.0.1:${address.port}`, workerToken: "worker-test", traceId: "trace-lease" });
  try {
    await api.claimTask("task-1", "worker-1");
    await api.heartbeatTask("task-1", "worker-1", "lease-1");
  } finally {
    await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
  assert.equal(requests.length, 2);
  assert.equal(requests[0].url, "/api/v1/internal/tasks/task-1/claim");
  assert.deepEqual(requests[0].body, { workerId: "worker-1" });
  assert.equal(requests[1].url, "/api/v1/internal/tasks/task-1/heartbeat");
  assert.deepEqual(requests[1].body, { workerId: "worker-1", leaseToken: "lease-1" });
  for (const request of requests) {
    assert.equal(request.headers["x-worker-token"], "worker-test");
    assert.equal(request.headers["x-trace-id"], "trace-lease");
  }
});

test("product generation passes the queue trace and job key into the legacy child context", async () => {
  const previousContext = {
    JOB_KEY: process.env.JOB_KEY,
    TRACE_ID: process.env.TRACE_ID,
    PRODUCT_FILES_DIR: process.env.PRODUCT_FILES_DIR,
  };
  const observed = [];
  const updates = [];
  const stores = [];
  const api = {
    setTraceId() {},
    async getProductGeneration() {
      return {
        task: {
          targetCount: 1,
          completedCount: 0,
          successCount: 0,
          failedCount: 0,
          workerCount: 1,
          aborted: false,
          traceId: "trace_from_task",
        },
      };
    },
    async updateProductGeneration(taskId, body) {
      updates.push({ taskId, ...body });
    },
    async store(action, body) {
      stores.push({ action, body });
    },
  };
  const worker = new RechargeWorker({
    api,
    config: { workerId: "product-test", runtimeDir: "." },
    productCreator: async (_cdk, onProgress, options) => {
      observed.push({ traceId: process.env.TRACE_ID, jobKey: process.env.JOB_KEY, options });
      await onProgress({ progress: 50, message: "测试成品流程" });
      return { success: true, email: "product@example.com", imapKey: "imap-test", sub2apiPath: "/tmp/product.json" };
    },
  });

  try {
    await worker.process({ kind: "product_generation", mode: "product_generation", taskId: "product_task", traceId: "trace_product" });
  } finally {
    for (const [key, value] of Object.entries(previousContext)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }

  assert.equal(observed.length, 1);
  assert.equal(observed[0].traceId, "trace_product");
  assert.equal(observed[0].jobKey, "product_task");
  assert.deepEqual(observed[0].options, { jobKey: "product_task" });
  assert.deepEqual(stores, [{ action: "addProduct", body: { email: "product@example.com", filePath: "/tmp/product.json", imapKey: "imap-test" } }]);
  assert.equal(updates.at(-1).status, "succeeded");
  assert.equal(updates.at(-1).progress, 100);
  assert.equal(process.env.JOB_KEY, previousContext.JOB_KEY);
  assert.equal(process.env.TRACE_ID, previousContext.TRACE_ID);
  assert.equal(process.env.PRODUCT_FILES_DIR, previousContext.PRODUCT_FILES_DIR);
});
