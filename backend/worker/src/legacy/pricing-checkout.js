'use strict';

const {
    getRegionConfig,
    REGION_CONFIG,
    REGION_CURRENCY_HINTS,
    REGION_PRICE_PATTERNS,
    REGION_WRONG_CURRENCY,
    getRegionUiLabels
} = require('./region-config');
const { assertChatGptLoggedIn } = require('./session-auth');
const { clearHumanVerification } = require('./human-verification');

const PRICING_URL = 'https://chatgpt.com/#pricing';

// 所有地区标签、币种和价格检测均来自 region-config.js，避免浏览器流程维护第二套配置。
const REGION_UI_LABELS = Object.fromEntries(
    Object.keys(REGION_CONFIG).map((code) => [code, getRegionUiLabels(code)])
);

// 已通过真实 ChatGPT 定价弹窗记录的触发器；后面的 selector 是兼容旧版/不同渲染结构的降级路径。
const COUNTRY_SELECTOR_TRIGGER_SELECTORS = [
    '[data-testid="country-selector-in-pricing-modal"] button[role="combobox"]',
    '[data-testid="country-selector-in-pricing-modal"] [role="combobox"]',
    'button[aria-haspopup="listbox"]',
    'button[role="combobox"]',
    '[role="combobox"]'
];

const SKIP_REGION_BUTTON_TEXT = /^(Upgrade|Personal|Business|Free|Plus|Pro|Subscribe|Close|Your current plan|升级|订阅|关闭)$/i;

const PLAN_UPGRADE_PATTERNS = {
    go: [/升级至\s*Go/i, /Upgrade to Go/i, /Get Go/i, /Subscribe to Go/i, /^Go$/i],
    plus: [/升级至\s*Plus/i, /Upgrade to Plus/i, /Get Plus/i, /Subscribe to Plus/i, /^Upgrade$/i],
    pro_5x: [/升级至\s*Pro/i, /Upgrade to Pro/i, /Get Pro/i, /^Upgrade$/i],
    pro_20x: [/升级至\s*Pro/i, /Upgrade to Pro/i, /Get Pro/i, /^Upgrade$/i]
};

function normalizeOptionText(text) {
    return String(text || '').replace(/\s+/g, ' ').trim();
}

function matchesCountryLabel(text, labels) {
    const normalized = normalizeOptionText(text).toLowerCase();
    return labels
        .map((label) => normalizeOptionText(label).toLowerCase())
        .filter(Boolean)
        .sort((a, b) => b.length - a.length)
        .some((label) => {
            if (normalized === label) return true;
            if (!normalized.endsWith(label)) return false;
            const prefix = normalized.slice(0, -label.length);
            return !/[a-z0-9]$/i.test(prefix);
        });
}

async function isBusinessTabActive(page) {
    const businessTab = page.getByRole('tab', { name: /^Business$/i }).first();
    if (await businessTab.isVisible({ timeout: 800 }).catch(() => false)) {
        const selected = await businessTab.getAttribute('aria-selected').catch(() => null);
        const state = await businessTab.getAttribute('data-state').catch(() => null);
        if (selected === 'true' || state === 'active') {
            return true;
        }
    }

    const businessCard = await page.getByText(/ChatGPT Business/i).first().isVisible({ timeout: 800 }).catch(() => false);
    const plusCard = await page.getByText(/^ChatGPT Plus$/i).first().isVisible({ timeout: 500 }).catch(() => false);
    return businessCard && !plusCard;
}

async function isPersonalPlanView(page) {
    if (await isBusinessTabActive(page)) {
        return false;
    }

    const plusCard = await page.locator('[role="dialog"]').locator('div').filter({
        hasText: /ChatGPT Plus|^Plus$/
    }).filter({
        has: page.getByRole('button', { name: /Upgrade|升级|Subscribe|Get/i })
    }).first().isVisible({ timeout: 1000 }).catch(() => false);

    if (plusCard) {
        return true;
    }

    const plusTitle = await page.getByText(/^ChatGPT Plus$/i).first().isVisible({ timeout: 800 }).catch(() => false);
    const plusBtn = await page.getByRole('button', { name: /Upgrade to Plus|升级至\s*Plus|Get Plus|Subscribe to Plus/i })
        .first()
        .isVisible({ timeout: 800 })
        .catch(() => false);
    return plusTitle || plusBtn;
}

