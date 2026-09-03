'use strict';

const BASE_URL = String(process.env.API_BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const WORKER_TOKEN = String(process.env.WORKER_API_TOKEN || '').trim();
const TASK_ID = String(process.env.TASK_ID || '').trim();
const WORKER_ID = String(process.env.WORKER_ID || '').trim();
const WORKER_LEASE_TOKEN = String(process.env.WORKER_LEASE_TOKEN || '').trim();

function push(entry = {}) {
  const text = String(entry.text || '').trim();
  if (!text) return;
  const traceId = String(entry.traceId || process.env.TRACE_ID || '').trim();
  void fetch(`${BASE_URL}/api/v1/internal/runtime-logs`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Worker-Token': WORKER_TOKEN,
      'X-Worker-ID': WORKER_ID,
      'X-Worker-Lease-Token': WORKER_LEASE_TOKEN,
      'X-Trace-ID': traceId
    },
    body: JSON.stringify({
      taskId: String(entry.taskId || TASK_ID || '').trim(),
      jobKey: String(entry.jobKey || process.env.JOB_KEY || '').trim(),
      traceId,
      level: String(entry.level || 'stdout'),
      source: String(entry.source || 'legacy/product'),
      text,
      workerId: String(entry.workerId || WORKER_ID || '').trim(),
      leaseToken: String(entry.leaseToken || WORKER_LEASE_TOKEN || '').trim()
    })
  }).catch(() => undefined);
}

module.exports = { push };
