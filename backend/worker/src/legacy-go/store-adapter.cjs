'use strict';

const crypto = require('node:crypto');

const DEFAULT_BASE_URL = 'http://127.0.0.1:8080';
const poolEmailAliases = new Map();
const cardAllocationIds = new Map();

function baseUrl() {
  return String(process.env.API_BASE_URL || DEFAULT_BASE_URL).replace(/\/$/, '');
}

function workerToken() {
  return String(process.env.WORKER_API_TOKEN || '').trim();
}

function traceId() {
  return String(process.env.TRACE_ID || '').trim();
}

function cardReservationContext(ownerKey, extra = {}) {
  const body = { ownerKey };
  const taskId = String(process.env.TASK_ID || '').trim();
  if (!taskId) return { ...body, ...extra };
  const values = {
    taskId,
    jobKey: String(process.env.JOB_KEY || '').trim(),
    poolId: String(process.env.POOL_ID || '').trim(),
    businessAccountId: String(process.env.BUSINESS_ACCOUNT_ID || '').trim(),
    usageType: String(process.env.USAGE_TYPE || '').trim(),
    workerId: String(process.env.WORKER_ID || '').trim(),
    leaseToken: String(process.env.WORKER_LEASE_TOKEN || '').trim(),
    // ownerKey includes the card-attempt number. Duplicate Workers therefore
    // converge on the same allocation, while a declined attempt can rotate.
    idempotencyKey: String(process.env.CARD_ALLOCATION_IDEMPOTENCY_KEY || ownerKey).trim(),
  };
  return Object.fromEntries(Object.entries({ ...body, ...values, ...extra }).filter(([, value]) => value !== ''));
}

function cardAllocationId(cardId, allocationId = '') {
  const explicit = String(allocationId || '').trim();
  if (explicit) return explicit;
  return cardAllocationIds.get(String(cardId || '').trim()) || '';
}

function poolEmailAlias(id) {
  const raw = String(id || '').trim();
  if (!raw || /^\d+$/.test(raw)) return raw;
  for (let offset = 0; offset < 10000; offset += 1) {
    const digest = crypto.createHash('sha256').update(`${raw}:${offset}`).digest();
    const alias = String((digest.readUInt32BE(0) % 2000000000) + 1);
    const existing = poolEmailAliases.get(alias);
    if (!existing || existing === raw) {
      poolEmailAliases.set(alias, raw);
      return alias;
    }
  }
  throw new Error('邮箱池 ID 别名耗尽');
}

function resolvePoolEmailId(id) {
  const value = String(id || '').trim();
  const mapped = poolEmailAliases.get(value);
  if (mapped) return mapped;
  if (/^\d+$/.test(value) && String(process.env.POOL_EMAIL_ID || '').trim() === value) {
    const raw = String(process.env.POOL_EMAIL_ID_RAW || '').trim();
    if (raw) return raw;
  }
  return value;
}

async function call(action, body = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), Number(process.env.WORKER_API_TIMEOUT_MS || 30000));
  try {
    const response = await fetch(`${baseUrl()}/api/v1/internal/store/${encodeURIComponent(action)}`, {
      method: 'POST',
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        'X-Worker-Token': workerToken(),
        'X-Trace-ID': traceId()
      },
      body: JSON.stringify(body)
    });
    const text = await response.text();
    let payload = {};
    try {
      payload = text ? JSON.parse(text) : {};
    } catch (_) {
      payload = {};
    }
    if (!response.ok) {
      const message = payload.message || payload.error || `Go store request failed (${response.status})`;
      const error = new Error(String(message));
      error.status = response.status;
      error.traceId = response.headers.get('x-trace-id') || payload.traceId || payload.trace_id || traceId();
      throw error;
    }
    return payload;
  } finally {
    clearTimeout(timeout);
  }
}

const PLAN_NAME_MAP = Object.freeze({
  plus: 'chatgptplusplan',
  pro_5x: 'chatgptprolite',
  pro_20x: 'chatgptpro',
  go: 'chatgptgoplan'
});