async function isBusinessPlanView(page) {
    const businessTitle = await page.getByText(/ChatGPT Business/i).first().isVisible({ timeout: 800 }).catch(() => false);
    const onPersonal = await isPersonalPlanView(page);
    return businessTitle && !onPersonal;
}

/**
 * 定价页顶部 Personal / Business 切换（Plus/Pro 都在 Personal 下）
 */
async function switchToPersonalPlans(page) {
    if (await isPersonalPlanView(page)) {
        console.log('✅ [步骤] 已在个人套餐 (Personal) 视图');
        return;
    }

    if (await isBusinessTabActive(page)) {
        console.log('🔄 [步骤] 检测到 Business 标签，正在切换到 Personal...');
    } else {
        console.log('🔄 [步骤] 正在切换到「个人 / Personal」套餐...');
    }

    const personalCandidates = [
        () => page.getByRole('tab', { name: /^Personal$/i }).first(),
        () => page.getByRole('tab', { name: /^个人$/ }).first(),
        () => page.getByRole('button', { name: /^Personal$/i }).first(),
        () => page.getByRole('button', { name: /^个人$/ }).first(),
        () => page.getByRole('radio', { name: /^Personal$/i }).first(),
        () => page.locator('[role="tablist"] [role="tab"]').filter({ hasText: /^Personal$/i }).first(),
        () => page.locator('button').filter({ hasText: /^Personal$/ }).first(),
        () => page.locator('[role="dialog"]').getByText('Personal', { exact: true }).first(),
        () => page.getByText('Personal', { exact: true }).first()
    ];

    for (const getLocator of personalCandidates) {
        try {
            const el = getLocator();
            if (await el.isVisible({ timeout: 1200 })) {
                const selected = await el.getAttribute('aria-selected').catch(() => null);
                const pressed = await el.getAttribute('aria-pressed').catch(() => null);
                if (selected === 'true' || pressed === 'true') {
                    console.log('✅ [步骤] Personal 标签已选中');
                    return;
                }
                await el.scrollIntoViewIfNeeded().catch(() => {});
                await el.click({ timeout: 8000 });
                await page.waitForTimeout(1800);
                if (await isPersonalPlanView(page)) {
                    console.log('✅ [步骤] 已切换到个人套餐 (Personal)');
                    return;
                }
            }
        } catch (_) { /* try next */ }
    }

    if (await isBusinessPlanView(page)) {
        throw new Error('定价页停留在 Business 套餐，未能切换到 Personal，无法购买 Plus/Pro');
    }

    console.warn('[Warn] 未能确认 Personal 切换，将继续查找升级按钮');
}

async function getPricingSurface(page) {
    const dialog = page.locator('[role="dialog"]').first();
    if (await dialog.isVisible({ timeout: 2000 }).catch(() => false)) {
        return dialog;
    }
    return page.locator('main').first();
}

async function readPricingSurfaceText(page) {
    const surface = await getPricingSurface(page);
    return String(await surface.innerText({ timeout: 5000 }).catch(() => '') || '');
}

async function scrollPricingSurface(page) {
    const surface = await getPricingSurface(page);
    await surface.evaluate((node) => {
        node.scrollTop = node.scrollHeight;
    }).catch(() => {});
    await page.evaluate(() => {
        window.scrollTo(0, document.body.scrollHeight);
    }).catch(() => {});
    await page.waitForTimeout(600);
}

async function pageShowsTargetRegionPricing(page, regionCode) {
    const code = String(regionCode || 'PH').toUpperCase();
    const text = await readPricingSurfaceText(page);
    const selectedCountry = await readSelectedCountryLabel(page);
    return isTargetRegionPricingText(text, code, selectedCountry);
}

