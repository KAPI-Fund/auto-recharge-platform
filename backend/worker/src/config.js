import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

function env(name, fallback = "") {
  const value = String(process.env[name] || "").trim();
  return value || fallback;
}

function resolveRuntimeDir(value) {
  const raw = String(value || "").trim();
  if (!raw || raw === "./runtime" || raw === "runtime") {
    const moduleDir = path.dirname(fileURLToPath(import.meta.url));
    const candidates = [path.resolve(moduleDir, "../../.."), path.resolve(moduleDir, "../..")];
    const projectRoot = candidates.find((candidate) =>
      existsSync(path.join(candidate, "docker-compose.yml")) ||
      existsSync(path.join(candidate, "package.json")) && existsSync(path.join(candidate, "src")),
    ) || candidates[0];
    return path.join(projectRoot, "runtime");
  }
  return path.isAbsolute(raw) ? path.resolve(raw) : path.resolve(process.cwd(), raw);
}

function resolveConfiguredPath(value, fallback) {
  const raw = String(value || "").trim();
  if (!raw) return fallback;
  return path.isAbsolute(raw) ? path.resolve(raw) : path.resolve(process.cwd(), raw);
}

function envNumber(name, fallback) {
  const value = Number(process.env[name]);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

export function loadConfig() {
  const runtimeDir = resolveRuntimeDir(process.env.RUNTIME_DIR);
  return {
    apiBaseUrl: env("API_BASE_URL", "http://127.0.0.1:8080").replace(/\/$/, ""),
    workerToken: env("WORKER_API_TOKEN", "dev-worker-token"),
    redisUrl: env("REDIS_URL", "redis://127.0.0.1:6379/0"),
    queueName: env("QUEUE_NAME", "recharge:tasks"),
    workerId: env("WORKER_ID", `worker-${process.pid}`),
    upstreamBaseUrl: env("UPSTREAM_BASE_URL", "").replace(/\/$/, ""),
    upstreamApiKey: env("UPSTREAM_API_KEY", ""),
    upstreamCreatePath: env("UPSTREAM_CREATE_PATH", "/pay"),
    upstreamStatusPath: env("UPSTREAM_STATUS_PATH", "/tasks/:id"),
    upstreamPollIntervalMs: envNumber("UPSTREAM_POLL_INTERVAL_MS", 3000),
    upstreamPollAttempts: envNumber("UPSTREAM_POLL_ATTEMPTS", 40),
    gptApiPollIntervalMs: envNumber("GPT_API_POLL_INTERVAL_MS", 5000),
    gptApiMaxPolls: envNumber("GPT_API_MAX_POLLS", 120),
    browserCheckoutUrl: env("BROWSER_CHECKOUT_URL", ""),
    browserHeadless: env("BROWSER_HEADLESS", "1") !== "0",
    browserSuccessSelector: env("BROWSER_SUCCESS_SELECTOR", ""),
    browserScreenshotDir: resolveConfiguredPath(process.env.BROWSER_SCREENSHOT_DIR, path.join(runtimeDir, "screenshots")),
    runtimeDir,
    legacyHeadful: env("LEGACY_HEADFUL", env("BROWSER_HEADLESS", "1") === "0" ? "1" : "0") === "1",
    chromiumChannel: env("CHROMIUM_CHANNEL", ""),
    checkoutMode: env("CHECKOUT_MODE", "api"),
    paymentTestMode: env("PAYMENT_TEST_MODE", ""),
    legacyMaxAttempts: envNumber("LEGACY_MAX_ATTEMPTS", 3),
    legacyProcessTimeoutMs: envNumber("LEGACY_PROCESS_TIMEOUT_MS", 900000),
    hcaptchaSolverEnabled: env("HCAPTCHA_SOLVER_ENABLED", "1") !== "0",
    hcaptchaVlmApiKey: env("HCAPTCHA_VLM_API_KEY", ""),
    hcaptchaVlmBaseUrl: env("HCAPTCHA_VLM_BASE_URL", "https://api.openai.com/v1"),
    hcaptchaVlmModel: env("HCAPTCHA_VLM_MODEL", "gpt-5.5"),
    hcaptchaVlmTimeout: envNumber("HCAPTCHA_VLM_TIMEOUT", 45),
    hcaptchaSolverTimeout: envNumber("HCAPTCHA_SOLVER_TIMEOUT", 240),
    hcaptchaSolverNoVlm: env("HCAPTCHA_SOLVER_NO_VLM", "0") === "1",
    hcaptchaCdpPort: env("HCAPTCHA_CDP_PORT", "9222"),
    hcaptchaCaptchaPlatformApiKey: env("HCAPTCHA_CAPTCHA_PLATFORM_API_KEY", ""),
    hcaptchaCaptchaPlatformApiUrl: env("HCAPTCHA_CAPTCHA_PLATFORM_API_URL", "https://api.capsolver.com"),
    hcaptchaCaptchaPlatformTimeout: envNumber("HCAPTCHA_CAPTCHA_PLATFORM_TIMEOUT", 180),
    poolEmailImapHost: env("POOL_EMAIL_IMAP_HOST", "outlook.office365.com"),
    poolEmailImapPort: envNumber("POOL_EMAIL_IMAP_PORT", 993),
    poolEmailIncludeJunk: env("POOL_EMAIL_INCLUDE_JUNK", "1") !== "0",
  };
}
