'use strict';

const GPT_API_PLAN_MAP = Object.freeze({
    plus: 'plus',
    go: 'go',
    pro_5x: 'pro5x',
    pro_20x: 'pro20x'
});

function mapGptApiPlanKey(planType) {
    return GPT_API_PLAN_MAP[String(planType || '').trim()] || 'plus';
}

module.exports = { GPT_API_PLAN_MAP, mapGptApiPlanKey };
