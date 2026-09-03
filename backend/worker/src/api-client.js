export class ApiClient {
  constructor({ baseUrl, workerToken, traceId = "" }) {
    this.baseUrl = baseUrl;
    this.workerToken = workerToken;
    this.traceId = traceId;
  }

  async request(path, options = {}) {
    const { traceId: optionTraceId, ...fetchOptions } = options;
    const headers = new Headers(fetchOptions.headers || {});
    headers.set("Content-Type", "application/json");
    headers.set("X-Worker-Token", this.workerToken);
    const traceId = String(optionTraceId || this.traceId || "").trim();
    if (traceId) headers.set("X-Trace-ID", traceId);
    const response = await fetch(`${this.baseUrl}/api/v1${path}`, { ...fetchOptions, headers });
    const text = await response.text();
    let payload = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      payload = null;
    }
    if (!response.ok) {
      const traceId = response.headers.get("x-trace-id") || payload?.traceId || payload?.trace_id || this.traceId;
      const message = payload?.message || `API request failed: ${response.status}`;
      const error = new Error(traceId && !message.includes(traceId) ? `${message} (traceId: ${traceId})` : message);
      error.traceId = traceId;
      error.code = payload?.code || payload?.errorCode || "";
      error.status = response.status;
      throw error;
    }
    return payload;
  }

  setTraceId(traceId) {
    this.traceId = String(traceId || "").trim();
    return this;
  }

  claimTask(taskId, workerId) {
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}/claim`, {
      method: "POST",
      body: JSON.stringify({ workerId }),
    });
  }

  heartbeatTask(taskId, workerId, leaseToken, body = {}) {
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}/heartbeat`, {
      method: "POST",
      body: JSON.stringify({ ...body, workerId, leaseToken }),
    });
  }

  getTaskSecret(taskId, workerId = "", leaseToken = "") {
    const headers = {};
    if (workerId) headers["X-Worker-ID"] = workerId;
    if (leaseToken) headers["X-Worker-Lease-Token"] = leaseToken;
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}/secret`, { headers });
  }

  getTaskRuntime(taskId, workerId = "", leaseToken = "") {
    const headers = {};
    if (workerId) headers["X-Worker-ID"] = workerId;
    if (leaseToken) headers["X-Worker-Lease-Token"] = leaseToken;
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}/runtime`, { headers });
  }

  runUpstreamTask(taskId, workerId, leaseToken) {
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}/upstream`, {
      method: "POST",
      body: JSON.stringify({ workerId, leaseToken }),
    });
  }

  getRuntimeConfig() {
    return this.request("/internal/config");
  }

  getProductGeneration(taskId) {
    return this.request(`/internal/product-generations/${encodeURIComponent(taskId)}`);
  }

  updateProductGeneration(taskId, body) {
    return this.request(`/internal/product-generations/${encodeURIComponent(taskId)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    });
  }

  updateTask(taskId, body) {
    return this.request(`/internal/tasks/${encodeURIComponent(taskId)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    });
  }

  appendRuntimeLog(body) {
    return this.request("/internal/runtime-logs", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  store(action, body = {}) {
    return this.request(`/internal/store/${encodeURIComponent(action)}`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }
}
