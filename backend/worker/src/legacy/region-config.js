'use strict';

/**
 * 支付地区配置映射
 * 每个地区包含对应的币种和中文标签
 */
const REGION_CONFIG = {
    AE: { currency: 'AED', label: '阿联酋', aliases: ['United Arab Emirates', 'UAE', '阿联酋'], locale: 'en-AE', timezone: 'Asia/Dubai' },
    AR: { currency: 'ARS', label: '阿根廷', aliases: ['Argentina', '阿根廷'], locale: 'es-AR', timezone: 'America/Argentina/Buenos_Aires' },
    AU: { currency: 'AUD', label: '澳大利亚', aliases: ['Australia', '澳大利亚'], locale: 'en-AU', timezone: 'Australia/Sydney' },
    BR: { currency: 'BRL', label: '巴西', aliases: ['Brazil', '巴西'], locale: 'pt-BR', timezone: 'America/Sao_Paulo' },
    CA: { currency: 'CAD', label: '加拿大', aliases: ['Canada', '加拿大'], locale: 'en-CA', timezone: 'America/Toronto' },
    CH: { currency: 'CHF', label: '瑞士', aliases: ['Switzerland', '瑞士'], locale: 'de-CH', timezone: 'Europe/Zurich' },
    CL: { currency: 'CLP', label: '智利', aliases: ['Chile', '智利'], locale: 'es-CL', timezone: 'America/Santiago' },
    CO: { currency: 'COP', label: '哥伦比亚', aliases: ['Colombia', '哥伦比亚'], locale: 'es-CO', timezone: 'America/Bogota' },
    CZ: { currency: 'CZK', label: '捷克', aliases: ['Czechia', 'Czech Republic', '捷克'], locale: 'cs-CZ', timezone: 'Europe/Prague' },
    DE: { currency: 'EUR', label: '德国', aliases: ['Germany', '德国'], locale: 'de-DE', timezone: 'Europe/Berlin' },
    DK: { currency: 'DKK', label: '丹麦', aliases: ['Denmark', '丹麦'], locale: 'da-DK', timezone: 'Europe/Copenhagen' },
    GB: { currency: 'GBP', label: '英国', aliases: ['United Kingdom', 'Great Britain', '英国'], locale: 'en-GB', timezone: 'Europe/London' },
    HK: { currency: 'HKD', label: '中国香港', aliases: ['Hong Kong', '中国香港'], locale: 'zh-HK', timezone: 'Asia/Hong_Kong' },
    HU: { currency: 'HUF', label: '匈牙利', aliases: ['Hungary', '匈牙利'], locale: 'hu-HU', timezone: 'Europe/Budapest' },
    ID: { currency: 'IDR', label: '印度尼西亚', aliases: ['Indonesia', '印度尼西亚'], locale: 'id-ID', timezone: 'Asia/Jakarta' },
    IL: { currency: 'ILS', label: '以色列', aliases: ['Israel', '以色列'], locale: 'he-IL', timezone: 'Asia/Jerusalem' },
    IN: { currency: 'INR', label: '印度', aliases: ['India', '印度'], currencySymbols: ['₹'], locale: 'en-IN', timezone: 'Asia/Kolkata' },
    JP: { currency: 'JPY', label: '日本', aliases: ['Japan', '日本'], locale: 'ja-JP', timezone: 'Asia/Tokyo' },
    KR: { currency: 'KRW', label: '韩国', aliases: ['South Korea', 'Republic of Korea', '韩国'], locale: 'ko-KR', timezone: 'Asia/Seoul' },
    MX: { currency: 'MXN', label: '墨西哥', aliases: ['Mexico', '墨西哥'], locale: 'es-MX', timezone: 'America/Mexico_City' },
    PH: { currency: 'PHP', label: '菲律宾', aliases: ['Philippines', 'Pilipinas', '菲律宾'], currencySymbols: ['₱'], locale: 'en-PH', timezone: 'Asia/Manila' },
    US: { currency: 'USD', label: '美国', aliases: ['United States', 'United States of America', 'USA', '美国'], currencySymbols: ['$'], locale: 'en-US', timezone: 'America/New_York' },
    SG: { currency: 'SGD', label: '新加坡', aliases: ['Singapore', '新加坡'], currencySymbols: ['S$'], locale: 'en-SG', timezone: 'Asia/Singapore' },
    MY: { currency: 'MYR', label: '马来西亚', aliases: ['Malaysia', '马来西亚'], currencySymbols: ['RM'], locale: 'en-MY', timezone: 'Asia/Kuala_Lumpur' },
    NG: { currency: 'NGN', label: '尼日利亚', aliases: ['Nigeria', '尼日利亚'], locale: 'en-NG', timezone: 'Africa/Lagos' },
    NO: { currency: 'NOK', label: '挪威', aliases: ['Norway', '挪威'], locale: 'nb-NO', timezone: 'Europe/Oslo' },
    NZ: { currency: 'NZD', label: '新西兰', aliases: ['New Zealand', '新西兰'], locale: 'en-NZ', timezone: 'Pacific/Auckland' },
    PE: { currency: 'PEN', label: '秘鲁', aliases: ['Peru', '秘鲁'], locale: 'es-PE', timezone: 'America/Lima' },
    PL: { currency: 'PLN', label: '波兰', aliases: ['Poland', '波兰'], locale: 'pl-PL', timezone: 'Europe/Warsaw' },
    QA: { currency: 'QAR', label: '卡塔尔', aliases: ['Qatar', '卡塔尔'], locale: 'en-QA', timezone: 'Asia/Qatar' },
    RO: { currency: 'RON', label: '罗马尼亚', aliases: ['Romania', '罗马尼亚'], locale: 'ro-RO', timezone: 'Europe/Bucharest' },
    SA: { currency: 'SAR', label: '沙特阿拉伯', aliases: ['Saudi Arabia', '沙特阿拉伯'], locale: 'en-SA', timezone: 'Asia/Riyadh' },
    SE: { currency: 'SEK', label: '瑞典', aliases: ['Sweden', '瑞典'], locale: 'sv-SE', timezone: 'Europe/Stockholm' },
    TH: { currency: 'THB', label: '泰国', aliases: ['Thailand', '泰国'], locale: 'th-TH', timezone: 'Asia/Bangkok' },
    TR: { currency: 'TRY', label: '土耳其', aliases: ['Türkiye', 'Turkey', '土耳其'], locale: 'tr-TR', timezone: 'Europe/Istanbul' },
    TW: { currency: 'TWD', label: '中国台湾', aliases: ['Taiwan', '中国台湾'], locale: 'zh-TW', timezone: 'Asia/Taipei' },
    UA: { currency: 'UAH', label: '乌克兰', aliases: ['Ukraine', '乌克兰'], locale: 'uk-UA', timezone: 'Europe/Kyiv' },
    VN: { currency: 'VND', label: '越南', aliases: ['Vietnam', '越南'], locale: 'vi-VN', timezone: 'Asia/Ho_Chi_Minh' },
    ZA: { currency: 'ZAR', label: '南非', aliases: ['South Africa', '南非'], locale: 'en-ZA', timezone: 'Africa/Johannesburg' }
};

