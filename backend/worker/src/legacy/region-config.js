'use strict';

/**
 * 支付地区配置映射
 * 每个地区包含对应的币种和中文标签
 */
const REGION_CONFIG = {
    AE: { currency: 'AED', label: '阿联酋', locale: 'en-AE', timezone: 'Asia/Dubai' },
    AR: { currency: 'ARS', label: '阿根廷', locale: 'es-AR', timezone: 'America/Argentina/Buenos_Aires' },
    AU: { currency: 'AUD', label: '澳大利亚', locale: 'en-AU', timezone: 'Australia/Sydney' },
    BR: { currency: 'BRL', label: '巴西', locale: 'pt-BR', timezone: 'America/Sao_Paulo' },
    CA: { currency: 'CAD', label: '加拿大', locale: 'en-CA', timezone: 'America/Toronto' },
    CH: { currency: 'CHF', label: '瑞士', locale: 'de-CH', timezone: 'Europe/Zurich' },
    CL: { currency: 'CLP', label: '智利', locale: 'es-CL', timezone: 'America/Santiago' },
    CO: { currency: 'COP', label: '哥伦比亚', locale: 'es-CO', timezone: 'America/Bogota' },
    CZ: { currency: 'CZK', label: '捷克', locale: 'cs-CZ', timezone: 'Europe/Prague' },
    DE: { currency: 'EUR', label: '德国', locale: 'de-DE', timezone: 'Europe/Berlin' },
    DK: { currency: 'DKK', label: '丹麦', locale: 'da-DK', timezone: 'Europe/Copenhagen' },
    GB: { currency: 'GBP', label: '英国', locale: 'en-GB', timezone: 'Europe/London' },
    HK: { currency: 'HKD', label: '中国香港', locale: 'zh-HK', timezone: 'Asia/Hong_Kong' },
    HU: { currency: 'HUF', label: '匈牙利', locale: 'hu-HU', timezone: 'Europe/Budapest' },
    ID: { currency: 'IDR', label: '印度尼西亚', locale: 'id-ID', timezone: 'Asia/Jakarta' },
    IL: { currency: 'ILS', label: '以色列', locale: 'he-IL', timezone: 'Asia/Jerusalem' },
    IN: { currency: 'INR', label: '印度', locale: 'en-IN', timezone: 'Asia/Kolkata' },
    JP: { currency: 'JPY', label: '日本', locale: 'ja-JP', timezone: 'Asia/Tokyo' },
    KR: { currency: 'KRW', label: '韩国', locale: 'ko-KR', timezone: 'Asia/Seoul' },
    MX: { currency: 'MXN', label: '墨西哥', locale: 'es-MX', timezone: 'America/Mexico_City' },
    PH: { currency: 'PHP', label: '菲律宾', locale: 'en-PH', timezone: 'Asia/Manila' },
    US: { currency: 'USD', label: '美国', locale: 'en-US', timezone: 'America/New_York' },
    SG: { currency: 'SGD', label: '新加坡', locale: 'en-SG', timezone: 'Asia/Singapore' },
    MY: { currency: 'MYR', label: '马来西亚', locale: 'en-MY', timezone: 'Asia/Kuala_Lumpur' },
    NG: { currency: 'NGN', label: '尼日利亚', locale: 'en-NG', timezone: 'Africa/Lagos' },
    NO: { currency: 'NOK', label: '挪威', locale: 'nb-NO', timezone: 'Europe/Oslo' },
    NZ: { currency: 'NZD', label: '新西兰', locale: 'en-NZ', timezone: 'Pacific/Auckland' },
    PE: { currency: 'PEN', label: '秘鲁', locale: 'es-PE', timezone: 'America/Lima' },
    PL: { currency: 'PLN', label: '波兰', locale: 'pl-PL', timezone: 'Europe/Warsaw' },
    QA: { currency: 'QAR', label: '卡塔尔', locale: 'en-QA', timezone: 'Asia/Qatar' },
    RO: { currency: 'RON', label: '罗马尼亚', locale: 'ro-RO', timezone: 'Europe/Bucharest' },
    SA: { currency: 'SAR', label: '沙特阿拉伯', locale: 'en-SA', timezone: 'Asia/Riyadh' },
    SE: { currency: 'SEK', label: '瑞典', locale: 'sv-SE', timezone: 'Europe/Stockholm' },
    TH: { currency: 'THB', label: '泰国', locale: 'th-TH', timezone: 'Asia/Bangkok' },
    TR: { currency: 'TRY', label: '土耳其', locale: 'tr-TR', timezone: 'Europe/Istanbul' },
    TW: { currency: 'TWD', label: '中国台湾', locale: 'zh-TW', timezone: 'Asia/Taipei' },
    UA: { currency: 'UAH', label: '乌克兰', locale: 'uk-UA', timezone: 'Europe/Kyiv' },
    VN: { currency: 'VND', label: '越南', locale: 'vi-VN', timezone: 'Asia/Ho_Chi_Minh' },
    ZA: { currency: 'ZAR', label: '南非', locale: 'en-ZA', timezone: 'Africa/Johannesburg' }
};

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
    isSupportedRegion,
    getRegionConfig,
    getRegionBilling,
    getRegionBrowserProfile,
    getPlanTypeLabel
};