function isTargetRegionPricingText(text, regionCode, selectedCountry) {
    const code = String(regionCode || 'PH').toUpperCase();
    const normalizedText = String(text || '');
    const positive = REGION_PRICE_PATTERNS[code] || [];
    const negative = REGION_WRONG_CURRENCY[code] || [];
    const hasPositivePrice = positive.some((pattern) => pattern.test(normalizedText));
    const hasWrongCurrency = negative.some((pattern) => pattern.test(normalizedText));
    const hasSelectedTargetCountry = selectedCountry && matchesCountryLabel(selectedCountry, REGION_UI_LABELS[code] || []);

    if (hasWrongCurrency) {
        return false;
    }
    // A currency symbol is not sufficient: special offers can render a zero
    // price while the billing-country selector is still on the old country.
    // Never allow the caller to click an upgrade button without confirming the
    // actual selected billing country.
    if (!hasSelectedTargetCountry) {
        return false;
    }
    if (hasPositivePrice) {
        return true;
    }
    const looseHints = REGION_CURRENCY_HINTS[code] || [];
    return looseHints.some((hint) => normalizedText.includes(hint));
}

const ALL_COUNTRY_NAME_PATTERN = /^(United Kingdom|Philippines|United States|United States of America|India|Singapore|Malaysia|Afghanistan|Algeria|Andorra|Albania|Australia|Canada|Japan|China|英国|菲律宾|美国|印度|新加坡|马来西亚)$/i;

async function isRegionMenuOpen(page) {
    const viewport = page.locator('[data-radix-scroll-area-viewport], [data-radix-select-viewport], [role="listbox"]').last();
    if (await viewport.isVisible({ timeout: 500 }).catch(() => false)) {
        return true;
    }
    const listbox = page.locator('[role="listbox"]').first();
    if (await listbox.isVisible({ timeout: 500 }).catch(() => false)) {
        return true;
    }
    return false;
}

async function closeRegionMenuIfOpen(page) {
    if (!(await isRegionMenuOpen(page))) {
        return;
    }
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(400);
}

async function openRegionPicker(page) {
    if (await isRegionMenuOpen(page)) {
        console.log('[Info] 地区选择器已打开');
        return true;
    }

    const surface = await getPricingSurface(page);
    await scrollPricingSurface(page);

    // The first selectors are recorded from the real ChatGPT pricing modal.
    // Search both the pricing surface and the document because the modal
    // footer can be portaled outside the dialog.  Do not click an unrelated
    // combobox when a recorded selector exists but is hidden: that can select
    // an address/payment control and falsely advance the flow.
    const recordedTriggers = [
        ...COUNTRY_SELECTOR_TRIGGER_SELECTORS.flatMap((selector) => [
            surface.locator(selector).last(),
            page.locator(selector).last()
        ])
    ];
    let recordedSelectorFound = false;

    const tryTriggers = async (triggers) => {
        for (const trigger of triggers) {
            try {
                if ((await trigger.count().catch(() => 0)) > 0) {
                    recordedSelectorFound = true;
                }
                if (!(await trigger.isVisible({ timeout: 1200 }).catch(() => false))) {
                    continue;
                }
                const inListbox = await trigger.evaluate((node) => Boolean(node.closest('[role="listbox"]'))).catch(() => false);
                if (inListbox) {
                    continue;
                }
                const text = normalizeOptionText(await trigger.innerText().catch(() => ''));
                if (!text || SKIP_REGION_BUTTON_TEXT.test(text)) {
                    continue;
                }
                await trigger.scrollIntoViewIfNeeded().catch(() => {});
                await trigger.click({ timeout: 8000 });
                await page.waitForTimeout(900);
                if (await isRegionMenuOpen(page)) {
                    console.log(`[Info] 已打开地区选择器 (${text.slice(0, 40)})`);
                    return true;
                }
            } catch (_) { /* try next */ }
        }
        return false;
    };

    if (await tryTriggers(recordedTriggers)) {
        return true;
    }

    if (recordedSelectorFound) {
        console.warn('[Warn] 已找到真实记录的国家选择器，但当前不可见或不可交互，拒绝使用未知控件替代');
        return false;
    }

    const triggerCandidates = [
        surface.locator('button[aria-haspopup="menu"]').last(),
        surface.locator('button[aria-expanded]').filter({ hasText: ALL_COUNTRY_NAME_PATTERN }).last(),
        surface.locator('[role="dialog"] button').filter({ hasText: ALL_COUNTRY_NAME_PATTERN }).last()
    ];

    return tryTriggers(triggerCandidates);
}

