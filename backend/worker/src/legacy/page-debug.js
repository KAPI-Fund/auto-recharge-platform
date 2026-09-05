'use strict';

/**
 * 页面侧调试采集（Playwright/CDP）。
 * 不依赖用户打开 F12；OpenAI 禁调试不影响自动化进程监听 console / network。
 * 输出走 stdout，由 Worker 转发到后台运行日志。
 */

const INTERESTING_HOST = /chatgpt\.com|openai\.com|stripe\.com|stripecdn\.com|js\.stripe\.com|hcaptcha\.com|cloudflare/i;
const MAX_LINE = 280;
const attached = new WeakMap();

function normalizeLogLevel(value, fallback = 'info') {
    const raw = String(value || '').trim().toLowerCase();
    if (raw === 'off' || raw === '0' || raw === 'false' || raw === 'no') return 'off';
    if (raw === 'info') return 'info';
    if (raw === 'debug' || raw === '1' || raw === 'true' || raw === 'on') return 'debug';
    return fallback;
}

function configuredLogLevel(env = process.env) {
    const configured = String(env.WORKER_LOG_LEVEL || '').trim();
    if (configured) return normalizeLogLevel(configured);
    const legacy = String(env.BROWSER_DEBUG_LOGS || '').trim();
    if (legacy) return normalizeLogLevel(legacy);
    return 'info';
}

function shouldLogAt(level, env = process.env) {
    const current = configuredLogLevel(env);
    if (current === 'off') return false;
    return level === 'info' || current === 'debug';
}

function debugEnabled() {
    return configuredLogLevel() !== 'off';
}

function clip(value, max = MAX_LINE) {
    const text = redactSensitive(String(value || '').replace(/\s+/g, ' ').trim());
    if (text.length <= max) return text;
    return `${text.slice(0, max)}…`;
}

function redactSensitive(value) {
    return String(value || '')
        .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, '[redacted-email]')
        .replace(/\b(?:\d[ -]*?){13,19}\b/g, '[redacted-number]');
}

function debugLog(kind, message, level = 'info') {
    if (!shouldLogAt(level)) return;
    console.log(`[BrowserDebug][${kind}] ${clip(message, 500)}`);
}

function shouldLogResponse(url, status) {
    if (status >= 400) return true;
    if (!INTERESTING_HOST.test(url || '')) return false;
    return /checkout|payments|stripe|elements|hcaptcha|backend-api/i.test(url || '');
}

function shortUrl(url) {
    try {
        const parsed = new URL(String(url || ''));
        return clip(`${parsed.host}${parsed.pathname || ''}`, 160);
    } catch (_) {
        return clip(url, 160);
    }
}

function createBuffer(limit = 80) {
    const items = [];
    return {
        push(item) {
            items.push(item);
            if (items.length > limit) items.shift();
        },
        list() {
            return items.slice();
        },
        clear() {
            items.length = 0;
        },
        get size() {
            return items.length;
        }
    };
}

/**
 * 在 page 上挂 console / 网络监听（同一 page 可重复调用，返回同一 session）。
 * @returns {{ reset: Function, summarize: Function, detach: Function }}
 */
