export type JsonMap = Record<string, unknown>;

const prefix = "/legacy-api";
const requestTimeoutMs = 10_000;

function requestTraceId() {
  if (typeof window === "undefined") return "";
  if (typeof window.crypto?.randomUUID === "function") {
    return `web-${window.crypto.randomUUID()}`;
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function traceError(message: string, traceId: string) {
  const error = new Error(message);
  if (traceId) {
    Object.defineProperty(error, "traceId", { value: traceId, enumerable: false });
  }
  return error;
}

function token(key: string) {
  return typeof window === "undefined"
    ? ""
    : window.localStorage.getItem(key) || "";
}

export function adminToken() {
  return token("kc_admin_token");
}
export function secondaryToken() {
  return token("kc_secondary_token");
}
export function setAuthTokens(admin: string, secondary = "") {
  if (typeof window === "undefined") return;
  window.localStorage.setItem("kc_admin_token", admin);
  if (secondary) window.localStorage.setItem("kc_secondary_token", secondary);
  else window.localStorage.removeItem("kc_secondary_token");
}
export function clearAuthTokens() {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem("kc_admin_token");
  window.localStorage.removeItem("kc_secondary_token");
}

export async function legacyRequest<T = JsonMap>(
  path: string,
  options: RequestInit = {},
  needsSecondary = false,
): Promise<T> {
  const headers = new Headers(options.headers || {});
  headers.set("Content-Type", "application/json");
  const admin = adminToken();
  const secondary = secondaryToken();
  if (admin) headers.set("X-Admin-Token", admin);
  if (secondary && needsSecondary) headers.set("X-Admin-Secondary-Token", secondary);
  const traceId = headers.get("X-Trace-ID") || requestTraceId();
  if (traceId) headers.set("X-Trace-ID", traceId);
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), requestTimeoutMs);
  let response: Response;
  try {
    response = await fetch(`${prefix}${path}`, {
      ...options,
      headers,
      cache: "no-store",
      signal: options.signal || controller.signal,
    });
  } catch (reason) {
    if (reason instanceof DOMException && reason.name === "AbortError") {
      throw traceError("请求超时，请检查 API 服务状态", traceId);
    }
    throw traceError(reason instanceof Error ? reason.message : "请求失败", traceId);
  } finally {
    window.clearTimeout(timeout);
  }
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const responseTraceId = response.headers.get("X-Trace-ID") || body?.traceId || body?.trace_id || traceId;
    throw traceError(
      body?.message || body?.error || `请求失败 (${response.status})`,
      responseTraceId,
    );
  }
  return body as T;
}