async function getCountryScrollViewport(page) {
    const viewport = page.locator('[data-radix-scroll-area-viewport], [data-radix-select-viewport], [role="listbox"]').last();
    if (await viewport.isVisible({ timeout: 1000 }).catch(() => false)) {
        return viewport;
    }

    const listbox = page.locator('[role="listbox"]').first();
    if (await listbox.isVisible({ timeout: 1000 }).catch(() => false)) {
        return listbox;
    }

    return null;
}

async function clickExactCountryInViewport(viewport, labels) {
    return viewport.evaluate((root, labelList) => {
        const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim().toLowerCase();
        const wanted = labelList.map(normalize);
        const elements = Array.from(root.querySelectorAll('[role="option"], [data-index], button, li, div'));

        for (const el of elements) {
            if (!root.contains(el)) {
                continue;
            }
            const lines = String(el.innerText || '').split('\n').map((line) => line.trim()).filter(Boolean);
            if (lines.length !== 1) {
                continue;
            }
            const text = normalize(lines[0]);
            if (!wanted.includes(text)) {
                continue;
            }
            const childHasSame = Array.from(el.children).some((child) => {
                const childLines = String(child.innerText || '').split('\n').map((line) => line.trim()).filter(Boolean);
                return childLines.length === 1 && normalize(childLines[0]) === text;
            });
            if (childHasSame) {
                continue;
            }
            el.scrollIntoView({ block: 'center' });
            el.click();
            return lines[0];
        }
        return '';
    }, labels).catch(() => '');
}

async function scrollVirtualCountryList(page, labels) {
    const targets = (labels || []).map((item) => String(item || '').trim()).filter(Boolean);
    if (!targets.length) {
        return false;
    }

    const viewport = await getCountryScrollViewport(page);
    if (!viewport) {
        console.warn('[Warn] 未找到可滚动的国家列表容器');
        return false;
    }

    await viewport.evaluate((node) => {
        node.scrollTop = 0;
    }).catch(() => {});
    await page.waitForTimeout(250);

    const jumpRatio = targets.some((label) => /philippines|pilipinas|菲律宾/i.test(label)) ? 0.58 : 0.5;
    await viewport.evaluate((node, ratio) => {
        node.scrollTop = Math.floor(node.scrollHeight * ratio);
    }, jumpRatio).catch(() => {});
    await page.waitForTimeout(300);

    for (let step = 0; step < 160; step += 1) {
        for (const label of targets) {
            const opt = viewport.getByText(new RegExp(`^${escapeRegExp(label)}$`, 'i')).first();
            if ((await opt.count()) > 0) {
                try {
                    await opt.scrollIntoViewIfNeeded({ timeout: 4000 }).catch(() => {});
                    if (await opt.isVisible({ timeout: 300 }).catch(() => false)) {
                        await opt.click({ timeout: 8000 });
                        console.log(`✅ [步骤] Playwright 文本匹配选中: ${label}`);
                        return true;
                    }
                } catch (_) { /* continue scrolling */ }
            }
        }

        const picked = await clickExactCountryInViewport(viewport, targets);
        if (picked) {
            console.log(`✅ [步骤] 虚拟列表第 ${step + 1} 步选中: ${picked}`);
            return true;
        }

        const visibleTexts = await viewport.evaluate((root) => {
            return Array.from(root.querySelectorAll('[role="option"], button, li'))
                .map((el) => String(el.innerText || '').split('\n')[0].trim())
                .filter(Boolean)
                .slice(0, 8);
        }).catch(() => []);
        if (step === 0 || step % 15 === 0) {
            console.log(`[Info] 滚动第 ${step + 1} 步，可见: ${visibleTexts.join(' | ') || '(empty)'}`);
        }

        const atBottom = await viewport.evaluate((node) => {
            const before = node.scrollTop;
            node.scrollTop += Math.max(72, Math.floor(node.clientHeight * 0.32));
            return node.scrollTop <= before;
        }).catch(() => true);

        await page.waitForTimeout(90);
        if (atBottom) {
            break;
        }
    }

    await viewport.hover().catch(() => {});
    for (let wheel = 0; wheel < 80; wheel += 1) {
        const picked = await clickExactCountryInViewport(viewport, targets);
        if (picked) {
            console.log(`✅ [步骤] 滚轮后选中: ${picked}`);
            return true;
        }
        await page.mouse.wheel(0, 280);
        await page.waitForTimeout(60);
    }

    return false;
}