function attachPageDebugCapture(page, options = {}) {
    if (!page || typeof page.on !== 'function') {
        return { reset() {}, summarize() {}, detach() {} };
    }
    if (!debugEnabled()) {
        return { reset() {}, summarize() {}, detach() {} };
    }

    const existing = attached.get(page);
    if (existing) {
        if (options.label) existing.label = String(options.label);
        return existing;
    }

    const labelRef = { value: String(options.label || 'page').trim() || 'page' };
    const consoleBuf = createBuffer(40);
    const netFailBuf = createBuffer(40);
    const netBuf = createBuffer(60);
    const stats = {
        consoleErrors: 0,
        consoleWarnings: 0,
        requestFailed: 0,
        badResponses: 0,
        stripeOk: 0,
        stripeBad: 0,
        chatgptOk: 0,
        chatgptBad: 0
    };

    const onConsole = (msg) => {
        try {
            const type = String(msg.type?.() || msg.type || 'log').toLowerCase();
            const text = clip(msg.text?.() || '');
            if (!text) return;
            const entry = { type, text, at: Date.now() };
            if (type === 'error') {
                stats.consoleErrors += 1;
                consoleBuf.push(entry);
                debugLog('console', `${labelRef.value} error: ${text}`);
                return;
            }
            if (type === 'warning' || type === 'warn') {
                stats.consoleWarnings += 1;
                consoleBuf.push(entry);
                if (stats.consoleWarnings <= 25) {
                    debugLog('console', `${labelRef.value} warn: ${text}`);
                }
                return;
            }
            if (/stripe|checkout|payment|error|fail|blocked|csp|cors/i.test(text)) {
                consoleBuf.push(entry);
                debugLog('console', `${labelRef.value} ${type}: ${text}`, 'debug');
            }
        } catch (_) { /* ignore */ }
    };

    const onPageError = (error) => {
        stats.consoleErrors += 1;
        const text = clip(error?.message || error);
        consoleBuf.push({ type: 'pageerror', text, at: Date.now() });
        debugLog('pageerror', `${labelRef.value}: ${text}`);
    };

    const classifyHost = (url) => {
        if (/stripe\.com|stripecdn\.com/i.test(url)) return 'stripe';
        if (/chatgpt\.com|openai\.com/i.test(url)) return 'chatgpt';
        return 'other';
    };

    const onRequestFailed = (request) => {
        try {
            stats.requestFailed += 1;
            const url = request.url?.() || '';
            const failure = request.failure?.() || {};
            const errText = clip(failure.errorText || 'unknown', 100);
            const line = `${request.method?.() || 'GET'} ${shortUrl(url)} err=${errText}`;
            netFailBuf.push({ url, errText, at: Date.now() });
            const hostKind = classifyHost(url);
            if (hostKind === 'stripe') stats.stripeBad += 1;
            if (hostKind === 'chatgpt') stats.chatgptBad += 1;
            if (!INTERESTING_HOST.test(url) && stats.requestFailed > 12) return;
            if (stats.requestFailed <= 40 || INTERESTING_HOST.test(url)) {
                debugLog('net-fail', `${labelRef.value} ${line}`);
            }
        } catch (_) { /* ignore */ }
    };

    const onResponse = (response) => {
        try {
            const url = response.url?.() || '';
            const status = Number(response.status?.() || 0);
            const hostKind = classifyHost(url);
            if (status >= 200 && status < 400) {
                if (hostKind === 'stripe' && /stripe|elements|checkout/i.test(url)) stats.stripeOk += 1;
                if (hostKind === 'chatgpt' && /checkout|payments|backend-api/i.test(url)) stats.chatgptOk += 1;
            } else if (status >= 400) {
                if (hostKind === 'stripe') stats.stripeBad += 1;
                if (hostKind === 'chatgpt') stats.chatgptBad += 1;
            }
            if (!shouldLogResponse(url, status)) return;
            if (status >= 400) stats.badResponses += 1;
            const line = `${status} ${response.request?.().method?.() || 'GET'} ${shortUrl(url)}`;
            netBuf.push({ status, url, at: Date.now() });
            debugLog('net', `${labelRef.value} ${line}`, status >= 400 ? 'info' : 'debug');
        } catch (_) { /* ignore */ }
    };

    page.on('console', onConsole);
    page.on('pageerror', onPageError);
    page.on('requestfailed', onRequestFailed);
    page.on('response', onResponse);

    const session = {
        set label(value) {
            labelRef.value = String(value || 'page').trim() || 'page';
        },
        get label() {
            return labelRef.value;
        },
        reset(reason = '') {
            consoleBuf.clear();
            netFailBuf.clear();
            netBuf.clear();
            stats.consoleErrors = 0;
            stats.consoleWarnings = 0;
            stats.requestFailed = 0;
            stats.badResponses = 0;
            stats.stripeOk = 0;
            stats.stripeBad = 0;
            stats.chatgptOk = 0;
            stats.chatgptBad = 0;
            if (reason) {
                debugLog('reset', `${labelRef.value} 计数器已重置 (${clip(reason, 80)})`);
            }
        },
        summarize(reason = '') {
            debugLog(
                'summary',
                `${labelRef.value} reason=${clip(reason, 60)} consoleErr=${stats.consoleErrors} consoleWarn=${stats.consoleWarnings} netFail=${stats.requestFailed} http4xx5xx=${stats.badResponses} stripe ok/bad=${stats.stripeOk}/${stats.stripeBad} chatgpt ok/bad=${stats.chatgptOk}/${stats.chatgptBad}`
            );
            const errors = consoleBuf.list().filter((item) => item.type === 'error' || item.type === 'pageerror').slice(-8);
            for (const item of errors) {
                debugLog('summary-console', `${item.type}: ${item.text}`);
            }
            const fails = netFailBuf.list().slice(-8);
            for (const item of fails) {
                debugLog('summary-net-fail', `${shortUrl(item.url)} err=${item.errText}`);
            }
            const bads = netBuf.list().filter((item) => item.status >= 400).slice(-10);
            for (const item of bads) {
                debugLog('summary-net', `${item.status} ${shortUrl(item.url)}`);
            }
            if (!errors.length && !fails.length && !bads.length) {
                debugLog('summary', `${labelRef.value} 本轮未捕获到 console error / 失败请求 / 4xx-5xx（白屏可能是前端未渲染或空壳页）`);
            }
            return {
                stats: { ...stats },
                consoleErrors: errors,
                netFails: fails,
                badResponses: bads
            };
        },
        detach() {
            try {
                page.off('console', onConsole);
                page.off('pageerror', onPageError);
                page.off('requestfailed', onRequestFailed);
                page.off('response', onResponse);
            } catch (_) { /* ignore */ }
            attached.delete(page);
        }
    };

    attached.set(page, session);
    debugLog('attach', `${labelRef.value} 已启用 console/网络采集（CDP，不依赖页面 F12）`);
    return session;
}

