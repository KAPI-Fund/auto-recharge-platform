'use strict';

const { REGION_CONFIG } = require('./region-config');

function resolveBillingRegion({ task, region, cfg } = {}) {
    const candidates = [
        region,
        task?.paymentRegion,
        task?.payment_region,
        task?.region,
        task?.country,
        cfg?.country,
        'PH'
    ];
    return candidates
        .map((value) => String(value || '').trim().toUpperCase())
        .find((value) => value && REGION_CONFIG[value]) || 'PH';
}

/**
 * Resolve the billing country/currency sent to the protocol provider.
 * Task/product metadata wins over the legacy global provider setting.
 */
function resolveGptApiBilling({ task, region, cfg } = {}) {
    const country = resolveBillingRegion({ task, region, cfg });
    const configCurrency = country && REGION_CONFIG[country]?.currency;
    const currency = String(configCurrency || cfg?.currency || 'PHP').trim().toUpperCase();
    return { country: country || 'PH', currency };
}

module.exports = { resolveBillingRegion, resolveGptApiBilling };