async function scrollAndSelectCountryOption(page, labels) {
    const targets = (labels || []).map((item) => String(item || '').trim()).filter(Boolean);
    if (!targets.length) {
        return false;
    }

    if (!(await isRegionMenuOpen(page))) {
        return false;
    }

    console.log(`[Info] 开始在虚拟列表中查找: ${targets.join(' / ')}`);

    if (await scrollVirtualCountryList(page, targets)) {
        return true;
    }

    for (const label of targets) {
        if (await tryKeyboardCountryFilter(page, label)) {
            return true;
        }
    }

    return false;
}

async function clickRegionOption(page, label) {
    if (await scrollAndSelectCountryOption(page, [label])) {
        return true;
    }

    const optionLocators = [
        page.getByRole('option', { name: new RegExp(`^${escapeRegExp(label)}$`, 'i') }),
        page.getByRole('menuitem', { name: new RegExp(escapeRegExp(label), 'i') }),
        page.getByRole('radio', { name: new RegExp(escapeRegExp(label), 'i') }),
        page.getByRole('button', { name: new RegExp(`^${escapeRegExp(label)}$`, 'i') }),
        page.locator(`[role="option"]:has-text("${label}")`),
        page.locator(`[role="menuitem"]:has-text("${label}")`),
        page.locator(`li:has-text("${label}")`)
    ];

    for (const locator of optionLocators) {
        try {
            const el = locator.first();
            if ((await el.count()) === 0) {
                continue;
            }
            await el.scrollIntoViewIfNeeded({ timeout: 8000 }).catch(() => {});
            if (await el.isVisible({ timeout: 1200 })) {
                await el.click({ timeout: 8000 });
                return true;
            }
        } catch (_) { /* try next */ }
    }
    return false;
}

