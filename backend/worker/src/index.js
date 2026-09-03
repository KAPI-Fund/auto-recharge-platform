import { loadConfig } from "./config.js";
import { ApiClient } from "./api-client.js";
import { TaskQueue } from "./queue.js";
import { RechargeWorker } from "./worker.js";
import { createServer } from "node:http";
import { previewMailbox } from "./mailbox-preview.js";
import * as browserPool from "./browser-pool.js";
import { createRequire } from "node:module";
import { randomUUID } from "node:crypto";

const require = createRequire(import.meta.url);
const hcaptchaSolver = require("./legacy/hcaptcha-solver.js");
const captchaPlatform = require("./legacy/captcha-platform.js");

const config = loadConfig();
const queue = new TaskQueue(config);
const api = new ApiClient({ baseUrl: config.apiBaseUrl, workerToken: config.workerToken });
const worker = new RechargeWorker({ api, config, browserPool });

function controlToken(request) {
  const header = request.headers.authorization || "";
  return String(request.headers["x-worker-token"] || header.replace(/^Bearer\s+/i, "")).trim();
}

function startControlServer() {
  const port = Number(process.env.BROWSER_POOL_CONTROL_PORT || 8091);
  if (!Number.isFinite(port) || port <= 0) return null;
  const server = createServer(async (request, response) => {
    response.setHeader("Content-Type", "application/json; charset=utf-8");
    const traceId = String(request.headers["x-trace-id"] || `worker-${randomUUID()}`).trim().slice(0, 96);
    response.setHeader("X-Trace-ID", traceId);
    const send = (payload, statusCode = 200) => {
      response.statusCode = statusCode;
      response.end(JSON.stringify({ ...payload, traceId, trace_id: traceId }));
    };
    if (controlToken(request) !== config.workerToken) {
      send({ success: false, message: "Worker control unauthorized" }, 401);
      return;
    }
    const url = new URL(request.url || "/", `http://${request.headers.host || "127.0.0.1"}`);
    try {
      if (request.method === "GET" && (url.pathname === "/health" || url.pathname === "/stats")) {
        send({ success: true, pool: browserPool.getDetailedStats() });
        return;
      }
      let body = {};
      if (request.method === "POST") {
        const chunks = [];
        for await (const chunk of request) chunks.push(chunk);
        body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      }
      if (request.method === "POST" && url.pathname === "/mode") {
        const enabled = body.enabled === true || body.mode === "enabled";
        browserPool.setRuntimeEnabled(enabled);
        if (enabled) await browserPool.initBrowserPool();
        else await browserPool.shutdownBrowserPool();
        send({ success: true, pool: browserPool.getDetailedStats() });
        return;
      }
      if (request.method === "POST" && url.pathname === "/reload") {
        const result = await browserPool.reloadBrowserPool(body.size || null);
        send({ success: true, result, pool: browserPool.getDetailedStats() });
        return;
      }
      if (request.method === "POST" && url.pathname === "/mailbox-preview") {
        const result = await previewMailbox({
          ...body,
          imapHost: body.imapHost || config.poolEmailImapHost,
          imapPort: body.imapPort || config.poolEmailImapPort,
          includeJunk: body.includeJunk ?? config.poolEmailIncludeJunk,
        });
        send(result);
        return;
      }
      if (request.method === "POST" && url.pathname === "/hcaptcha/health") {
        const status = await hcaptchaSolver.checkHcaptchaSolverHealth({
          vlm_api_key: body.vlm_api_key || "",
          vlm_base_url: body.vlm_base_url || "",
          vlm_model: body.vlm_model || "",
          no_vlm: body.no_vlm === true,
        });
        send({ success: true, status });
        return;
      }
      if (request.method === "POST" && url.pathname === "/hcaptcha/test-vlm") {
        const result = await hcaptchaSolver.testVlmConnectivity({
          vlm_api_key: body.vlm_api_key || "",
          vlm_base_url: body.vlm_base_url || "",
          vlm_model: body.vlm_model || "",
          vlm_timeout: body.vlm_timeout || "",
        });
        send({ success: true, result });
        return;
      }
      if (request.method === "POST" && url.pathname === "/hcaptcha/test-captcha-platform") {
        const result = await captchaPlatform.testCaptchaPlatformConnectivity({
          captcha_platform_api_key: body.captcha_platform_api_key || "",
          captcha_platform_api_url: body.captcha_platform_api_url || "",
          captcha_platform_timeout: body.captcha_platform_timeout || "",
        });
        send({ success: true, result });
        return;
      }
      send({ success: false, message: "Not found" }, 404);
    } catch (error) {
      send({ success: false, message: error instanceof Error ? error.message : String(error) }, 409);
    }
  });
  server.listen(port, "0.0.0.0", () => console.log(`[worker] browser pool control listening on :${port}`));
  return server;
}

await browserPool.initBrowserPool();
const recovered = await queue.recover();
if (recovered > 0) console.log(`[worker] recovered ${recovered} in-flight task(s)`);
const controlServer = startControlServer();

console.log(`[worker] ${config.workerId} listening on ${config.queueName}`);

async function loop() {
  while (true) {
    const message = await queue.next();
    if (!message) {
      continue;
    }
    try {
      await worker.process(message);
      await queue.acknowledge(message);
    } catch (error) {
      if (error?.code === "skip_foreign_task") {
        await queue.requeue(message).catch(() => undefined);
        await new Promise((resolve) => setTimeout(resolve, 750));
        continue;
      }
      console.error("[worker] task loop error", error);
      await queue.requeue(message).catch(() => undefined);
    }
  }
}

const shutdown = async () => {
  await queue.close();
  await browserPool.shutdownBrowserPool();
  controlServer?.close();
  process.exit(0);
};
process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
loop().catch((error) => {
  console.error("[worker] fatal error", error);
  process.exit(1);
});
