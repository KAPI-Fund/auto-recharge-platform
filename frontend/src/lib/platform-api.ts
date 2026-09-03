import type { CDK, Card, Overview, Plan, PublicTask, StoreOrder, StoreProduct, Task } from "@/lib/platform-types";

const prefix = "/platform-api";
const requestTimeoutMs = 10_000;

function requestTraceId() {
  if (typeof window === "undefined") return "";
  if (typeof window.crypto?.randomUUID === "function") return `web-${window.crypto.randomUUID()}`;
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function traceError(message: string, traceId: string) {
  const error = new Error(message);
  if (traceId) {
    Object.defineProperty(error, "traceId", { value: traceId, enumerable: false });
  }
  return error;
}

function adminHeaders() {
  const token = typeof window === "undefined"
    ? ""
    : window.localStorage.getItem("kc_admin_token") || "";
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function request<T>(path: string, options: RequestInit = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("Content-Type", "application/json");
  Object.entries(adminHeaders()).forEach(([key, value]) => headers.set(key, value));
  const traceId = headers.get("X-Trace-ID") || requestTraceId();
  if (traceId) headers.set("X-Trace-ID", traceId);
  const controller = new AbortController();
  const timeout = typeof window === "undefined"
    ? setTimeout(() => controller.abort(), requestTimeoutMs)
    : window.setTimeout(() => controller.abort(), requestTimeoutMs);
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
    clearTimeout(timeout);
  }
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const responseTraceId = response.headers.get("X-Trace-ID") || body?.traceId || body?.trace_id || traceId;
    throw traceError(body?.message || body?.error || `请求失败 (${response.status})`, responseTraceId);
  }
  return body as T;
}

export function getAdminToken() {
  return typeof window === "undefined"
    ? ""
    : window.localStorage.getItem("kc_admin_token") || "";
}

export function setAdminToken(value: string) {
  if (typeof window !== "undefined") window.localStorage.setItem("kc_admin_token", value);
}

export function clearAdminToken() {
  if (typeof window !== "undefined") {
    window.localStorage.removeItem("kc_admin_token");
  }
}

export async function listPlans() {
  return (await request<{ plans: Plan[] }>("/plans")).plans;
}

export async function createStoreOrder(input: { planCode: string; email: string; phoneCountryCode: string; phoneNumber: string }) {
  return request<{ order: StoreOrder; checkoutUrl?: string; debug: boolean; message?: string }>("/store/orders", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function getStoreOrder(id: string) {
  return (await request<{ order: StoreOrder }>(`/store/orders/${encodeURIComponent(id)}`)).order;
}

export async function queryStoreOrders(input: { query?: string; orderNo?: string; email?: string; phoneCountryCode?: string; phoneNumber?: string }) {
  return (await request<{ orders: StoreOrder[]; retention: string }>("/store/orders/query", {
    method: "POST",
    body: JSON.stringify(input),
  })).orders;
}

export async function verifyCDK(code: string) {
  return request<{ valid: boolean; status: string; cdk?: { code?: string; plan: Plan }; task?: PublicTask }>("/recharge/verify", {
    method: "POST",
    body: JSON.stringify({ code }),
  });
}

export async function createRechargeTask(input: { code: string; session: string; mode: string }) {
  return request<{ task: PublicTask }>("/recharge/tasks", { method: "POST", body: JSON.stringify(input) });
}

export async function getRechargeTask(id: string) {
  return (await request<{ task: PublicTask }>(`/recharge/tasks/${encodeURIComponent(id)}`)).task;
}

export async function getOverview() {
  return (await request<{ overview: Overview }>("/admin/overview")).overview;
}

export async function getAdminTasks() {
  return (await request<{ tasks: Task[] }>("/admin/tasks?limit=100")).tasks;
}

export async function getAdminCDKs() {
  return (await request<{ cdks: CDK[] }>("/admin/cdks?limit=200")).cdks;
}

export async function generateCDKs(input: { planCode: string; quantity: number; prefix: string }) {
  return (await request<{ cdks: CDK[] }>("/admin/cdks", { method: "POST", body: JSON.stringify(input) })).cdks;
}

export async function getAdminCards() {
  return (await request<{ cards: Card[] }>("/admin/cards?limit=200")).cards;
}

export async function importCards(cards: Array<{ number: string; expiry: string; cvc: string; holder: string }>) {
  return request<{ created: number }>("/admin/cards/import", { method: "POST", body: JSON.stringify({ cards }) });
}

export async function getAdminConfig() {
  return (await request<{ config: Record<string, string> }>("/admin/config")).config;
}

export async function saveAdminConfig(config: Record<string, string>) {
  return request<{ ok: boolean }>("/admin/config", { method: "PUT", body: JSON.stringify(config) });
}

export type StoreProductOption = { value: string; code?: string; label: string; countryLabel?: string; currency?: string };
export type StoreProductOptions = {
  currency: string;
  providerPlans: StoreProductOption[];
  countries: StoreProductOption[];
  defaults: Record<string, unknown>;
};

export async function getStoreProducts() {
  return request<{ products: StoreProduct[]; options: StoreProductOptions }>("/admin/store/products");
}

export async function saveStoreProduct(input: { code: string; name: string; description: string; providerPlanName: string; country: string; currency: string; price: number; saleLimit: number; sortOrder: number; published: boolean }) {
  return (await request<{ product: StoreProduct }>("/admin/store/products", { method: "POST", body: JSON.stringify(input) })).product;
}

export async function setStoreProductPublished(code: string, published: boolean) {
  return (await request<{ product: StoreProduct }>(`/admin/store/products/${encodeURIComponent(code)}`, { method: "PATCH", body: JSON.stringify({ published }) })).product;
}

export async function toggleStoreProductPublished(code: string) {
  return request<{ product: StoreProduct; message?: string }>(`/admin/store/products/${encodeURIComponent(code)}/toggle`, { method: "POST" });
}
