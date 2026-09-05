import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { copyFileSync, mkdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { createRequire } from "node:module";
import { runDryRun } from "./providers/dry-run.js";

const legacyEntry = fileURLToPath(new URL("./legacy-runner.cjs", import.meta.url));
const legacyRoot = fileURLToPath(new URL("./legacy/", import.meta.url));
const require = createRequire(import.meta.url);
const { startProductCreation } = require("./legacy-product.cjs");

function progressFromOutput(line, previous) {
  const text = String(line || "");
  if (/PAYMENT_SUCCESS|CHECKOUT_DEBUG_SUCCESS|支付成功|激活成功/.test(text)) return 100;
  if (/Stripe|信用卡|支付流程/.test(text)) return Math.max(previous, 68);
  if (/Checkout|支付链接|定价页/.test(text)) return Math.max(previous, 48);
  if (/Session|登录|代理/.test(text)) return Math.max(previous, 18);
  if (/套餐类型|创建订单/.test(text)) return Math.max(previous, 32);
  return previous;
}

function outputPaths(output, marker) {
  const result = new Set();
  for (const line of String(output || "").split(/\r?\n/)) {
    const index = line.indexOf(marker);
    if (index < 0) continue;
    const value = line.slice(index + marker.length).trim().split(/\s+/)[0];
    if (value) result.add(value);
  }
  return [...result];
}

function legacyArtifacts(output) {
  return {
    screenshots: [
      ...outputPaths(output, "FAILURE_SCREENSHOT:"),
      ...outputPaths(output, "SUCCESS_SCREENSHOT:"),
      ...outputPaths(output, "LIVE_SCREENSHOT:"),
    ],
    videos: outputPaths(output, "VIDEO_FILE:"),
    checkoutUrl: outputPaths(output, "CHECKOUT_URL:")[0] || "",
  };
}

function runtimeErrorFromOutput(output) {
  const match = String(output || "").match(/❌ \[运行时错误\]:\s*(.+)/);
  return match?.[1]?.trim() || "";
}

function syncLegacyArtifacts(artifacts, runtimeDir) {
  const sourceRoot = path.join(legacyRoot, "debug_screenshots");
  const targetRoot = path.join(runtimeDir || path.join(legacyRoot, "runtime"), "legacy", "debug_screenshots");
  const copied = [];
  for (const value of artifacts) {
    const source = String(value || "").trim();
    if (!source || !path.isAbsolute(source)) continue;
    const relative = path.relative(sourceRoot, source);
    if (!relative || relative.startsWith("..") || path.isAbsolute(relative)) continue;
    try {
      if (!statSync(source).isFile()) continue;
      const target = path.join(targetRoot, relative);
      mkdirSync(path.dirname(target), { recursive: true });
      copyFileSync(source, target);
      copied.push(target);
    } catch (error) {
      console.warn(`[legacy] 媒体归档失败 ${source}: ${error.message}`);
    }
  }
  return copied;
}

export function analyzeLegacyOutput(output, exitCode, timedOut = false) {
  const text = String(output || "");
  const reachedPayment = /Checkout 页面已打开|正在使用 Stripe 信用卡|\[Stripe\] Step|定价页|chatgpt\.com\/checkout/i.test(text);
  const runtimeError = runtimeErrorFromOutput(text);
  if (/payment_paused_before_submit|PAYMENT_PAUSED_BEFORE_SUBMIT/.test(text)) {
    return { status: "manual", message: "测试暂停：已到提交支付前，未执行真实付款", shouldRetry: false, errorCode: "payment_paused_before_submit" };
  }
  if (exitCode === 0 && /PAYMENT_SUCCESS|CHECKOUT_DEBUG_SUCCESS|最终校验：支付成功|支付成功/.test(text)) {
    return { status: "success", message: "激活成功", shouldRetry: false };
  }
  if (/金额校验失败/.test(text)) return { status: "failed", message: "支付金额校验失败，请检查账单地区与币种配置", shouldRetry: false, errorCode: "amount_validation_failed" };
  if (/Session 未生效|Session 无效|Session 登录失败|Session 响应异常|缺少 AccessToken|Google 登录|Sign in with Google/i.test(text)) {
    return { status: "failed", message: runtimeError || "Session 无效或已过期，请重新获取完整 Session JSON", shouldRetry: false, errorCode: "session_invalid" };
  }
  if (/无法将定价页切换到目标地区/.test(text)) return { status: "failed", message: runtimeError || "定价页地区切换失败，请检查账单地区", shouldRetry: false, errorCode: "region_switch_failed" };
  if (/未找到.*升级按钮/.test(text)) return { status: "failed", message: runtimeError || "定价页未找到对应套餐升级按钮", shouldRetry: false, errorCode: "upgrade_button_missing" };
  if (/等待 Checkout 页面超时|支付结果等待超时|Checkout.*超时/.test(text)) return { status: reachedPayment ? "manual" : "failed", message: runtimeError || "Checkout 流程超时", shouldRetry: false, errorCode: "checkout_timeout" };
  if (/无法获取支付链接|API 创建 Checkout 失败|createCheckoutSession 失败|无法打开 Checkout 页面|订单创建失败/.test(text)) return { status: "failed", message: runtimeError || "无法创建官方 Checkout 订单", shouldRetry: false, errorCode: "checkout_create_failed" };
  if (/该账号无激活权限|not_eligible|Offer not found/i.test(text)) {
    return { status: "failed", message: "该账号不符合订阅条件，请更换账号或调整账单地区", shouldRetry: false, errorCode: "not_eligible" };
  }
  if (/Browser does not support socks5 proxy authentication|socks5 proxy authentication/.test(text)) return { status: "failed", message: "代理配置错误：Playwright 不支持当前 SOCKS5 认证", shouldRetry: false, errorCode: "proxy_auth_unsupported" };
  if (/代理认证失败|代理响应异常|账号余额/.test(text)) return { status: "failed", message: "代理认证或余额异常，请联系管理员", shouldRetry: false, errorCode: "proxy_auth_failed" };
  if (/代理连接失败|代理连接超时/.test(text)) return { status: "retry", message: "代理连接失败，准备切换代理重试", shouldRetry: true, errorCode: "proxy_connection_failed" };
  if (/监测到致命拦截|You have been blocked/.test(text)) return { status: "retry", message: "检测到风控拦截，准备切换代理重试", shouldRetry: true, errorCode: "blocked" };
  if (/手机号被拒绝或系统拦截|短信验证码超时|该手机号无验证码|手机号短信验证异常/.test(text)) return { status: "retry", message: "手机号不可用，准备切换手机号重试", shouldRetry: true, errorCode: "phone_invalid", deletePhone: true };
  if (/银行卡被拒绝|stripe_card_declined|stripe_redirect_failed|stripe_redirect_canceled/.test(text)) {
    return { status: "manual", message: "银行卡被拒绝或 Stripe 驳回，请查看截图后人工处理", shouldRetry: false, errorCode: "card_declined", deleteCard: true };
  }
  if (/支付结果检测失败|支付结果等待超时/.test(text)) return { status: "manual", message: "支付结果未确认，请查看截图后人工处理", shouldRetry: false, errorCode: "payment_result_unknown" };
  if (/需要人工验证|captcha_challenge_required|Cloudflare|hCaptcha|人机验证|manual_intervention|需要人工操作|card_pool_exhausted|支付失败/.test(text) && reachedPayment) {
    return { status: "manual", message: runtimeError || "支付流程失败，请查看失败截图后人工处理", shouldRetry: false, errorCode: text.includes("card_pool_exhausted") ? "card_pool_exhausted" : "manual_intervention" };
  }
  if (/代理或网络持续超时|浏览器连接被代理多次关闭|ERR_CONNECTION_CLOSED|ECONNRESET|ECONNABORTED|Failed to fetch|网络请求失败/i.test(text) && !reachedPayment) {
    return { status: "retry", message: "代理或网络异常，准备切换代理重试", shouldRetry: true, errorCode: "worker_retryable" };
  }
  if (/OpenAI 鉴权服务异常|\/auth\/error\?error=undefined|chatgpt\.com\/auth\/error/i.test(text)) {
    return { status: "retry", message: "OpenAI 鉴权风控，准备切换代理重试", shouldRetry: true, errorCode: "openai_auth_error" };
  }
  if (/user_already_exists|该邮箱已被注册/.test(text)) return { status: "retry", message: "邮箱已被注册，准备换邮箱重试", shouldRetry: true, errorCode: "email_registered" };
  if (timedOut || /运行时错误/.test(text)) {
    return { status: reachedPayment ? "manual" : "failed", message: reachedPayment ? "支付流程超时或中断，请查看失败截图" : runtimeError || "自动化执行失败", shouldRetry: false, errorCode: timedOut ? "worker_timeout" : "runtime_error" };
  }
  if (!reachedPayment && /Stripe|pay\.openai\.com|Checkout/.test(text)) return { status: "retry", message: "支付流程未完成，准备重试", shouldRetry: true, errorCode: "checkout_incomplete" };
  return { status: reachedPayment ? "manual" : "failed", message: runtimeError || (exitCode === 0 ? "原版 Worker 未确认成功" : "原版 Worker 执行失败"), shouldRetry: false, errorCode: reachedPayment ? "payment_incomplete" : "legacy_worker_failed" };
}

function cardLast4FromOutput(output) {
  const match = String(output || "").match(/(?:已预留卡片:\s*\.\.\.|CARD\s+)(\d{4})/i);
  return match?.[1] || "";
}

export function isTaskLeaseError(error) {
  const code = String(error?.code || "").trim();
  return code === "task_lease_lost" || code === "task_lease_held" || error?.leaseLost === true;
}

function asTaskLeaseError(error) {
  if (isTaskLeaseError(error)) return error;
  const leaseError = new Error(error instanceof Error ? error.message : "Worker 租约已失效");
  leaseError.code = "task_lease_lost";
  leaseError.leaseLost = true;
  leaseError.cause = error;
  return leaseError;
}

export function startTaskLease({ api, taskId, workerId, leaseToken, leaseTimeoutSeconds = 60, onLost, onHeartbeatError }) {
  let stopped = false;
  let lostError = null;
  let inFlight = null;
  let heartbeatFailures = 0;
  const intervalMs = Math.max(3000, Math.floor(Number(leaseTimeoutSeconds || 60) * 1000 / 3));

  const markLost = (error) => {
    if (lostError || stopped) return;
    lostError = asTaskLeaseError(error);
    onLost?.(lostError);
  };

  const beat = () => {
    if (stopped || lostError || inFlight) return;
    inFlight = Promise.resolve()
      .then(() => api.heartbeatTask(taskId, workerId, leaseToken))
      .then(() => {
        heartbeatFailures = 0;
      })
      .catch((error) => {
        if (isTaskLeaseError(error)) {
          markLost(error);
          return;
        }
        heartbeatFailures += 1;
        onHeartbeatError?.(error, heartbeatFailures);
        // A worker must not keep performing a real payment after it can no
        // longer prove ownership of the task. One transient miss is tolerated;
        // consecutive misses before the lease window expires abort the child
        // and let API recovery reclaim the allocation.
        if (heartbeatFailures >= 2) markLost(error);
      })
      .finally(() => {
        inFlight = null;
      });
  };

  const timer = setInterval(beat, intervalMs);
  timer.unref?.();

  return {
    get lost() {
      return Boolean(lostError);
    },
    markLost,
    assert() {
      if (lostError) throw lostError;
    },
    stop() {
      stopped = true;
      clearInterval(timer);
    },
  };
}

function runLegacy({ taskId, jobKey, secret, taskContext = {}, config, workerId, leaseToken, onProgress, onLog, onLeaseLost, signal, browserPool }) {
  const spawnLegacy = (poolSlot) => new Promise((resolve) => {
    const childEnv = {
      ...process.env,
      API_BASE_URL: config.apiBaseUrl,
      WORKER_API_TOKEN: config.workerToken,
      WORKER_ID: workerId || "",
      WORKER_LEASE_TOKEN: leaseToken || "",
      TASK_ID: taskId || "",
      JOB_KEY: jobKey || secret.jobKey || taskId,
      TRACE_ID: secret.traceId || config.traceId || taskId,
      POOL_ID: taskContext.poolId || "",
      BUSINESS_ACCOUNT_ID: taskContext.businessAccountId || "",
      USAGE_TYPE: taskContext.usageType || "",
      CARD_ALLOCATION_ID: taskContext.allocationId || taskContext.cardAllocationId || "",
      CHATGPT_TOKEN: secret.token || "",
      CHATGPT_SESSION_JSON: secret.session || "",
      CDK_CODE: secret.cdkCode || "",
      CDK_PLAN_TYPE: secret.planType || secret.planId || "plus",
      ACTIVATION_EMAIL: secret.email || "",
      PAYMENT_REGION_OVERRIDE: secret.region || "",
      PLAN_NAME_OVERRIDE: secret.planName || "",
      PROXY: secret.proxy || "",
      HEADFUL: config.legacyHeadful ? "1" : "0",
      CHROMIUM_CHANNEL: config.chromiumChannel || "",
      CHECKOUT_MODE: config.checkoutMode || "api",
      PAYMENT_TEST_MODE: config.paymentTestMode || "",
      HCAPTCHA_SOLVER_ENABLED: config.hcaptchaSolverEnabled ? "1" : "0",
      HCAPTCHA_VLM_API_KEY: config.hcaptchaVlmApiKey || "",
      CTF_VLM_API_KEY: config.hcaptchaVlmApiKey || "",
      HCAPTCHA_VLM_BASE_URL: config.hcaptchaVlmBaseUrl || "",
      CTF_VLM_BASE_URL: config.hcaptchaVlmBaseUrl || "",
      HCAPTCHA_VLM_MODEL: config.hcaptchaVlmModel || "",
      CTF_VLM_MODEL: config.hcaptchaVlmModel || "",
      HCAPTCHA_VLM_TIMEOUT: String(config.hcaptchaVlmTimeout || "45"),
      CTF_VLM_TIMEOUT: String(config.hcaptchaVlmTimeout || "45"),
      HCAPTCHA_SOLVER_TIMEOUT: String(config.hcaptchaSolverTimeout || "240"),
      HCAPTCHA_SOLVER_NO_VLM: config.hcaptchaSolverNoVlm ? "1" : "0",
      HCAPTCHA_CDP_PORT: String(config.hcaptchaCdpPort || "9222"),
      CDP_PORT: String(config.hcaptchaCdpPort || "9222"),
      CDP_URL: `http://127.0.0.1:${String(config.hcaptchaCdpPort || "9222")}`,
      HCAPTCHA_CAPTCHA_PLATFORM_API_KEY: config.hcaptchaCaptchaPlatformApiKey || "",
      CAPTCHA_PLATFORM_API_KEY: config.hcaptchaCaptchaPlatformApiKey || "",
      HCAPTCHA_CAPTCHA_PLATFORM_API_URL: config.hcaptchaCaptchaPlatformApiUrl || "https://api.capsolver.com",
      CAPTCHA_PLATFORM_API_URL: config.hcaptchaCaptchaPlatformApiUrl || "https://api.capsolver.com",
      HCAPTCHA_CAPTCHA_PLATFORM_TIMEOUT: String(config.hcaptchaCaptchaPlatformTimeout || "180"),
      CAPTCHA_PLATFORM_TIMEOUT: String(config.hcaptchaCaptchaPlatformTimeout || "180"),
      HCAPTCHA_SOLVER_OUT_DIR: config.runtimeDir ? path.join(config.runtimeDir, "hcaptcha") : "",
      LEGACY_DEBUG_DIR: config.runtimeDir ? path.join(config.runtimeDir, "legacy") : "",
      RECORD_VIDEO: "1",
      CHECKOUT_DEBUG_ONLY: secret.cdkCode === "[checkout-debug]" ? "1" : "0",
      ...(browserPool?.buildPoolEnv(poolSlot) || {}),
    };
    const child = spawn(process.execPath, [legacyEntry], {
      cwd: path.dirname(legacyEntry),
      env: childEnv,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    let progress = 5;
    let settled = false;
    let timeout = null;
    let timedOut = false;
    const abortChild = () => {
      if (!settled) child.kill("SIGTERM");
    };
    if (signal?.aborted) abortChild();
    else signal?.addEventListener("abort", abortChild, { once: true });
    const buffers = { stdout: "", stderr: "" };
    const finish = (result) => {
      if (settled) return;
      settled = true;
      if (timeout) clearTimeout(timeout);
      signal?.removeEventListener("abort", abortChild);
      resolve(result);
    };
    const consume = (chunk, source) => {
      const text = chunk.toString();
      output += text;
      buffers[source] += text;
      const chunks = buffers[source].split(/\r?\n/);
      buffers[source] = chunks.pop() || "";
      const lines = chunks.filter(Boolean);
      for (const line of lines) {
        progress = progressFromOutput(line, progress);
        void Promise.resolve(onProgress(progress, line, output.slice(-12000))).catch((error) => {
          if (isTaskLeaseError(error)) onLeaseLost?.(error);
        });
        const captcha = /captcha|hcaptcha|cloudflare|人机验证/i.test(line);
        void onLog?.(line, source, captcha ? "captcha" : source);
        if (source === "stderr") console.error(`[legacy:${taskId}] ${line}`);
        else console.log(`[legacy:${taskId}] ${line}`);
      }
    };
    const flush = () => {
      for (const source of ["stdout", "stderr"]) {
        const line = buffers[source].trim();
        if (!line) continue;
        buffers[source] = "";
        progress = progressFromOutput(line, progress);
        void Promise.resolve(onProgress(progress, line, output.slice(-12000))).catch((error) => {
          if (isTaskLeaseError(error)) onLeaseLost?.(error);
        });
        const captcha = /captcha|hcaptcha|cloudflare|人机验证/i.test(line);
        void onLog?.(line, source, captcha ? "captcha" : source);
      }
    };
    child.stdout.on("data", (chunk) => consume(chunk, "stdout"));
    child.stderr.on("data", (chunk) => consume(chunk, "stderr"));
    child.on("error", (error) => finish({ status: "failed", message: error.message, output, analysis: { status: "failed", message: error.message, shouldRetry: false, errorCode: "worker_spawn_failed" } }));
    child.on("close", (code, signal) => {
      flush();
      const analysis = analyzeLegacyOutput(output, code, timedOut);
      if (signal && analysis.status === "success") analysis.status = "failed";
      const succeeded = analysis.status === "success" && code === 0;
      const message = succeeded ? "原版 Playwright Worker 执行成功" : (analysis.message || (signal ? `Worker 被信号 ${signal} 终止` : "原版 Playwright Worker 执行失败"));
      const artifacts = legacyArtifacts(output);
      syncLegacyArtifacts([...artifacts.screenshots, ...artifacts.videos], config.runtimeDir);
      finish({ status: succeeded ? "succeeded" : analysis.status === "manual" ? "manual" : "failed", message, output, exitCode: code, analysis, cardLast4: cardLast4FromOutput(output), ...artifacts, failureScreenshots: [...artifacts.screenshots, ...artifacts.videos].join("\n") });
    });
    const timeoutMs = Number(config.legacyProcessTimeoutMs || 900000);
    timeout = setTimeout(() => {
      timedOut = true;
      output += "\n[TIMEOUT] 原版 Worker 超时，正在终止子进程。\n";
      void onLog?.("[TIMEOUT] 原版 Worker 超时，正在终止子进程。", "worker", "stderr");
      child.kill("SIGTERM");
      setTimeout(() => child.kill("SIGKILL"), 5000).unref();
    }, timeoutMs);
  });
  return browserPool ? browserPool.withBrowserSlot(taskId, spawnLegacy) : spawnLegacy(null);
}

export class RechargeWorker {
  constructor({ api, config, browserPool = null, productCreator = startProductCreation }) {
    this.api = api;
    this.config = config;
    this.browserPool = browserPool;
    this.productCreator = productCreator;
  }

  async process(message) {
    const traceId = String(message?.traceId || message?.trace_id || message?.taskId || "").trim();
    let activeTraceId = traceId;
    this.api.setTraceId(traceId);
    if (String(message?.kind || "").trim() === "product_generation" || String(message?.mode || "").trim() === "product_generation") {
      await this.processProductGeneration(message, activeTraceId);
      return;
    }
    const taskId = String(message?.taskId || "").trim();
    if (!taskId) {
      return;
    }
    if (String(message?.mode || "").trim() === "protocol") {
      const skip = new Error("protocol task skipped by browser worker");
      skip.code = "skip_foreign_task";
      throw skip;
    }
    const workerId = `${this.config.workerId}-${randomUUID().slice(0, 8)}`;
    let leaseToken = "";
    let claimed = false;
    let lease = null;
    let abortController = null;
    let updateOwnedTask = null;
    try {
      const claim = await this.api.claimTask(taskId, workerId);
      if (claim?.claimed === false || claim?.terminal) {
        return;
      }
      leaseToken = String(claim?.leaseToken || "").trim();
      if (!leaseToken) {
        throw new Error("Worker claim 未返回租约令牌");
      }
      claimed = true;
      activeTraceId = String(claim.traceId || traceId).trim();
      this.api.setTraceId(activeTraceId);
      abortController = new AbortController();
      const leaseExpiry = Date.parse(String(claim.leaseExpiresAt || ""));
      const leaseSeconds = Number(claim.leaseTimeoutSeconds) || (Number.isFinite(leaseExpiry) ? Math.max(5, (leaseExpiry - Date.now()) / 1000) : 60);
      lease = startTaskLease({
        api: this.api,
        taskId,
        workerId,
        leaseToken,
        leaseTimeoutSeconds: leaseSeconds,
        onLost: () => abortController.abort(),
        onHeartbeatError: (error) => console.warn(`[worker:${taskId}] heartbeat failed:`, error?.message || error),
      });

      updateOwnedTask = async (body) => {
        lease.assert();
        return this.api.updateTask(taskId, { ...body, workerId, leaseToken });
      };
      const taskMeta = await this.api.getTaskRuntime(taskId, workerId, leaseToken);
      activeTraceId = String(taskMeta.traceId || traceId).trim();
      this.api.setTraceId(activeTraceId);
      const taskJobKey = String(taskMeta.jobKey || taskId).trim();
      const mode = String(message.mode || taskMeta.mode || "browser");
      if (mode === "upstream") {
        // Upstream business execution is owned by Go. Node only consumes the
        // queue item and waits for the Go API to persist the final result.
        await this.api.runUpstreamTask(taskId, workerId, leaseToken);
        return;
      }

      const secret = mode === "dry_run"
        ? { traceId: activeTraceId, jobKey: taskJobKey, cdkCode: taskMeta.cdkCode || "", planId: taskMeta.planCode || "" }
        : await this.api.getTaskSecret(taskId, workerId, leaseToken);
      const runtime = await this.api.getRuntimeConfig().catch(() => ({ config: {} }));
      const persisted = runtime.config || {};
      const runtimeConfig = {
        ...this.config,
        upstreamBaseUrl: persisted.upstreamBaseURL || this.config.upstreamBaseUrl,
        upstreamCreatePath: persisted.upstreamCreatePath || this.config.upstreamCreatePath,
        upstreamStatusPath: persisted.upstreamStatusPath || this.config.upstreamStatusPath,
        browserCheckoutUrl: persisted.browserCheckoutURL || this.config.browserCheckoutUrl,
        browserHeadless: persisted.browserHeadless !== undefined ? persisted.browserHeadless !== "false" : this.config.browserHeadless,
        legacyHeadful: persisted.browserHeadless !== undefined
          ? ["false", "0", "no"].includes(String(persisted.browserHeadless).trim().toLowerCase())
          : this.config.legacyHeadful,
        runtimeDir: persisted.runtimeDir || this.config.runtimeDir,
        checkoutMode: persisted.checkoutMode || this.config.checkoutMode || "api",
        hcaptchaSolverEnabled: persisted.hcaptchaSolverEnabled !== undefined ? persisted.hcaptchaSolverEnabled !== "0" : this.config.hcaptchaSolverEnabled,
        hcaptchaVlmApiKey: persisted.hcaptchaVlmApiKey || this.config.hcaptchaVlmApiKey,
        hcaptchaVlmBaseUrl: persisted.hcaptchaVlmBaseUrl || this.config.hcaptchaVlmBaseUrl,
        hcaptchaVlmModel: persisted.hcaptchaVlmModel || this.config.hcaptchaVlmModel,
        hcaptchaVlmTimeout: Number(persisted.hcaptchaVlmTimeout || this.config.hcaptchaVlmTimeout),
        hcaptchaSolverTimeout: Number(persisted.hcaptchaSolverTimeout || this.config.hcaptchaSolverTimeout),
        hcaptchaSolverNoVlm: persisted.hcaptchaSolverNoVlm !== undefined ? persisted.hcaptchaSolverNoVlm === "1" : this.config.hcaptchaSolverNoVlm,
        hcaptchaCdpPort: persisted.hcaptchaCdpPort || this.config.hcaptchaCdpPort,
        hcaptchaCaptchaPlatformApiKey: persisted.hcaptchaCaptchaPlatformApiKey || this.config.hcaptchaCaptchaPlatformApiKey,
        hcaptchaCaptchaPlatformApiUrl: persisted.hcaptchaCaptchaPlatformApiUrl || this.config.hcaptchaCaptchaPlatformApiUrl,
        hcaptchaCaptchaPlatformTimeout: Number(persisted.hcaptchaCaptchaPlatformTimeout || this.config.hcaptchaCaptchaPlatformTimeout),
        cdkCode: secret.cdkCode || "",
      };
      const updateProgress = async (progress, messageText, rawOutput = "", extra = {}) => {
        const message = String(messageText || "").trim();
        await updateOwnedTask({
          status: "running",
          progress,
          message,
          rawOutput,
          ...extra,
        });
      };
      let result;
      if (mode === "browser" || mode === "legacy") {
        const maxAttempts = Math.max(1, Number(this.config.legacyMaxAttempts || 3));
        let combinedOutput = "";
        let finalResult = null;
        for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
          await updateOwnedTask({ status: "running", progress: Math.min(8, 4 + attempt), message: `正在进行第 ${attempt}/${maxAttempts} 次尝试`, attempt });
          lease.assert();
          const proxy = await this.api.store("getActiveProxy").catch(() => ({ proxy: "" }));
          const resultSecret = { ...secret, proxy: proxy.proxy || "" };
          const attemptResult = await runLegacy({
            taskId,
            jobKey: secret.jobKey || taskId,
            secret: resultSecret,
            taskContext: taskMeta,
            config: runtimeConfig,
            workerId,
            leaseToken,
            browserPool: this.browserPool,
            signal: abortController.signal,
            onLeaseLost: (error) => {
              lease.markLost(error);
              abortController.abort();
            },
            onProgress: updateProgress,
            onLog: (text, source, level) => this.api.appendRuntimeLog({
              taskId,
              jobKey: secret.jobKey || taskId,
              traceId: activeTraceId,
              level,
              source: `legacy/${source}`,
              text,
              workerId,
              leaseToken,
            }).catch(() => undefined)
          });
          combinedOutput += `${combinedOutput ? "\n\n" : ""}===== ATTEMPT ${attempt}${attemptResult.cardLast4 ? ` | CARD ${attemptResult.cardLast4}` : ""} =====\n${attemptResult.output || ""}`;
          finalResult = { ...attemptResult, attempt, output: combinedOutput };
          if (attemptResult.status === "succeeded" || !attemptResult.analysis?.shouldRetry || attempt >= maxAttempts) break;
          await updateOwnedTask({ status: "running", progress: Math.min(90, 20 + attempt * 8), message: attemptResult.analysis.message, attempt, rawOutput: combinedOutput });
        }
        result = finalResult || { status: "failed", message: "原版 Worker 未返回结果", output: combinedOutput, analysis: { errorCode: "worker_empty_result" } };
      } else {
        // Dry-run has no child stdout stream. Keep one synthetic log per
        // stage without duplicating the legacy child output path.
        const updateDryRunProgress = async (progress, messageText, rawOutput = "", extra = {}) => {
          await updateProgress(progress, messageText, rawOutput, extra);
          const message = String(messageText || "").trim();
          if (message) {
            await this.api.appendRuntimeLog({
              taskId,
              jobKey: taskJobKey,
              traceId: activeTraceId,
              level: "stdout",
              source: "worker/dry-run",
              text: message,
              workerId,
              leaseToken,
            }).catch(() => undefined);
          }
        };
        result = await runDryRun({ onProgress: updateDryRunProgress });
      }
      const finalAnalysis = result.analysis || {};
      const finalErrorCode = finalAnalysis.errorCode || (result.status === "failed" ? "worker_failed" : result.status === "manual" ? "manual_intervention" : "");
      await updateOwnedTask({
        status: result.status,
        progress: Number.isFinite(result.progress) ? result.progress : result.status === "succeeded" ? 100 : result.status === "failed" ? 99 : 92,
        message: result.message,
        upstreamOrderId: result.orderId || "",
        gptApiOrderId: result.gptApiOrderId || "",
        gptApiTaskId: result.gptApiTaskId || "",
        gptApiRaw: result.gptApiRaw || "",
        gptApiTopupCode: result.gptApiTopupCode || "",
        rawOutput: result.output || result.gptApiRaw || "",
        failureScreenshots: result.failureScreenshots || legacyArtifacts(result.output || "").screenshots.concat(legacyArtifacts(result.output || "").videos).join("\n"),
        cardLast4: result.cardLast4 || cardLast4FromOutput(result.output || ""),
        errorCode: finalErrorCode,
        errorMessage: result.status === "succeeded" ? "" : result.message,
        attempt: result.attempt,
      });
    } catch (error) {
      if (isTaskLeaseError(error) || lease?.lost) {
        console.warn(`[worker:${taskId}] task lease lost; acknowledging queue item without overwriting task state`);
        return;
      }
      if (!claimed) {
        throw error;
      }
      const message = error instanceof Error ? error.message : "Worker 执行失败";
      try {
        await updateOwnedTask({
          status: "failed",
          progress: 0,
          message,
          errorCode: message.includes("浏览器池繁忙") ? "browser_pool_busy" : "worker_failed",
          errorMessage: message,
        });
      } catch (updateError) {
        if (isTaskLeaseError(updateError) || lease?.lost) {
          console.warn(`[worker:${taskId}] task lease lost while recording failure`);
          return;
        }
        throw updateError;
      }
    } finally {
      lease?.stop();
    }
  }

  async processProductGeneration(message, traceId = "") {
    const taskId = String(message?.taskId || "").trim();
    if (!taskId) return;

    const initial = await this.api.getProductGeneration(taskId);
    const remoteTask = initial.task || initial;
    const targetCount = Math.max(1, Number(remoteTask.targetCount ?? remoteTask.target_count ?? 1));
    const workerCount = Math.max(1, Math.min(targetCount, Number(remoteTask.workerCount ?? remoteTask.worker_count ?? 1)));
    const legacyTraceId = String(traceId || message?.traceId || message?.trace_id || remoteTask.traceId || remoteTask.trace_id || taskId).trim();
    const state = {
      targetCount,
      completedCount: Number(remoteTask.completedCount ?? remoteTask.completed_count ?? 0),
      successCount: Number(remoteTask.successCount ?? remoteTask.success_count ?? 0),
      failedCount: Number(remoteTask.failedCount ?? remoteTask.failed_count ?? 0),
      workerCount,
      aborted: Boolean(remoteTask.aborted),
      lastError: "",
    };
    const itemProgress = new Map();
    let nextIndex = 0;
    let updateChain = Promise.resolve();

    const summary = () => JSON.stringify({
      kind: "admin_product_generation",
      targetCount: state.targetCount,
      completedCount: state.completedCount,
      successCount: state.successCount,
      failedCount: state.failedCount,
      workerCount: state.workerCount,
      aborted: state.aborted,
      lastError: state.lastError,
      resumedFromJobKey: remoteTask.resumedFromJobKey || remoteTask.resumed_from_job_key || null,
    });

    const progress = () => {
      const running = [...itemProgress.values()].reduce((sum, value) => sum + Math.max(0, Math.min(99, Number(value) || 0)), 0);
      return Math.max(0, Math.min(99, Math.floor(((state.completedCount * 100) + running) / state.targetCount)));
    };

    const update = (status, messageText, forcedProgress = null) => {
      const body = {
        status,
        progress: forcedProgress === null ? progress() : forcedProgress,
        message: String(messageText || ""),
        rawOutput: summary(),
        completedCount: state.completedCount,
        successCount: state.successCount,
        failedCount: state.failedCount,
        aborted: state.aborted,
      };
      updateChain = updateChain.then(() => this.api.updateProductGeneration(taskId, body)).catch((error) => {
        console.error(`[product:${taskId}] 状态更新失败:`, error);
      });
      return updateChain;
    };

    const refreshStopFlag = async () => {
      try {
        const latest = await this.api.getProductGeneration(taskId);
        const row = latest.task || latest;
        if (row.aborted === true || row.status === "failed" && row.aborted === true) state.aborted = true;
      } catch (error) {
        console.warn(`[product:${taskId}] 读取停止状态失败:`, error?.message || error);
      }
      return state.aborted;
    };

    const isFatal = (error) => /系统维护中|余额不足|无法获取有效的 Access Token|页面仍无法正常显示|资产池枯竭|支付已成功但协议提取失败|协议提取失败/.test(String(error?.message || error));
    const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

    const previousLegacyContext = {
      JOB_KEY: process.env.JOB_KEY,
      TRACE_ID: process.env.TRACE_ID,
      PRODUCT_FILES_DIR: process.env.PRODUCT_FILES_DIR,
    };
    process.env.JOB_KEY = taskId;
    process.env.TRACE_ID = legacyTraceId;
    process.env.PRODUCT_FILES_DIR = path.join(this.config.runtimeDir || "./runtime", "product_files");
    await update("running", "Worker 已接管成品生产任务", 1);

    const runItem = async (index) => {
      itemProgress.set(index, 1);
      while (true) {
        if (await refreshStopFlag()) return;
        try {
          await update("running", `正在生产第 ${index}/${state.targetCount} 个成品号...`);
          const result = await this.productCreator("", async (payload = {}) => {
            itemProgress.set(index, Number(payload.progress) || 1);
            await update("running", `第 ${index}/${state.targetCount} 个: ${payload.message || "正在执行原版成品流程..."}`);
          }, { jobKey: taskId });
          if (!result?.success) throw new Error("原版成品流程未返回成功结果");
          await this.api.store("addProduct", {
            email: result.email || "",
            filePath: result.sub2apiPath || result.sub2apiFile || "",
            imapKey: result.imapKey || "",
          });
          state.successCount += 1;
          state.completedCount += 1;
          itemProgress.delete(index);
          await update("running", `已完成 ${state.completedCount}/${state.targetCount} 个成品号`);
          return;
        } catch (error) {
          state.lastError = error?.message || String(error);
          if (isFatal(error)) {
            state.failedCount += 1;
            state.aborted = true;
            await update("failed", `成品生产终止：${state.lastError}`);
            return;
          }
          if (await refreshStopFlag()) return;
          await update("running", `第 ${index}/${state.targetCount} 个失败，准备重试：${state.lastError}`);
          await sleep(3000);
        }
      }
    };

    const loop = async () => {
      while (true) {
        if (await refreshStopFlag()) return;
        const index = ++nextIndex;
        if (index > state.targetCount) return;
        await runItem(index);
      }
    };

    try {
      await Promise.all(Array.from({ length: workerCount }, () => loop()));
      const finalStatus = state.aborted || state.failedCount > 0 ? "failed" : "succeeded";
      const finalMessage = finalStatus === "succeeded"
        ? `成功生产 ${state.successCount} 个成品号`
        : `生产已停止，已成功 ${state.successCount} 个${state.lastError ? `，原因：${state.lastError}` : ""}`;
      await update(finalStatus, finalMessage, 100);
      await updateChain;
    } catch (error) {
      state.lastError = error?.message || String(error);
      state.aborted = true;
      await update("failed", `后台成品生产异常：${state.lastError}`, 100);
      await updateChain;
    } finally {
      for (const [key, value] of Object.entries(previousLegacyContext)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    }
  }
}

export { syncLegacyArtifacts };
