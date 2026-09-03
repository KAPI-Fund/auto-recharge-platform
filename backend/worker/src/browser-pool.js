'use strict';

import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { fileURLToPath } = require('node:url');
const { chromium } = require('playwright-extra');
const StealthPlugin = require('puppeteer-extra-plugin-stealth');

chromium.use(StealthPlugin());

const DEFAULT_POOL_SIZE = 2;
const BASE_PORT = Number(process.env.BROWSER_POOL_BASE_PORT || 19222);
const ACQUIRE_TIMEOUT_MS = Number(process.env.BROWSER_POOL_ACQUIRE_TIMEOUT_MS || 180000);
const MAX_POOL_SIZE = Math.min(48, Math.max(1, Number(process.env.BROWSER_POOL_MAX_SIZE || 24)));
function resolveRuntimeDir() {
  const configured = String(process.env.RUNTIME_DIR || '').trim();
  if (!configured || configured === './runtime' || configured === 'runtime') {
    const moduleDir = path.dirname(fileURLToPath(import.meta.url));
    const candidates = [path.resolve(moduleDir, '../../..'), path.resolve(moduleDir, '../..')];
    const projectRoot = candidates.find((candidate) =>
      fs.existsSync(path.join(candidate, 'docker-compose.yml'))
      || fs.existsSync(path.join(candidate, 'package.json')) && fs.existsSync(path.join(candidate, 'src'))
    ) || candidates[0];
    return path.join(projectRoot, 'runtime');
  }
  return path.isAbsolute(configured) ? path.resolve(configured) : path.resolve(process.cwd(), configured);
}

const PROFILE_ROOT = process.env.BROWSER_POOL_PROFILE_DIR
  || path.join(resolveRuntimeDir(), 'browser-pool');

let runtimePoolSizeOverride = null;
let runtimeEnabledOverride = null;
let slots = [];
let initialized = false;
let initPromise = null;
let waitQueue = [];

function isEnabled() {
  if (runtimeEnabledOverride !== null) return runtimeEnabledOverride;
  return String(process.env.BROWSER_POOL || '1') !== '0';
}

function resolvePoolSize() {
  const configured = runtimePoolSizeOverride ?? Number(process.env.BROWSER_POOL_SIZE || 0);
  const concurrent = Number(process.env.MAX_CONCURRENT_ACTIVATIONS || 0);
  const value = configured > 0 ? configured : concurrent > 0 ? concurrent : DEFAULT_POOL_SIZE;
  return Math.min(MAX_POOL_SIZE, Math.max(1, value));
}

function profileDir(slotId) {
  return path.join(PROFILE_ROOT, `slot-${slotId}`);
}

function formatBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

function directorySize(target) {
  try {
    const stat = fs.statSync(target);
    if (!stat.isDirectory()) return stat.size;
    return fs.readdirSync(target, { withFileTypes: true })
      .reduce((sum, item) => sum + directorySize(path.join(target, item.name)), 0);
  } catch (_) {
    return 0;
  }
}

function launchOptions(port) {
  const headful = process.env.HEADFUL === '1';
  const options = {
    headless: !headful,
    args: [
      '--disable-blink-features=AutomationControlled',
      `--remote-debugging-port=${port}`,
      ...(headful ? [] : ['--no-sandbox', '--disable-setuid-sandbox'])
    ],
    viewport: { width: 1920, height: 1080 },
    locale: 'en-US',
    timezoneId: 'America/Chicago',
    ignoreHTTPSErrors: true
  };
  const channel = String(process.env.CHROMIUM_CHANNEL || '').trim();
  if (channel) options.channel = channel;
  return options;
}

async function warmSlot(slotId) {
  const port = BASE_PORT + slotId;
  const dir = profileDir(slotId);
  fs.mkdirSync(dir, { recursive: true });
  const persistentContext = await chromium.launchPersistentContext(dir, launchOptions(port));
  return {
    slotId,
    port,
    cdpUrl: `http://127.0.0.1:${port}`,
    persistentContext,
    inUse: false,
    jobKey: null,
    uses: 0,
    createdAt: Date.now()
  };
}

async function initBrowserPool() {
  if (!isEnabled()) {
    initialized = false;
    return { enabled: false, initialized: false, size: 0 };
  }
  if (initialized) return { enabled: true, initialized: true, size: slots.length };
  if (initPromise) return initPromise;

  initPromise = (async () => {
    const size = resolvePoolSize();
    const created = [];
    console.log(`[BrowserPool] 正在预热 ${size} 个浏览器槽位 (CDP ${BASE_PORT}..${BASE_PORT + size - 1})`);
    for (let index = 0; index < size; index += 1) {
      try {
        const slot = await warmSlot(index);
        created.push(slot);
        console.log(`[BrowserPool] slot-${index} 就绪 ${slot.cdpUrl}`);
      } catch (error) {
        console.error(`[BrowserPool] slot-${index} 启动失败: ${error.message}`);
      }
    }
    slots = created;
    initialized = created.length > 0;
    if (!initialized) console.warn('[BrowserPool] 无可用槽位，任务回退为独立浏览器');
    return { enabled: isEnabled(), initialized, size: created.length };
  })();

  try {
    return await initPromise;
  } finally {
    initPromise = null;
  }
}

