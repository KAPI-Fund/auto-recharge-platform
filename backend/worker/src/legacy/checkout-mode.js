'use strict';

const CHECKOUT_MODES = Object.freeze(['api', 'ui', 'api_then_ui']);

function normalizeCheckoutMode(value) {
    const mode = String(value || 'api').trim().toLowerCase();
    if (!CHECKOUT_MODES.includes(mode)) {
        throw new Error(`不支持的 CHECKOUT_MODE=${mode || '(empty)'}，可选值: ${CHECKOUT_MODES.join(', ')}`);
    }
    return mode;
}

function shouldFallbackToUi(mode) {
    return normalizeCheckoutMode(mode) === 'api_then_ui';
}

function formatCheckoutApiFailure(error) {
    const message = error instanceof Error ? error.message : String(error || '未知错误');
    return message.startsWith('API 创建 Checkout 失败') ? message : `API 创建 Checkout 失败: ${message}`;
}

module.exports = {
    CHECKOUT_MODES,
    normalizeCheckoutMode,
    shouldFallbackToUi,
    formatCheckoutApiFailure
};
