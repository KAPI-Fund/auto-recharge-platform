'use strict';

/**
 * Close one recharge card attempt through the Go card-pool boundary.
 *
 * The Go endpoint is the preferred path because it records the diagnostic
 * failure, consumes the allocation exactly once, and always attempts Provider
 * cancellation. The fallback keeps older test doubles/legacy stores usable;
 * each individual operation is still attempted even when an earlier one
 * fails.
 */
async function settleOneTimeCard(store, card, { failureCode = '', failureMessage = '' } = {}) {
    const cardId = String(card?.id || '').trim();
    const allocationId = String(card?.allocationId || card?.allocation_id || '').trim();
    if (!cardId) {
        return { ok: true, accepted: true, skipped: true, errors: [] };
    }

    if (typeof store?.settleCard === 'function') {
        try {
            const response = await store.settleCard(cardId, allocationId, failureCode, failureMessage);
            const ok = response?.ok !== false;
            return {
                ok,
                accepted: true,
                response,
                error: ok ? undefined : new Error(String(response?.error || '卡片收尾未完全成功')),
                errors: ok ? [] : [new Error(String(response?.error || '卡片收尾未完全成功'))],
            };
        } catch (error) {
            // A Go HTTP error means the request reached the API. The API
            // persists provider-release-pending before returning the error, so
            // do not immediately duplicate the remote cancellation call.
            return { ok: false, accepted: Boolean(error?.status), error, errors: [error] };
        }
    }

    const errors = [];
    const attempt = async (operation) => {
        try {
            await operation();
        } catch (error) {
            errors.push(error);
        }
    };
    if (String(failureCode || '').trim() || String(failureMessage || '').trim()) {
        await attempt(() => store.recordCardFailure(cardId, allocationId, failureCode, failureMessage));
    }
    await attempt(() => store.recordCardUsage(cardId, allocationId));
    await attempt(() => store.releaseCard(cardId, allocationId));
    return { ok: errors.length === 0, accepted: true, errors };
}

/**
 * Release a reservation when payment was never finally submitted.
 *
 * This is deliberately different from settleOneTimeCard: the latter records
 * usage and triggers the provider cancellation path. A missing submit button,
 * pre-submit captcha, or a checkout form that never became usable must not
 * make the card look like a bad card.
 */
async function releaseCardReservation(store, card) {
    const cardId = String(card?.id || '').trim();
    const allocationId = String(card?.allocationId || card?.allocation_id || '').trim();
    if (!cardId) {
        return { ok: true, accepted: true, skipped: true, errors: [] };
    }

    const release = store?.releaseCardReservation || store?.releaseCard;
    if (typeof release !== 'function') {
        return {
            ok: false,
            accepted: false,
            error: new Error('卡片预留释放接口不可用'),
            errors: [new Error('卡片预留释放接口不可用')],
        };
    }

    try {
        const response = await release.call(store, cardId, allocationId);
        const ok = response?.ok !== false;
        return {
            ok,
            accepted: true,
            response,
            error: ok ? undefined : new Error(String(response?.error || '卡片预留释放失败')),
            errors: ok ? [] : [new Error(String(response?.error || '卡片预留释放失败'))],
        };
    } catch (error) {
        return { ok: false, accepted: Boolean(error?.status), error, errors: [error] };
    }
}

module.exports = { settleOneTimeCard, releaseCardReservation };