function escapeRegExp(text) {
    return String(text).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

async function tryKeyboardCountryFilter(page, label) {
    const typeText = String(label || '').trim();
    if (!typeText) {
        return false;
    }

    const viewport = await getCountryScrollViewport(page);
    if (!viewport) {
        return false;
    }

    try {
        await viewport.click({ timeout: 3000 }).catch(() => {});
        await page.waitForTimeout(150);
        await page.keyboard.press('Home').catch(() => {});
        await page.waitForTimeout(100);
        await page.keyboard.type(typeText, { delay: 60 });
        await page.waitForTimeout(500);

        const picked = await clickExactCountryInViewport(viewport, [label]);
        if (picked) {
            console.log(`✅ [步骤] 键盘筛选后已选择: ${picked}`);
            return true;
        }

        const option = page.getByRole('option', { name: new RegExp(`^${escapeRegExp(label)}$`, 'i') }).first();
        if (await option.isVisible({ timeout: 1200 }).catch(() => false)) {
            await option.click({ timeout: 8000 });
            console.log(`✅ [步骤] 键盘筛选后已选择: ${label}`);
            return true;
        }
    } catch (_) { /* ignore */ }

    return false;
}

async function readSelectedCountryLabel(page) {
    const surface = await getPricingSurface(page);
    const candidates = [
        ...COUNTRY_SELECTOR_TRIGGER_SELECTORS.map((selector) => surface.locator(selector)),
        surface.locator('button[aria-haspopup="listbox"]'),
        surface.locator('button[aria-haspopup="menu"]'),
        surface.locator('button[aria-expanded]'),
        surface.locator('[role="combobox"]'),
        surface.locator('[data-testid*="country" i], [aria-label*="country" i], [aria-label*="billing" i]')
    ];
    const labels = Object.values(REGION_UI_LABELS).flat();

    for (const locator of candidates) {
        const count = await locator.count().catch(() => 0);
        for (let index = 0; index < count; index += 1) {
            const candidate = locator.nth(index);
            if (!(await candidate.isVisible({ timeout: 500 }).catch(() => false))) {
                continue;
            }
            const inListbox = await candidate.evaluate((node) => Boolean(node.closest('[role="listbox"], [role="menu"]'))).catch(() => false);
            if (inListbox) {
                continue;
            }
            const text = normalizeOptionText([
                await candidate.innerText().catch(() => ''),
                await candidate.getAttribute('aria-label').catch(() => '')
            ].filter(Boolean).join(' '));
            const matched = labels.find((label) => matchesCountryLabel(text, [label]));
            if (matched) {
                return matched;
            }
        }
    }
    return '';
}

async function selectRegionOption(page, regionCode) {
    const code = String(regionCode || 'PH').toUpperCase();
    const labels = REGION_UI_LABELS[code] || [];
    const preferredLabels = labels.filter((label) => label.length > 2);

    const search = page.locator(
        '[role="listbox"] input[type="search"], [role="listbox"] input[placeholder*="Search" i], [role="listbox"] input[placeholder*="搜索" i], ' +
        '[data-radix-popper-content-wrapper] input[type="search"], input[placeholder*="Search countries" i], ' +
        'input[placeholder*="Search" i], input[placeholder*="搜索" i]'
    ).first();
    if (await search.isVisible({ timeout: 1500 }).catch(() => false)) {
        for (const label of preferredLabels) {
            await search.fill('').catch(() => {});
            await search.fill(label).catch(() => {});
            await page.waitForTimeout(700);
            const viewport = await getCountryScrollViewport(page);
            const picked = viewport
                ? await clickExactCountryInViewport(viewport, [label])
                : '';
            if (picked) {
                console.log(`✅ [步骤] 已通过搜索框选择地区: ${picked}`);
                return verifySelectedCountry(page, preferredLabels);
            }
        }
    }

    if (await scrollAndSelectCountryOption(page, preferredLabels)) {
        return verifySelectedCountry(page, preferredLabels);
    }

    return false;
}

async function verifySelectedCountry(page, labels) {
    await page.waitForTimeout(800);
    const selected = await readSelectedCountryLabel(page);
    if (selected && matchesCountryLabel(selected, labels)) {
        console.log(`✅ [步骤] 地区选择已确认: ${selected}`);
        return true;
    }
    if (selected) {
        console.warn(`[Warn] 地区选择校验失败，当前显示: ${selected}，期望: ${labels.join(' / ')}`);
        return false;
    }
    console.warn(`[Warn] 无法读取定价页当前选中的账单地区，期望: ${labels.join(' / ')}`);
    return false;
}

/** @deprecated 使用 pageShowsTargetRegionPricing */
async function pageShowsCurrency(page, regionCode) {
    return pageShowsTargetRegionPricing(page, regionCode);
}

async function waitForPricingPage(page, timeout = 60000) {
    await page.goto(PRICING_URL, { waitUntil: 'domcontentloaded', timeout });
    await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {});
    await page.waitForTimeout(2500);

    await clearHumanVerification(page, { phase: 'pricing-page', maxWaitMs: 120000 });

    await assertChatGptLoggedIn(page, '定价页');

    const url = page.url();
    if (!url.includes('chatgpt.com')) {
        throw new Error(`定价页打开失败，当前 URL: ${url}`);
    }
    console.log('✅ [步骤] 已打开 ChatGPT 定价页 (#pricing)');
}

/**
 * 在定价页选择账单地区（Personal 视图下，右下角/弹窗内地区切换）
 */