/**
 * 白屏/表单未就绪时导出页面快照，便于对照录像。
 */
async function dumpPageDebugSnapshot(page, reason = '') {
    if (!debugEnabled()) return null;
    if (!page || page.isClosed?.()) {
        debugLog('snapshot', `无法采集：页面已关闭 reason=${clip(reason, 80)}`);
        return null;
    }

    try {
        const info = await page.evaluate(() => {
            const body = document.body;
            const text = body ? String(body.innerText || body.textContent || '') : '';
            const htmlLen = body ? String(body.innerHTML || '').length : 0;
            const iframes = Array.from(document.querySelectorAll('iframe')).map((frame) => ({
                src: String(frame.getAttribute('src') || ''),
                w: frame.clientWidth || 0,
                h: frame.clientHeight || 0
            }));
            return {
                readyState: document.readyState || '',
                title: document.title || '',
                url: location.href || '',
                visibility: document.visibilityState || '',
                textLen: text.trim().length,
                textSample: text.replace(/\s+/g, ' ').trim().slice(0, 240),
                htmlLen,
                iframeCount: iframes.length,
                iframes: iframes.slice(0, 8),
                hasRoot: Boolean(document.getElementById('__next') || document.getElementById('root') || document.querySelector('#app')),
            };
        }).catch((error) => ({
            evaluateError: String(error?.message || error)
        }));

        const url = page.url?.() || info.url || '';
        debugLog('snapshot', `reason=${clip(reason, 60)} url=${shortUrl(url)}`);
        if (info.evaluateError) {
            debugLog('snapshot', `evaluate 失败: ${clip(info.evaluateError)}`);
            return info;
        }
        debugLog(
            'snapshot',
            `title=${clip(info.title, 80)} ready=${info.readyState} vis=${info.visibility} textLen=${info.textLen} htmlLen=${info.htmlLen} iframes=${info.iframeCount} hasRoot=${info.hasRoot}`
        );
        if (info.textSample) {
            debugLog('snapshot', `body: ${clip(info.textSample, 240)}`);
        } else {
            debugLog('snapshot', 'body 文本为空（白屏/未渲染/被遮罩）');
        }
        for (const frame of info.iframes || []) {
            debugLog('snapshot', `iframe ${frame.w}x${frame.h} src=${shortUrl(frame.src) || '(empty)'}`);
        }
        return info;
    } catch (error) {
        debugLog('snapshot', `采集异常: ${clip(error?.message || error)}`);
        return null;
    }
}

module.exports = {
    attachPageDebugCapture,
    dumpPageDebugSnapshot,
    debugEnabled,
    normalizeLogLevel,
    configuredLogLevel,
    shouldLogAt,
    redactSensitive,
    shortUrl
};
