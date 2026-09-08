'use strict';

function normalizeSubmitLabel(text) {
    return String(text || '').replace(/\s+/g, ' ').trim();
}

function isCheckoutSubmitLabel(text) {
    return /^(Subscribe|订阅|Pay)$/i.test(normalizeSubmitLabel(text));
}

function pickCheckoutSubmitCandidate(candidates) {
    const visible = (candidates || []).filter((item) => (
        item
        && item.visible !== false
        && isCheckoutSubmitLabel(item.text)
    ));
    if (!visible.length) {
        return null;
    }
    const submitType = visible.filter((item) => String(item.type || '').toLowerCase() === 'submit');
    const pool = submitType.length ? submitType : visible;
    return pool[pool.length - 1];
}

function createCheckoutSubmitGuard() {
    return {
        clickStarted: false,
        submitStarted: false,
        allow(eventType) {
            if (eventType === 'click') {
                if (this.clickStarted) {
                    return false;
                }
                this.clickStarted = true;
                return true;
            }
            if (eventType === 'submit') {
                if (this.submitStarted) {
                    return false;
                }
                this.submitStarted = true;
                return true;
            }
            return true;
        },
        reset() {
            this.clickStarted = false;
            this.submitStarted = false;
        }
    };
}

function installCheckoutSubmitGuardInBrowser() {
    if (window.__arpSubmitGuardInstalled) {
        return 'exists';
    }
    window.__arpSubmitGuardInstalled = true;
    window.__arpCheckoutClickStarted = false;
    window.__arpCheckoutSubmitStarted = false;

    const labelOf = (el) => String(el && (el.innerText || el.textContent) || '').replace(/\s+/g, ' ').trim();
    const isSubmitControl = (el) => {
        if (!el) {
            return false;
        }
        return /^(Subscribe|订阅|Pay)$/i.test(labelOf(el));
    };

    const blockDuplicate = (event) => {
        if (event.type === 'submit') {
            if (window.__arpCheckoutSubmitStarted) {
                event.preventDefault();
                event.stopImmediatePropagation();
                return;
            }
            window.__arpCheckoutSubmitStarted = true;
            return;
        }
        if (event.type !== 'click') {
            return;
        }
        const target = event.target;
        const control = target && target.closest
            ? target.closest('button, [role="button"], [type="submit"]')
            : target;
        if (!isSubmitControl(control)) {
            return;
        }
        if (window.__arpCheckoutClickStarted) {
            event.preventDefault();
            event.stopImmediatePropagation();
            return;
        }
        window.__arpCheckoutClickStarted = true;
    };

    document.addEventListener('click', blockDuplicate, true);
    document.addEventListener('submit', blockDuplicate, true);
    return 'installed';
}

function resetCheckoutSubmitGuardInBrowser() {
    window.__arpCheckoutClickStarted = false;
    window.__arpCheckoutSubmitStarted = false;
}

function clickVisibleCheckoutSubmitInBrowser() {
    const labels = /^(Subscribe|订阅|Pay)$/i;
    const visible = (el) => {
        const style = window.getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        return style.display !== 'none'
            && style.visibility !== 'hidden'
            && !el.disabled
            && rect.width > 0
            && rect.height > 0;
    };
    const buttons = [...document.querySelectorAll('button, [role="button"]')].filter((el) => (
        labels.test(String(el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim())
        && visible(el)
    ));
    if (!buttons.length) {
        return { ok: false, count: 0 };
    }
    const submitType = buttons.filter((el) => String(el.getAttribute('type') || '').toLowerCase() === 'submit');
    const pool = submitType.length ? submitType : buttons;
    const btn = pool[pool.length - 1];
    btn.click();
    return { ok: true, count: buttons.length };
}

module.exports = {
    normalizeSubmitLabel,
    isCheckoutSubmitLabel,
    pickCheckoutSubmitCandidate,
    createCheckoutSubmitGuard,
    installCheckoutSubmitGuardInBrowser,
    resetCheckoutSubmitGuardInBrowser,
    clickVisibleCheckoutSubmitInBrowser
};