async function selectPricingRegion(page, regionCode) {
    const code = String(regionCode || 'PH').toUpperCase();
    const regionConfig = getRegionConfig(code);
    if (!regionConfig || !REGION_UI_LABELS[code]) {
        throw new Error(`不支持的定价页地区 ${code}`);
    }
    console.log(`🌏 [步骤] 正在选择账单地区: ${regionConfig?.label || code}...`);

    if (await pageShowsTargetRegionPricing(page, code)) {
        console.log(`✅ [步骤] 定价页已显示目标地区价格 (${code})，跳过地区切换`);
        return;
    }

    const surfacePreview = (await readPricingSurfaceText(page)).replace(/\s+/g, ' ').slice(0, 120);
    console.log(`[Info] 当前定价页片段: ${surfacePreview}`);

    for (let attempt = 1; attempt <= 3; attempt += 1) {
        if (await pageShowsTargetRegionPricing(page, code)) {
            console.log(`✅ [步骤] 定价页已显示目标地区价格 (${code})`);
            return;
        }

        if (attempt > 1) {
            console.log(`[Warn] 地区切换重试 ${attempt}/3...`);
            await closeRegionMenuIfOpen(page);
            await scrollPricingSurface(page);
        }

        let menuOpen = await isRegionMenuOpen(page);
        if (!menuOpen) {
            menuOpen = await openRegionPicker(page);
        }

        if (menuOpen || await isRegionMenuOpen(page)) {
            const selected = await selectRegionOption(page, code);
            if (!selected) {
                console.warn(`[Warn] 地区菜单已打开，但未找到 ${code} 对应选项（将尝试滚动列表）`);
            } else {
                await page.waitForTimeout(2500);
            }
        } else {
            console.warn('[Warn] 未能打开地区选择器，尝试直接滚动/点击目标地区');
            await selectRegionOption(page, code);
            await page.waitForTimeout(1500);
        }

        await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {});
        await page.waitForTimeout(1000);

        if (await pageShowsTargetRegionPricing(page, code)) {
            console.log(`✅ [步骤] 已切换到目标地区: ${regionConfig?.label || code}`);
            return;
        }
    }

    const finalText = (await readPricingSurfaceText(page)).replace(/\s+/g, ' ').slice(0, 160);
    const finalSelectedCountry = await readSelectedCountryLabel(page);
    throw new Error(`无法将定价页切换到目标地区 ${code}（${regionConfig?.label || code}），请检查后台支付地区设置。当前选中地区: ${finalSelectedCountry || '(无法读取)'}；当前页面: ${finalText}`);
}

/**
 * 点击对应套餐的升级按钮
 */