function resolvePlanName(planType) {
  return PLAN_NAME_MAP[String(planType || '').trim().toLowerCase()] || PLAN_NAME_MAP.plus;
}

const adapter = {
  PLAN_NAME_MAP,
  resolvePlanName,
  runQuery() {
    throw new Error('原版 Worker 的 SQL 存储接口已由 Go API 接管');
  },
  runExecute() {
    throw new Error('原版 Worker 的 SQL 存储接口已由 Go API 接管');
  },
  async ensureReady() {
    return true;
  },
  async getPaymentRegion() {
    return (await call('getPaymentRegion')).region || 'PH';
  },
  async verifyCdkDetails(code) {
    return (await call('verifyCdkDetails', { code })).cdk || null;
  },
  async getAppConfigValue(key, fallback = '') {
    const result = await call('getAppConfigValue', { key, fallback });
    return result.value ?? fallback;
  },
  async setAppConfigValue(key, value) {
    return call('setAppConfigValue', { key, value });
  },
  async getActiveProxy() {
    return (await call('getActiveProxy')).proxy || '';
  },
  async reserveCard(ownerKey) {
    const result = await call('reserveCard', cardReservationContext(String(ownerKey || '').trim()));
    const card = result.card || null;
    if (!card) return null;
    const allocationId = String(result.allocationId || result.allocation_id || card.allocationId || card.allocation_id || '').trim();
    const cardId = String(card.id || '').trim();
    if (cardId && allocationId) cardAllocationIds.set(cardId, allocationId);
    // The legacy payment code reads result.card directly. Normalize both
    // camelCase and snake_case bridge fields here so provider/pool attribution
    // survives the Go API -> Worker boundary and reaches billing records.
    const provider = String(result.provider || result.providerName || card.provider || '').trim();
    const providerCardId = String(
      result.providerCardId || result.provider_card_id || card.providerCardId || card.provider_card_id || ''
    ).trim();
    const poolId = String(result.poolId || result.pool_id || card.poolId || card.pool_id || '').trim();
    return {
      ...card,
      ...(provider ? { provider } : {}),
      ...(providerCardId ? { providerCardId, provider_card_id: providerCardId } : {}),
      ...(poolId ? { poolId, pool_id: poolId } : {}),
      ...(allocationId ? { allocationId, allocation_id: allocationId } : {}),
    };
  },
  async releaseCard(cardId, allocationId = '') {
    const id = String(cardId || '').trim();
    const resolvedAllocationId = cardAllocationId(id, allocationId);
    const extra = resolvedAllocationId ? { allocationId: resolvedAllocationId } : {};
    const result = await call('releaseCard', cardReservationContext('', { cardId: id, ...extra }));
    if (id) cardAllocationIds.delete(id);
    return result;
  },
  async markCardExhausted(cardId, allocationId = '', failureCode = '', failureMessage = '') {
    const id = String(cardId || '').trim();
    const resolvedAllocationId = cardAllocationId(id, allocationId);
    const result = await call('markCardExhausted', cardReservationContext('', {
      cardId: id,
      ...(resolvedAllocationId ? { allocationId: resolvedAllocationId } : {}),
      ...(String(failureCode || '').trim() ? { failureCode: String(failureCode).trim() } : {}),
      ...(String(failureMessage || '').trim() ? { failureMessage: String(failureMessage).trim() } : {}),
    }));
    if (id) cardAllocationIds.delete(id);
    return result;
  },
  async recordCardFailure(cardId, allocationId = '', failureCode = '', failureMessage = '') {
    const id = String(cardId || '').trim();
    const resolvedAllocationId = cardAllocationId(id, allocationId);
    return call('recordCardFailure', cardReservationContext('', {
      cardId: id,
      ...(resolvedAllocationId ? { allocationId: resolvedAllocationId } : {}),
      ...(String(failureCode || '').trim() ? { failureCode: String(failureCode).trim() } : {}),
      ...(String(failureMessage || '').trim() ? { failureMessage: String(failureMessage).trim() } : {}),
    }));
  },
  async recordCardUsage(cardId, allocationId = '') {
    const id = String(cardId || '').trim();
    const resolvedAllocationId = cardAllocationId(id, allocationId);
    return call('recordCardUsage', cardReservationContext('', {
      cardId: id,
      ...(resolvedAllocationId ? { allocationId: resolvedAllocationId } : {}),
    }));
  },
  async settleCard(cardId, allocationId = '', failureCode = '', failureMessage = '') {
    const id = String(cardId || '').trim();
    const resolvedAllocationId = cardAllocationId(id, allocationId);
    const result = await call('settleCard', cardReservationContext('', {
      cardId: id,
      ...(resolvedAllocationId ? { allocationId: resolvedAllocationId } : {}),
      ...(String(failureCode || '').trim() ? { failureCode: String(failureCode).trim() } : {}),
      ...(String(failureMessage || '').trim() ? { failureMessage: String(failureMessage).trim() } : {}),
    }));
    if (id) cardAllocationIds.delete(id);
    return result;
  },
  async bindCardPaymentProfile(cardId, profile) {
    return call('bindCardPaymentProfile', { cardId, profile });
  },
  async createBillingRecord(data) {
    return call('createBillingRecord', { data: { ...data, trace_id: data?.trace_id || traceId() } });
  },
  async listTaxFreeAddresses(region) {
    return (await call('listTaxFreeAddresses', { region })).addresses || [];
  },
  async createTaxFreeAddress(data) {
    return call('createTaxFreeAddress', data);
  },
  async getTaxFreeAddress(id) {
    return (await call('getTaxFreeAddress', { id })).address || null;
  },
  async updateTaxFreeAddress(id, data) {
    return call('updateTaxFreeAddress', { id, ...data });
  },
  async deleteTaxFreeAddress(id) {
    return call('deleteTaxFreeAddress', { id });
  },
  async bindTaxFreeAddress(id, cardId) {
    return call('bindTaxFreeAddress', { id, cardId });
  },
  async clearUnboundTaxFreeAddresses(region) {
    return call('clearUnboundTaxFreeAddresses', { region });
  },
  async pickableTaxFreeAddresses(region) {
    return (await call('pickableTaxFreeAddresses', { region })).addresses || [];
  },
  async getMaxBackgroundConcurrent() {
    return Number((await call('getMaxBackgroundConcurrent')).value || 1) || 1;
  },
  async getMaintenanceModeState() {
    return call('getMaintenanceModeState');
  },
  async reservePoolEmail(ownerKey) {
    const email = (await call('reservePoolEmail', { ownerKey })).email || null;
    if (!email?.id) return email;
    const rawId = String(email.id).trim();
    const alias = poolEmailAlias(rawId);
    process.env.POOL_EMAIL_ID_RAW = rawId;
    return { ...email, id: alias };
  },
  async releasePoolEmailReservation(id) {
    return call('releasePoolEmailReservation', { id: resolvePoolEmailId(id) });
  },
  async markPoolEmailRegistered(id) {
    return call('markPoolEmailRegistered', { id: resolvePoolEmailId(id) });
  },
  async reserveRuntimeAssets(ownerKey) {
    return call('reserveRuntimeAssets', { ownerKey });
  },
  async releaseRuntimeAssets({ phoneAssetId = '', cardAssetId = '' } = {}) {
    return call('releaseRuntimeAssets', { phoneAssetId, cardAssetId });
  },
  async deletePhoneAsset(phone) {
    return call('deletePhoneAsset', { phone });
  },
  async deleteCardAsset(cardNumber) {
    return call('deleteCardAsset', { cardNumber });
  },
  async incrementAssetSuccessCount({ phone = '', cardNumber = '' } = {}) {
    return call('incrementAssetSuccessCount', { phone, cardNumber });
  },
  async upsertPendingProduct(email, token = '') {
    return call('upsertPendingProduct', { email, token });
  },
  async markProductReadyByEmail(email, filePath = '', imapKey = '') {
    return call('markProductReadyByEmail', { email, filePath, imapKey });
  },
  async addProduct(email, filePath = '', password = '', token = '', imapKey = '') {
    return call('addProduct', { email, filePath, password, token, imapKey });
  }
};

module.exports = adapter;
