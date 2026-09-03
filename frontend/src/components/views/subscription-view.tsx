"use client";

import { useState } from "react";
import { ExternalLink, Receipt } from "lucide-react";
import { PublicSiteHeader } from "@/components/public-site-header";
import { checkSubscription } from "@/lib/legacy-api";
import type { JsonMap } from "@/lib/legacy-api";
import { cn } from "@/lib/utils";

type Notice = { tone: "success" | "error" | "info"; text: string } | null;

function displayValue(value: unknown) {
  return value === null || value === undefined || value === "" ? "—" : String(value);
}

export function SubscriptionView() {
  const [session, setSession] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice>(null);
  const [result, setResult] = useState<JsonMap | null>(null);

  async function handleCheck() {
    const sessionValue = session.trim();
    if (!sessionValue) {
      setResult(null);
      setNotice({ tone: "error", text: "请粘贴完整 Session JSON" });
      return;
    }

    setBusy(true);
    setResult(null);
    setNotice({ tone: "info", text: "正在查询订阅状态…" });
    try {
      const payload = await checkSubscription({
        session: sessionValue,
        timezone_offset_min: -new Date().getTimezoneOffset(),
      });
      if (!payload.success) throw new Error(String(payload.message || "查询失败"));
      setResult((payload.data || {}) as JsonMap);
      setNotice(null);
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "查询失败，请稍后重试" });
    } finally {
      setBusy(false);
    }
  }

  const hasActiveSubscription = Boolean(result?.hasActiveSubscription);
  const billingUrl = String(result?.billingPageUrl || "https://chatgpt.com/account/manage");
  const rows: Array<[string, unknown]> = [
    ["账号", result?.email],
    ["账号 ID", result?.accountId],
    ["套餐", result?.plan],
    ["订阅渠道", result?.subscriptionChannel],
    ["货币", result?.currency],
    ["到期时间", result?.expiresAtDisplay],
    ["剩余天数", result?.remainingDaysDisplay],
    ["自动续费", result?.autoRenew],
    ["曾付费", result?.hasPreviouslyPaid],
    ["查询时间", result?.queriedAtDisplay],
  ];

  return (
    <main className="recharge-page">
      <PublicSiteHeader />
      <div className="page-shell">
        <header className="site-hero compact-hero">
          <h1>发票助手</h1>
          <p className="hero-subtitle">粘贴 Session 即可查询当前订阅状态，并跳转 OpenAI 官方账单页面下载发票。</p>
        </header>

        <section className="recharge-card subscription-card">
          <div className="notice-banner">
            <Receipt />
            <p>发票由 OpenAI 官方账单系统出具。本工具仅查询订阅信息与提供操作指引，不代开发票。</p>
          </div>

          <section className="subscription-query-section">
            <label className="field-label" htmlFor="sessionInput">粘贴完整 Session JSON</label>
            <div className="textarea-wrapper">
              <a href="https://chatgpt.com/api/auth/session" target="_blank" rel="noopener" className="get-token-btn-floating">获取 Session</a>
              <textarea id="sessionInput" value={session} onChange={(event) => setSession(event.target.value)} placeholder="打开上方链接，全选复制完整 JSON 粘贴到此处（保留 user、expires、accessToken 等全部字段）" rows={5} />
            </div>
            <p className="field-hint subscription-field-hint">Session 仅用于本次查询，不会被保存。查询完成后可在下方打开官方账单页面。</p>
            <div className="subscription-query-actions">
              <button type="button" className={cn("btn-primary", "btn-lg", busy && "is-loading")} onClick={() => void handleCheck()} disabled={busy}>
                <span className="btn-text" style={{ display: busy ? "none" : undefined }}>查询订阅</span>
                <span className="loader" style={{ display: busy ? "block" : undefined }} />
              </button>
            </div>
          </section>

          {notice ? <div className={cn("status-message", notice.tone === "error" ? "status-error" : "status-success")} style={{ display: "block" }}>{notice.text}</div> : null}

          {result ? (
            <section className="subscription-result">
              <div className="subscription-result-head">
                <h2>订阅信息</h2>
                <span className={cn("flow-badge", hasActiveSubscription ? "success" : "pending")}>{hasActiveSubscription ? "订阅有效" : "未订阅 / 已过期"}</span>
              </div>
              <div className="query-result-card">
                {rows.map(([label, value]) => <div className="query-row" key={label}><span>{label}</span><strong>{displayValue(value)}</strong></div>)}
              </div>
              <div className="subscription-actions subscription-billing-actions">
                <a href={billingUrl} target="_blank" rel="noopener" className="btn-primary btn-lg action-link-btn"><ExternalLink />打开 OpenAI 账单页面</a>
              </div>
            </section>
          ) : null}

          <details className="subscription-guide-panel">
            <summary>发票下载步骤指引</summary>
            <section className="guide-steps">
              <div className="guide-step"><span className="guide-step-num">1</span><div><h3>登录 ChatGPT 账号</h3><p>使用已开通 Plus / Pro 的账号登录 <a href="https://chatgpt.com" target="_blank" rel="noopener">chatgpt.com</a>。</p></div></div>
              <div className="guide-step"><span className="guide-step-num">2</span><div><h3>进入订阅与账单</h3><p>点击左下角头像 → <strong>Settings（设置）</strong> → <strong>Account（账户）</strong> → <strong>Manage（管理订阅）</strong>。</p></div></div>
              <div className="guide-step"><span className="guide-step-num">3</span><div><h3>查看付款记录</h3><p>在 Stripe 账单页面找到对应扣款记录，点击 <strong>View invoice / Download invoice</strong> 下载 PDF 发票。</p></div></div>
              <div className="guide-step"><span className="guide-step-num">4</span><div><h3>修改账单信息（可选）</h3><p>如需公司抬头或税号，可在 Billing 页面更新 <strong>Billing details</strong>，后续发票将按新信息生成。</p></div></div>
            </section>
          </details>
        </section>

        <details className="faq-panel" open>
          <summary>常见问题</summary>
          <ul>
            <li><strong>查询失败？</strong> 请确认 Session 来自已登录的 ChatGPT 账号，且 accessToken 未过期。</li>
            <li><strong>发票币种是什么？</strong> 以 Stripe 实际扣款币种为准，部分账号 API 不返回币种字段。</li>
            <li><strong>开通后多久能下载？</strong> 支付成功后通常几分钟内可在 Billing 页面看到记录。</li>
            <li><strong>找不到发票？</strong> 确认登录的是已付费账号，并检查是否使用了团队/企业工作区或 App Store 订阅。</li>
          </ul>
        </details>
      </div>
    </main>
  );
}