async function clickPlanUpgrade(page, planType) {
    const plan = String(planType || 'plus').toLowerCase();
    const patterns = PLAN_UPGRADE_PATTERNS[plan] || PLAN_UPGRADE_PATTERNS.plus;
    console.log(`📦 [步骤] 正在点击升级按钮 (套餐: ${plan})...`);

    await assertChatGptLoggedIn(page, '升级前');
    await switchToPersonalPlans(page);
    await page.waitForTimeout(1000);

    for (const pattern of patterns) {
        try {
            const btn = page.getByRole('button', { name: pattern }).first();
            if (await btn.isVisible({ timeout: 4000 })) {
                await btn.scrollIntoViewIfNeeded().catch(() => {});
                await btn.click({ timeout: 10000 });
                console.log(`✅ [步骤] 已点击升级按钮: ${pattern}`);
                return;
            }
        } catch (_) { /* try next */ }
    }

    const cardTitle = plan === 'plus' ? /ChatGPT Plus/i : plan === 'go' ? /ChatGPT Go|^Go$/i : /ChatGPT Pro/i;
    try {
        const card = page.locator('div').filter({ hasText: cardTitle }).filter({ has: page.getByRole('button') }).first();
        const btn = card.getByRole('button').filter({ hasText: /升级|Upgrade|Subscribe|Get/i }).first();
        if (await btn.isVisible({ timeout: 3000 })) {
            await btn.scrollIntoViewIfNeeded().catch(() => {});
            await btn.click({ timeout: 10000 });
            console.log('✅ [步骤] 已点击套餐卡片内的升级按钮');
            return;
        }
    } catch (_) { /* fall through */ }

    if (plan === 'plus') {
        try {
            const plusCard = page.locator('div').filter({ has: page.getByText(/ChatGPT Plus|^Plus$/i) }).filter({
                has: page.getByRole('button', { name: /^Upgrade$|^升级$/i })
            }).first();
            const upgradeBtn = plusCard.getByRole('button', { name: /^Upgrade$|^升级$/i }).first();
            if (await upgradeBtn.isVisible({ timeout: 3000 })) {
                await upgradeBtn.scrollIntoViewIfNeeded().catch(() => {});
                await upgradeBtn.click({ timeout: 10000 });
                console.log('✅ [步骤] 已点击 Plus 卡片 Upgrade 按钮');
                return;
            }
        } catch (_) { /* fall through */ }
    }

    const fallbackSelectors = plan === 'plus'
        ? [
            'button:has-text("升级至 Plus")',
            'button:has-text("Upgrade to Plus")',
            '[role="dialog"] >> text=ChatGPT Plus >> .. >> .. >> button:has-text("Upgrade")',
            'text=ChatGPT Plus >> xpath=ancestor::div[.//button[contains(., "Upgrade") or contains(., "升级")]][1] >> button'
        ]
        : plan === 'go'
        ? [
            'button:has-text("升级至 Go")',
            'button:has-text("Upgrade to Go")',
            'text=ChatGPT Go >> xpath=ancestor::div[.//button[contains(., "Upgrade") or contains(., "升级")]][1] >> button'
        ]
        : [
            'button:has-text("升级至 Pro")',
            'button:has-text("Upgrade to Pro")',
            'text=ChatGPT Pro >> xpath=ancestor::div[.//button[contains(., "Upgrade") or contains(., "升级")]][1] >> button'
        ];

    for (const sel of fallbackSelectors) {
        try {
            const btn = page.locator(sel).first();
            if (await btn.isVisible({ timeout: 3000 })) {
                await btn.scrollIntoViewIfNeeded().catch(() => {});
                await btn.click({ timeout: 10000 });
                console.log(`✅ [步骤] 已点击升级按钮 (${sel})`);
                return;
            }
        } catch (_) { /* try next */ }
    }

    const onLogin = await page.locator('text=Sign in with Google').isVisible({ timeout: 1000 }).catch(() => false);
    if (onLogin) {
        throw new Error('Session 未生效：定价页显示 Google 登录，无法点击升级按钮');
    }

    if (await isBusinessPlanView(page)) {
        throw new Error(`定价页仍在 Business 视图，未找到 ${plan} 套餐；请确认账号可升级 Plus/Pro`);
    }

    throw new Error(`未找到 ${plan} 套餐的升级按钮，请确认账号可升级且 Session 已登录`);
}

async function waitForCheckoutPage(page, timeout = 90000) {
    console.log('💳 [步骤] 等待跳转到 Checkout 配置套餐页...');
    const deadline = Date.now() + timeout;

    while (Date.now() < deadline) {
        const url = page.url();
        if (url.includes('/checkout/') || url.includes('chatgpt.com/checkout')) {
            await page.waitForLoadState('domcontentloaded', { timeout: 15000 }).catch(() => {});
            await page.waitForTimeout(2000);
            await assertChatGptLoggedIn(page, 'Checkout');
            console.log(`✅ [步骤] Checkout 页面已打开: ${url.slice(0, 80)}...`);
            return url;
        }
        if (url.includes('accounts.google.com')) {
            throw new Error('Session 未生效：升级后跳转到 Google 登录页');
        }
        await page.waitForTimeout(1000);
    }

    throw new Error(`等待 Checkout 页面超时，当前 URL: ${page.url()}`);
}

async function openPricingCheckout(page, { region, planType }) {
    await waitForPricingPage(page);
    await switchToPersonalPlans(page);
    await selectPricingRegion(page, region);
    await clickPlanUpgrade(page, planType);
    const checkoutUrl = await waitForCheckoutPage(page);
    return checkoutUrl;
}

module.exports = {
    PRICING_URL,
    COUNTRY_SELECTOR_TRIGGER_SELECTORS,
    REGION_UI_LABELS,
    REGION_CURRENCY_HINTS,
    REGION_PRICE_PATTERNS,
    REGION_WRONG_CURRENCY,
    isTargetRegionPricingText,
    openPricingCheckout,
    waitForPricingPage,
    selectPricingRegion,
    switchToPersonalPlans,
    clickPlanUpgrade,
    waitForCheckoutPage,
    pageShowsTargetRegionPricing
};