export async function legacyDownload(
  path: string,
  options: RequestInit = {},
  needsSecondary = false,
): Promise<Blob> {
  const headers = new Headers(options.headers || {});
  const admin = adminToken();
  const secondary = secondaryToken();
  if (admin) headers.set("X-Admin-Token", admin);
  if (secondary && needsSecondary) headers.set("X-Admin-Secondary-Token", secondary);
  const traceId = headers.get("X-Trace-ID") || requestTraceId();
  if (traceId) headers.set("X-Trace-ID", traceId);
  const response = await fetch(`${prefix}${path}`, {
    ...options,
    headers,
    cache: "no-store",
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const responseTraceId = response.headers.get("X-Trace-ID") || body?.traceId || body?.trace_id || traceId;
    throw traceError(
      body?.message || body?.error || `请求失败 (${response.status})`,
      responseTraceId,
    );
  }
  return response.blob();
}

export function loginAdmin(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/login", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function sendTelegramCode(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/login/send-tg-code", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function verifyAdmin2FA(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/login/verify-2fa", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function getPublicAdminPaths() {
  return legacyRequest<JsonMap>("/public/admin-paths");
}
export function getAdminSession() {
  return legacyRequest<JsonMap>("/admin/session");
}
export function getSecurityStatus() {
  return legacyRequest<JsonMap>("/admin/security/status");
}
export function getSecondarySession() {
  return legacyRequest<JsonMap>("/admin/secondary/session", {}, true);
}
export function verifySecondaryPassword(password: string) {
  return legacyRequest<JsonMap>("/admin/verify-secondary", {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}
export function save2FAMode(mode: string) {
  return legacyRequest<JsonMap>("/admin/security/2fa-mode", {
    method: "POST",
    body: JSON.stringify({ mode }),
  });
}
export function saveAdminPaths(loginPath: string, panelPath: string) {
  return legacyRequest<JsonMap>("/admin/security/paths", {
    method: "POST",
    body: JSON.stringify({ loginPath, panelPath }),
  });
}
export function setupTOTP() {
  return legacyRequest<JsonMap>("/admin/2fa/setup", { method: "POST" });
}
export function confirmTOTP(code: string) {
  return legacyRequest<JsonMap>("/admin/2fa/confirm", {
    method: "POST",
    body: JSON.stringify({ code }),
  });
}
export function disableTOTP(currentPassword: string, code = "") {
  return legacyRequest<JsonMap>("/admin/2fa/disable", {
    method: "POST",
    body: JSON.stringify({ currentPassword, code }),
  });
}
export function changeAdminPassword(
  currentPassword: string,
  newPassword: string,
) {
  return legacyRequest<JsonMap>("/admin/change-password", {
    method: "POST",
    body: JSON.stringify({ currentPassword, newPassword }),
  });
}
export function changeSecondaryPassword(
  currentPassword: string,
  newPassword: string,
) {
  return legacyRequest<JsonMap>("/admin/change-secondary-password", {
    method: "POST",
    body: JSON.stringify({ currentPassword, newPassword }),
  });
}

export function getAdminData() {
  return legacyRequest<JsonMap>("/admin/data");
}
export function getRuntime() {
  return legacyRequest<JsonMap>("/public/runtime");
}
export function getPaymentRegion() {
  return legacyRequest<JsonMap>("/public/payment-region");
}

export function getCDKStatus(code: string) {
  return legacyRequest<JsonMap>(`/cdk/query?cdk=${encodeURIComponent(code)}`);
}
export function getTaskLogs(input: number | { page?: number; pageSize?: number } = 200) {
	const params = new URLSearchParams();
	if (typeof input === "number") {
		params.set("limit", String(input));
	} else {
		params.set("page", String(input.page || 1));
		params.set("page_size", String(input.pageSize || 12));
	}
	return legacyRequest<JsonMap>(`/admin/task-logs?${params.toString()}`);
}
export function deleteTaskLog(jobKey: string) {
  return legacyRequest<JsonMap>(
    `/admin/task-logs/${encodeURIComponent(jobKey)}`,
    { method: "DELETE" },
  );
}
export function getRuntimeLogs(
  limit = 200,
  options: { after?: number; tail?: boolean } = {},
) {
  const params = new URLSearchParams({ limit: String(limit) });
  if (options.after !== undefined) params.set("after", String(options.after));
  if (options.tail) params.set("tail", "1");
  return legacyRequest<JsonMap>(`/admin/runtime-logs?${params.toString()}`);
}
export function clearRuntimeLogs() {
  return legacyRequest<JsonMap>("/admin/runtime-logs/clear", {
    method: "POST",
  });
}
export function getLoginLogs(input: number | { page?: number; pageSize?: number } = 100) {
	const params = new URLSearchParams();
	if (typeof input === "number") {
		params.set("limit", String(input));
	} else {
		const page = input.page || 1;
		const pageSize = input.pageSize || 20;
		params.set("limit", String(pageSize));
		params.set("offset", String((page - 1) * pageSize));
	}
	return legacyRequest<JsonMap>(`/admin/login-logs?${params.toString()}`);
}

export async function getCDKs(options: { search?: string; status?: string; planType?: string; page?: number; pageSize?: number; selectUnused?: boolean } = {}) {
	const params = new URLSearchParams();
	if (options.search) params.set("search", options.search);
	if (options.status && options.status !== "all") params.set("status", options.status);
	if (options.planType && options.planType !== "all") params.set("plan_type", options.planType);
	if (options.selectUnused) params.set("select_unused", "1");
	params.set("page", String(options.page || 1));
	params.set("page_size", String(options.pageSize || 12));
	const body = await legacyRequest<unknown>(`/admin/cdks?${params.toString()}`, {}, true);
	return Array.isArray(body) ? { cdks: body, total: body.length, page: 1, pageSize: body.length, totalPages: 1 } : (body as JsonMap);
}
export function generateCDKs(body: JsonMap) {
  return legacyRequest<JsonMap>(
    "/admin/cdks/generate",
    { method: "POST", body: JSON.stringify(body) },
    true,
  );
}
export function importCDKs(body: JsonMap) {
  return legacyRequest<JsonMap>(
    "/admin/cdks/import",
    { method: "POST", body: JSON.stringify(body) },
    true,
  );
}
export function shipCDKs(codes: string[]) {
	return legacyRequest<JsonMap>(
		"/admin/cdks/batch/ship",
		{ method: "POST", body: JSON.stringify({ codes }) },
		true,
	);
}
export function deleteCDKs(codes: string[]) {
	return legacyRequest<JsonMap>(
		"/admin/cdks/batch/delete",
		{ method: "POST", body: JSON.stringify({ codes }) },
		true,
	);
}
export function shipCDK(code: string) {
  return legacyRequest<JsonMap>(
    `/admin/cdks/${encodeURIComponent(code)}/ship`,
    { method: "POST" },
    true,
  );
}
export function deleteCDK(code: string) {
  return legacyRequest<JsonMap>(
    `/admin/cdks/${encodeURIComponent(code)}`,
    { method: "DELETE" },
    true,
  );
}

export function getCards() {
  return legacyRequest<JsonMap>("/admin/cards", {}, true);
}
export function getCardPools() {
  return legacyRequest<JsonMap>("/admin/card-pools", {}, true);
}
export function createProviderCard(body: JsonMap) {
  return legacyRequest<JsonMap>(
    "/admin/cards/create",
    { method: "POST", body: JSON.stringify(body) },
    true,
  );
}
export function importCardPool(body: JsonMap) {
  return legacyRequest<JsonMap>(
    "/admin/cards/import",
    { method: "POST", body: JSON.stringify(body) },
    true,
  );
}
export function deleteCard(id: string) {
  return legacyRequest<JsonMap>(
    `/admin/cards/${encodeURIComponent(id)}`,
    { method: "DELETE" },
    true,
  );
}

export function getPoolEmails() {
  return legacyRequest<JsonMap>("/admin/pool-emails");
}
export function importPoolEmails(text: string) {
  return legacyRequest<JsonMap>("/admin/pool-emails/import", {
    method: "POST",
    body: JSON.stringify({ text }),
  });
}
export function previewPoolEmail(id: string, limit = 50) {
  return legacyRequest<JsonMap>(
    `/admin/pool-emails/${encodeURIComponent(id)}/messages?limit=${limit}`,
  );
}
export function deletePoolEmail(id: string) {
  return legacyRequest<JsonMap>(
    `/admin/pool-emails/${encodeURIComponent(id)}`,
    { method: "DELETE" },
  );
}

export function getPhones() {
  return legacyRequest<JsonMap>("/admin/phones");
}
export function importPhones(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/phones/import", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function updatePhone(id: string, body: JsonMap) {
  return legacyRequest<JsonMap>(`/admin/phones/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}
export function deletePhone(id: string) {
  return legacyRequest<JsonMap>(`/admin/phones/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function getProducts() {
  return legacyRequest<JsonMap>("/admin/products");
}
export function importProducts(text: string) {
	return legacyRequest<JsonMap>("/admin/products/import", {
		method: "POST",
		body: JSON.stringify({ text }),
	});
}
export function updateProductStatus(id: string, status = "") {
  return legacyRequest<JsonMap>(
    `/admin/products/${encodeURIComponent(id)}/status`,
    { method: "PUT", body: JSON.stringify(status ? { status } : {}) },
  );
}
export function deleteProduct(id: string) {
  return legacyRequest<JsonMap>(`/admin/products/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function downloadProduct(id: string) {
  return legacyDownload(`/admin/products/${encodeURIComponent(id)}/export`);
}
export function downloadProducts(ids: string[]) {
  return legacyDownload("/admin/products/export", {
    method: "POST",
    body: JSON.stringify({ ids }),
  });
}
export function generateProducts(count: number) {
  return legacyRequest<JsonMap>("/admin/products/generate", {
    method: "POST",
    body: JSON.stringify({ count }),
  });
}
export function resumeProducts() {
  return legacyRequest<JsonMap>("/admin/products/resume", { method: "POST" });
}
export function stopProducts(jobKey = "") {
  return legacyRequest<JsonMap>("/admin/products/generate-stop", {
    method: "POST",
    body: JSON.stringify(jobKey ? { jobKey } : {}),
  });
}
export function getProductGeneration(jobKey: string) {
  return legacyRequest<JsonMap>(
    `/admin/products/generation/${encodeURIComponent(jobKey)}`,
  );
}
export function redeemProduct(code: string) {
  return legacyRequest<JsonMap>("/redeem-product", {
    method: "POST",
    body: JSON.stringify({ cdk: code }),
  });
}

export function getProxies() {
  return legacyRequest<JsonMap>("/admin/proxies");
}
export function addProxies(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/proxies", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function updateProxy(id: string, body: JsonMap) {
  return legacyRequest<JsonMap>(`/admin/proxies/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}
export function testProxy(id: string) {
  return legacyRequest<JsonMap>(
    `/admin/proxies/${encodeURIComponent(id)}/test`,
    { method: "POST" },
  );
}
export function toggleProxy(id: string) {
  return legacyRequest<JsonMap>(
    `/admin/proxies/${encodeURIComponent(id)}/toggle`,
    { method: "POST" },
  );
}
export function testAllProxies() {
  return legacyRequest<JsonMap>("/admin/proxies/test-all", { method: "POST" });
}
export function testProxyValue(proxy: string) {
  return legacyRequest<JsonMap>("/admin/proxy/test", {
    method: "POST",
    body: JSON.stringify({ proxy }),
  });
}
export function deleteProxy(id: string) {
  return legacyRequest<JsonMap>(`/admin/proxies/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function getAddresses(region = "US") {
  return legacyRequest<JsonMap>(
    `/admin/addresses?region=${encodeURIComponent(region)}`,
  );
}
export function createAddress(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/addresses", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function generateAddresses(count: number) {
  return legacyRequest<JsonMap>("/admin/addresses/generate-random-us", {
    method: "POST",
    body: JSON.stringify({ count }),
  });
}
export function clearAddresses(region = "US") {
  return legacyRequest<JsonMap>(
    `/admin/addresses/unbound?region=${encodeURIComponent(region)}`,
    { method: "DELETE" },
  );
}
export function updateAddress(id: string, body: JsonMap) {
  return legacyRequest<JsonMap>(`/admin/addresses/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}
export function deleteAddress(id: string) {
  return legacyRequest<JsonMap>(`/admin/addresses/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function getSessions(limit = 200) {
  const body = await legacyRequest<unknown>(`/admin/sessions?limit=${limit}`, {}, true);
  return Array.isArray(body) ? { sessions: body } : (body as JsonMap);
}
export function getSession(jobKey: string) {
  return legacyRequest<JsonMap>(
    `/admin/sessions/${encodeURIComponent(jobKey)}`,
    {},
    true,
  );
}
export function downloadSession(jobKey: string) {
  return legacyDownload(
    `/admin/sessions/${encodeURIComponent(jobKey)}/export`,
    {},
    true,
  );
}
export function changeSessionRenewal(jobKey: string, action: "cancel" | "enable") {
  return legacyRequest<JsonMap>(
    `/admin/sessions/${encodeURIComponent(jobKey)}/renewal/${action}`,
    { method: "POST" },
    true,
  );
}
export function downloadAdminMedia(kind: "screenshots" | "video", path: string) {
  return legacyDownload(
    `/admin/${kind}?path=${encodeURIComponent(path)}`,
  );
}
export function triggerActivation(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/trigger-activation", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function checkSubscription(body: JsonMap) {
  return legacyRequest<JsonMap>("/public/subscription/check", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function cancelRenewal(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/subscription/cancel-auto-renew", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function enableRenewal(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/subscription/enable-auto-renew", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function batchRenewalStatus(
  jobKeys: string[],
) {
  return legacyRequest<JsonMap>("/admin/subscription/batch-renewal-status", {
    method: "POST",
    body: JSON.stringify({
      job_keys: jobKeys,
    }),
  });
}

export function getBilling(query = "") {
  return legacyRequest<JsonMap>(`/admin/billing${query ? `?${query}` : ""}`);
}
export function downloadBilling(query = "") {
  return legacyDownload(`/admin/billing/export${query ? `?${query}` : ""}`);
}
export function deleteBilling(id: string) {
  return legacyRequest<JsonMap>(`/admin/billing/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function deleteFailedBilling() {
  return legacyRequest<JsonMap>("/admin/billing/failed", { method: "DELETE" });
}
export function getBillingSummary(last4: string) {
  return legacyRequest<JsonMap>(
    `/admin/billing/summary/${encodeURIComponent(last4)}`,
  );
}

export function getConfig() {
  return legacyRequest<JsonMap>("/admin/config");
}
export function saveConfig(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/config", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function sendEmailTest(to: string) {
  return legacyRequest<JsonMap>("/admin/email/test", {
    method: "POST",
    body: JSON.stringify({ to }),
  });
}
export function getRegion() {
  return legacyRequest<JsonMap>("/admin/region");
}
export function saveRegion(region: string) {
  return legacyRequest<JsonMap>("/admin/region", {
    method: "PUT",
    body: JSON.stringify({ region }),
  });
}
export function getBrowserPool() {
  return legacyRequest<JsonMap>("/admin/browser-pool");
}
export function setBrowserPoolMode(enabled: boolean) {
  return legacyRequest<JsonMap>("/admin/browser-pool/mode", {
    method: "POST",
    body: JSON.stringify({ enabled }),
  });
}
export function reloadBrowserPool(size?: number) {
  return legacyRequest<JsonMap>("/admin/browser-pool/reload", {
    method: "POST",
    body: JSON.stringify(size ? { size } : {}),
  });
}

export function getGPTConfig() {
  return legacyRequest<JsonMap>("/admin/gpt-api");
}
export function saveGPTConfig(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/gpt-api", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function testGPTConfig(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/gpt-api/test", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function getGPTStatus() {
  return legacyRequest<JsonMap>("/admin/gpt-api/status");
}
export function getHcaptchaConfig() {
  return legacyRequest<JsonMap>("/admin/hcaptcha");
}
export function saveHcaptchaConfig(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/hcaptcha", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function testHcaptcha(kind = "test", body: JsonMap = {}) {
  return legacyRequest<JsonMap>(`/admin/hcaptcha/${kind}`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function getHcaptchaLogs(file = "") {
  const query = file ? `?file=${encodeURIComponent(file)}` : "";
  return legacyRequest<JsonMap>(`/admin/hcaptcha/logs${query}`);
}
export function getTelegramConfig() {
  return legacyRequest<JsonMap>("/admin/telegram");
}
export function saveTelegramConfig(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/telegram", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function testTelegram() {
  return legacyRequest<JsonMap>("/admin/telegram/test", { method: "POST" });
}

export function getCheckoutPlans() {
  return legacyRequest<JsonMap>("/admin/checkout/plans");
}
export function generateCheckout(body: JsonMap) {
  return legacyRequest<JsonMap>("/admin/checkout/generate", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function getCheckoutStatus(jobKey: string) {
  return legacyRequest<JsonMap>(
    `/admin/checkout/status/${encodeURIComponent(jobKey)}`,
  );
}