function getStats() {
  return {
    enabled: isEnabled(),
    initialized,
    configuredSize: resolvePoolSize(),
    maxPoolSize: MAX_POOL_SIZE,
    basePort: BASE_PORT,
    acquireTimeoutMs: ACQUIRE_TIMEOUT_MS,
    profileRoot: PROFILE_ROOT,
    size: slots.length,
    idle: slots.filter((slot) => !slot.inUse).length,
    busy: slots.filter((slot) => slot.inUse).length,
    waiting: waitQueue.length,
    totalUses: slots.reduce((sum, slot) => sum + slot.uses, 0),
    slots: slots.map((slot) => ({
      slotId: slot.slotId,
      port: slot.port,
      cdpUrl: slot.cdpUrl,
      inUse: slot.inUse,
      jobKey: slot.jobKey,
      uses: slot.uses,
      profileDir: profileDir(slot.slotId),
      profileSizeBytes: directorySize(profileDir(slot.slotId)),
      profileSizeText: formatBytes(directorySize(profileDir(slot.slotId))),
      pageCount: slot.persistentContext.pages().length,
      openUrls: slot.persistentContext.pages().slice(0, 8).map((page) => page.url())
    }))
  };
}

function getDetailedStats() {
  const stats = getStats();
  const memory = process.memoryUsage();
  const hostTotalGb = os.totalmem() / 1024 ** 3;
  const hostFreeGb = os.freemem() / 1024 ** 3;
  return {
    ...stats,
    queue: waitQueue.map((entry) => ({ jobKey: entry.jobKey })),
    memory: {
      hostTotalGb: Number(hostTotalGb.toFixed(2)),
      hostFreeGb: Number(hostFreeGb.toFixed(2)),
      processRssMb: Number((memory.rss / 1024 ** 2).toFixed(1)),
      sizingHint: `按当前可用约 ${hostFreeGb.toFixed(1)}GB，建议池大小不超过 ${Math.max(1, Math.floor(hostFreeGb / 0.55))}`
    },
    runtimeOverride: runtimePoolSizeOverride
  };
}

function acquireSlot(jobKey) {
  return new Promise((resolve, reject) => {
    if (!initialized || slots.length === 0) {
      resolve(null);
      return;
    }
    const free = slots.find((slot) => !slot.inUse);
    if (free) {
      free.inUse = true;
      free.jobKey = String(jobKey || '');
      free.uses += 1;
      resolve({ slotId: free.slotId, port: free.port, cdpUrl: free.cdpUrl });
      return;
    }
    const entry = {
      jobKey: String(jobKey || ''),
      resolve,
      reject,
      timer: setTimeout(() => {
        const index = waitQueue.indexOf(entry);
        if (index >= 0) waitQueue.splice(index, 1);
        reject(new Error(`浏览器池繁忙，${Math.round(ACQUIRE_TIMEOUT_MS / 1000)}s 内无空闲槽位`));
      }, ACQUIRE_TIMEOUT_MS)
    };
    waitQueue.push(entry);
  });
}

function releaseSlot(slotId) {
  const slot = slots.find((item) => item.slotId === Number(slotId));
  if (!slot) return;
  slot.inUse = false;
  slot.jobKey = null;
  const next = waitQueue.shift();
  if (!next) return;
  clearTimeout(next.timer);
  slot.inUse = true;
  slot.jobKey = next.jobKey;
  slot.uses += 1;
  next.resolve({ slotId: slot.slotId, port: slot.port, cdpUrl: slot.cdpUrl });
}

async function withBrowserSlot(jobKey, callback) {
  if (!isEnabled() || !initialized || !slots.length) return callback(null);
  const slot = await acquireSlot(jobKey);
  if (!slot) return callback(null);
  try {
    return await callback(slot);
  } finally {
    releaseSlot(slot.slotId);
  }
}

function buildPoolEnv(slot) {
  if (!slot) {
    return {
      BROWSER_RUNTIME_MODE: 'standalone',
      BROWSER_POOL: '0',
      BROWSER_POOL_SLOT: '',
      BROWSER_POOL_CDP_URL: ''
    };
  }
  return {
    BROWSER_RUNTIME_MODE: 'pool',
    BROWSER_POOL: '1',
    BROWSER_POOL_SLOT: String(slot.slotId),
    BROWSER_POOL_CDP_URL: slot.cdpUrl,
    CDP_URL: slot.cdpUrl,
    CDP_PORT: String(slot.port)
  };
}

function setRuntimePoolSize(size) {
  runtimePoolSizeOverride = Math.min(MAX_POOL_SIZE, Math.max(1, Number(size) || 1));
  return runtimePoolSizeOverride;
}

function setRuntimeEnabled(enabled) {
  runtimeEnabledOverride = Boolean(enabled);
  return runtimeEnabledOverride;
}

async function shutdownBrowserPool() {
  for (const entry of waitQueue) {
    clearTimeout(entry.timer);
    entry.reject(new Error('浏览器池正在关闭'));
  }
  waitQueue = [];
  for (const slot of slots) await slot.persistentContext.close().catch(() => {});
  slots = [];
  initialized = false;
}

async function reloadBrowserPool(size = null) {
  if (slots.some((slot) => slot.inUse)) throw new Error('当前有任务使用浏览器槽位，请待任务结束后再重载');
  if (size != null) setRuntimePoolSize(size);
  await shutdownBrowserPool();
  return initBrowserPool();
}

export {
  initBrowserPool,
  shutdownBrowserPool,
  reloadBrowserPool,
  setRuntimePoolSize,
  setRuntimeEnabled,
  getStats,
  getDetailedStats,
  withBrowserSlot,
  buildPoolEnv,
  acquireSlot,
  releaseSlot,
  isEnabled,
  formatBytes,
  MAX_POOL_SIZE
};