const REGION_CURRENCY_HINTS = {
    PH: ['₱', 'PHP'],
    US: ['$20', '$ 20', '$', 'USD'],
    IN: ['₹', 'INR'],
    SG: ['S$', 'SGD'],
    MY: ['RM', 'MYR']
};

const REGION_PRICE_PATTERNS = {
    PH: [/₱\s*[\d,.]+/, /[\d,.]+\s*₱/, /PHP\s*[\d,.]+/i],
    US: [/\$\s*[\d,.]+/, /USD\s*[\d,.]+/i],
    IN: [/₹\s*[\d,.]+/, /[\d,.]+\s*₹/, /INR\s*[\d,.]+/i],
    SG: [/S\$\s*[\d,.]+/, /[\d,.]+\s*SGD/i],
    MY: [/RM\s*[\d,.]+/, /[\d,.]+\s*MYR/i]
};

const REGION_WRONG_CURRENCY = {
    PH: [/£\s*[\d,.]+/, /[\d,.]+\s*£/, /\bGBP\b/i, /United Kingdom/i, /Great Britain/i, /€\s*[\d,.]+/, /[\d,.]+\s*€/, /\bEUR\b/i],
    IN: [/£\s*[\d,.]+/, /₱\s*[\d,.]+/, /\bPHP\b/i, /\$\s*[\d,.]+/, /\bUSD\b/i, /S\$\s*[\d,.]+/, /RM\s*[\d,.]+/],
    US: [/£\s*[\d,.]+/, /₱\s*[\d,.]+/, /\bGBP\b/i, /\bPHP\b/i, /S\$\s*[\d,.]+/, /RM\s*[\d,.]+/],
    SG: [/£\s*[\d,.]+/, /₱\s*[\d,.]+/, /\$\s*20(?:\.00)?\s*(?:USD|\/)/i, /RM\s*[\d,.]+/],
    MY: [/£\s*[\d,.]+/, /₱\s*[\d,.]+/, /S\$\s*[\d,.]+/, /\$\s*20(?:\.00)?/]
};

function getRegionUiLabels(regionCode) {
    const cfg = getRegionConfig(regionCode);
    return cfg ? [...new Set([cfg.label, ...(cfg.aliases || []), cfg.currency, ...(cfg.currencySymbols || [])])] : [];
}

/** 支持的地区代码列表 */
const SUPPORTED_REGIONS = Object.keys(REGION_CONFIG);

/** 默认支付地区 */
const DEFAULT_REGION = 'PH';

const PLAN_TYPE_LABELS = {
    plus: 'ChatGPT Plus',
    pro_5x: 'ChatGPT Pro 5x',
    pro_20x: 'ChatGPT Pro 20x',
    go: 'ChatGPT Go'
};

/**
 * 检查地区代码是否在支持列表中
 * @param {string} regionCode - 地区代码
 * @returns {boolean}
 */
function isSupportedRegion(regionCode) {
    return SUPPORTED_REGIONS.includes(String(regionCode || '').toUpperCase());
}

/**
 * 获取指定地区的配置（currency, label）
 * @param {string} regionCode - 地区代码
 * @returns {{ currency: string, label: string, locale: string, timezone: string } | null}
 */
function getRegionConfig(regionCode) {
    const code = String(regionCode || '').toUpperCase();
    return REGION_CONFIG[code] || null;
}

function getRegionBilling(regionCode) {
    const cfg = getRegionConfig(regionCode);
    if (!cfg) {
        return null;
    }
    const code = String(regionCode || '').toUpperCase();
    return {
        country: code,
        currency: cfg.currency,
        label: cfg.label
    };
}

function getRegionBrowserProfile(regionCode) {
    const cfg = getRegionConfig(regionCode) || REGION_CONFIG[DEFAULT_REGION];
    return {
        locale: cfg.locale,
        timezoneId: cfg.timezone
    };
}

function getPlanTypeLabel(planType) {
    return PLAN_TYPE_LABELS[planType] || PLAN_TYPE_LABELS.plus;
}

module.exports = {
    REGION_CONFIG,
    SUPPORTED_REGIONS,
    DEFAULT_REGION,
    PLAN_TYPE_LABELS,
    REGION_CURRENCY_HINTS,
    REGION_PRICE_PATTERNS,
    REGION_WRONG_CURRENCY,
    isSupportedRegion,
    getRegionConfig,
    getRegionBilling,
    getRegionBrowserProfile,
    getPlanTypeLabel,
    getRegionUiLabels
};
