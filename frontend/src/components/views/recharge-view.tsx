"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { ExternalLink, Info, Receipt, RotateCcw, ShieldCheck, Zap } from "lucide-react";
import { PublicSiteHeader } from "@/components/public-site-header";
import { createRechargeTask, createStoreOrder, getRechargeTask, getStoreOrder, listPlans, verifyCDK } from "@/lib/platform-api";
import { getCDKStatus, type JsonMap } from "@/lib/legacy-api";
import type { Plan, PublicTask, StoreOrder } from "@/lib/platform-types";
import { cn, formatPrice } from "@/lib/utils";

const phoneCountries = [["+86", "中国大陆"], ["+1", "美国 / 加拿大"], ["+44", "英国"], ["+81", "日本"], ["+82", "韩国"], ["+65", "新加坡"], ["+60", "马来西亚"], ["+63", "菲律宾"], ["+61", "澳大利亚"], ["+49", "德国"], ["+33", "法国"], ["+852", "中国香港"], ["+886", "中国台湾"]] as const;
const SESSION_URL = "https://chatgpt.com/api/auth/session";

type Notice = { tone: "success" | "danger" | "info"; text: string } | null;
type RechargeTab = "redeem" | "purchase" | "query";

function taskLabel(status: PublicTask["status"]) {
  return { queued: "排队中", running: "开通中", succeeded: "已完成", failed: "失败", manual: "失败" }[status];
}

function buyerTaskMessage(status: PublicTask["status"]) {
  if (status === "succeeded") return "开通成功";
  if (status === "failed" || status === "manual") return "内部错误，请联系客服";
  if (status === "queued") return "任务已提交，请耐心等待";
  return "正在处理中，请耐心等待";
}

function flowClass(task: PublicTask | null, verifiedPlan: Plan | null) {
  if (!task) return verifiedPlan ? "cdk_ok" : "pending";
  if (task.status === "succeeded") return "success";
  if (task.status === "failed" || task.status === "manual") return task.status;
  return "running";
}

function statusLabel(status: string) {
  return { available: "未使用", queued: "排队中", running: "开通中", processing: "开通中", succeeded: "已完成", failed: "失败", manual: "失败", used: "已使用", disabled: "已停用", "开通中": "开通中" }[status] || status || "未使用";
}

function planTitle(plan: Plan) {
  return plan.name || plan.code;
}

function planTag(plan: Plan) {
  return { plus: "标准版", pro_5x: "高用量", pro_20x: "旗舰" }[plan.code] || "套餐";
}

function planDescription(plan: Plan) {
  return plan.description || "";
}

function planPrice(plan: Plan) {
  if (plan.currency === "CNY") {
    return new Intl.NumberFormat("zh-CN", { style: "currency", currency: "CNY", maximumFractionDigits: 2 }).format(plan.price);
  }
  return formatPrice(plan.price, plan.currency || "USD");
}

function planIsSoldOut(plan: Plan) {
  if (typeof plan.soldOut === "boolean") return plan.soldOut;
  return Boolean((plan.saleLimit || 0) > 0 && (plan.soldCount || 0) >= (plan.saleLimit || 0));
}

function planIsUnavailable(plan: Plan) {
  return planIsSoldOut(plan) || plan.purchaseEnabled === false || (plan.saleLimit || 0) <= 0;
}

function planStockLabel(plan: Plan) {
  if (planIsSoldOut(plan)) return "已售罄，补货中";
  if ((plan.saleLimit || 0) > 0) return `剩余 ${Math.max(0, plan.remainingQuantity ?? (plan.saleLimit || 0) - (plan.soldCount || 0))} 件`;
  return "库存未配置";
}

function statusClass(status: string) {
  if (status === "used") return "status-used";
  if (["processing", "queued", "running", "开通中"].includes(status)) return "status-processing";
  return "status-unused";
}

export function RechargeView({ initialTab = "redeem", initialPlanCode = "" }: { initialTab?: RechargeTab; initialPlanCode?: string }) {
  const [activeTab, setActiveTab] = useState<RechargeTab>(initialTab);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [purchasePlanCode, setPurchasePlanCode] = useState(initialPlanCode);
  const [purchaseEmail, setPurchaseEmail] = useState("");
  const [purchasePhoneCountryCode, setPurchasePhoneCountryCode] = useState("+86");
  const [purchasePhoneNumber, setPurchasePhoneNumber] = useState("");
  const [storeOrder, setStoreOrder] = useState<StoreOrder | null>(null);
  const [storeOrderId, setStoreOrderId] = useState("");
  const [code, setCode] = useState("");
  const codeRef = useRef("");
  const [session, setSession] = useState("");
  const [verifiedPlan, setVerifiedPlan] = useState<Plan | null>(null);
  const [task, setTask] = useState<PublicTask | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice>(null);
  const [queryCode, setQueryCode] = useState("");
  const [queryResult, setQueryResult] = useState<JsonMap | null>(null);
  const [queryBusy, setQueryBusy] = useState(false);

  useEffect(() => {
    listPlans().then((items) => {
      setPlans(items);
      const requestedPlan = new URLSearchParams(window.location.search).get("plan") || initialPlanCode;
      setPurchasePlanCode((current) => requestedPlan && items.some((item) => item.code === requestedPlan)
        ? requestedPlan
        : items.some((item) => item.code === current && !planIsSoldOut(item))
          ? current
          : items.find((item) => !planIsSoldOut(item))?.code || items[0]?.code || "");
      if (requestedPlan && items.some((item) => item.code === requestedPlan)) {
        window.setTimeout(() => document.getElementById("plans")?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
      }
    }).catch(() => undefined);
  }, [initialPlanCode]);

  useEffect(() => {
    setActiveTab(initialTab);
  }, [initialTab]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const orderId = params.get("order_id") || "";
    const requestedTab = params.get("tab");
    const checkoutState = params.get("checkout") || params.get("payment");
    if (requestedTab === "purchase") setActiveTab("purchase");
    if (orderId) setStoreOrderId(orderId);
    if (checkoutState === "cancel" || checkoutState === "cancelled") setNotice({ tone: "danger", text: "支付已取消，订单仍可通过订单号、手机号或邮箱查询。" });
  }, []);

  useEffect(() => {
    if (!storeOrderId) return;
    let active = true;
    let timer: number | undefined;
    const load = async () => {
      try {
        const order = await getStoreOrder(storeOrderId);
        if (!active) return;
        setStoreOrder(order);
        // A debug purchase already puts the CDK into the redeem flow. Only
        // restore it from the order when the current flow has no code yet;
        // otherwise polling must not clear a successful CDK verification.
        if (order.cdkCode && !codeRef.current.trim()) {
          codeRef.current = order.cdkCode;
          setCode(order.cdkCode);
          setVerifiedPlan(null);
          setNotice({ tone: "success", text: "购买完成，兑换码已填入。请继续粘贴 Session 完成开通。" });
        }
        if (active && order.status === "pending") timer = window.setTimeout(() => void load(), 1500);
      } catch {
        // Keep the payment state visible while the redirect settles.
      }
    };
    void load();
    return () => { active = false; if (timer) window.clearTimeout(timer); };
  }, [storeOrderId]);

  useEffect(() => {
    if (!task || !["queued", "running"].includes(task.status)) return;
    const timer = window.setInterval(async () => {
      try { setTask(await getRechargeTask(task.id)); } catch { /* Keep the latest task snapshot. */ }
    }, 1600);
    return () => window.clearInterval(timer);
  }, [task]);

  useEffect(() => {
    if (!task) return;
    if (task.status === "failed" || task.status === "manual") setNotice({ tone: "danger", text: buyerTaskMessage(task.status) });
    else setNotice({ tone: "success", text: buyerTaskMessage(task.status) });
  }, [task]);

  useEffect(() => {
    const taskId = String(queryResult?.taskId || "").trim();
    const taskStatus = String(queryResult?.taskStatus || "").trim();
    if (!taskId || !["queued", "running"].includes(taskStatus) || !queryCode.trim()) return;
    let active = true;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const response = await getCDKStatus(queryCode.trim());
        if (!active || !response.success) return;
        const data = (response.data || {}) as JsonMap;
        setQueryResult(data);
        const nextStatus = String(data.taskStatus || "").trim();
        if (["queued", "running"].includes(nextStatus)) {
          timer = window.setTimeout(() => void poll(), 1600);
        }
      } catch {
        if (active) timer = window.setTimeout(() => void poll(), 2500);
      }
    };
    timer = window.setTimeout(() => void poll(), 1600);
    return () => { active = false; if (timer) window.clearTimeout(timer); };
  }, [queryCode, queryResult?.taskId, queryResult?.taskStatus]);

  async function handlePurchase() {
    const plan = plans.find((item) => item.code === purchasePlanCode) || plans[0];
    const email = purchaseEmail.trim();
    const phoneNumber = purchasePhoneNumber.replace(/[^0-9\s-]/g, "");
    if (!plan) return setNotice({ tone: "danger", text: "暂无可购买套餐" });
    if (planIsUnavailable(plan)) return setNotice({ tone: "danger", text: planIsSoldOut(plan) ? "已售罄，补货中" : "库存未配置" });
    if (!email) return setNotice({ tone: "danger", text: "请输入联系邮箱" });
    if (!phoneNumber) return setNotice({ tone: "danger", text: "请输入手机号" });
    setBusy(true);
    setNotice(null);
    try {
      const result = await createStoreOrder({ planCode: plan.code, email, phoneCountryCode: purchasePhoneCountryCode, phoneNumber });
      setStoreOrder(result.order);
      setStoreOrderId(result.order.id);
      if (result.checkoutUrl) {
        setNotice({ tone: "success", text: result.message || "正在跳转到支付页面" });
        window.location.assign(result.checkoutUrl);
        return;
      }
      if (result.order.cdkCode) {
        codeRef.current = result.order.cdkCode;
        setCode(result.order.cdkCode);
        setVerifiedPlan(null);
        selectTab("redeem");
        setNotice({ tone: "success", text: result.message || "调试购买完成，兑换码已发放" });
      }
    } catch (error) {
      setNotice({ tone: "danger", text: error instanceof Error ? error.message : "创建购买订单失败" });
    } finally {
      setBusy(false);
    }
  }

  async function handleVerify() {
    setTask(null);
    setVerifiedPlan(null);
    setQueryResult(null);
    if (!code.trim()) return setNotice({ tone: "danger", text: "请输入兑换码" });
    setBusy(true);
    setNotice(null);
    try {
      const result = await verifyCDK(code.trim());
      if (result.status === "processing" && result.task) {
        setVerifiedPlan(result.cdk?.plan || null);
        setTask(result.task);
        setNotice({ tone: "success", text: buyerTaskMessage(result.task.status) });
      } else if (result.cdk?.plan) {
        setVerifiedPlan(result.cdk.plan);
        setNotice({ tone: "success", text: `兑换码有效，已匹配 ${result.cdk.plan.name}` });
      } else {
        throw new Error("兑换码无效或已使用");
      }
    } catch (error) {
      setNotice({ tone: "danger", text: error instanceof Error ? error.message : "网络请求失败" });
    } finally {
      setBusy(false);
    }
  }

  async function handleStart() {
    const sessionValue = session.trim();
    if (!sessionValue) return setNotice({ tone: "danger", text: "请粘贴完整 Session JSON" });
    if (!verifiedPlan) return setNotice({ tone: "danger", text: "请先验证 CDK" });
    setBusy(true);
    setNotice(null);
    try {
      const result = await createRechargeTask({ code: code.trim(), session: sessionValue, mode: "" });
      setTask(result.task);
      setNotice({ tone: "success", text: buyerTaskMessage(result.task.status) });
    } catch (error) {
      setNotice({ tone: "danger", text: error instanceof Error ? error.message : "网络连接失败，请重试" });
    } finally {
      setBusy(false);
    }
  }

  async function handleQuery() {
    if (!queryCode.trim()) return setNotice({ tone: "danger", text: "请输入激活码" });
    setQueryBusy(true);
    setQueryResult(null);
    setNotice(null);
    try {
      const response = await getCDKStatus(queryCode.trim());
      if (!response.success) throw new Error(String(response.message || "查询失败"));
      const data = (response.data || {}) as JsonMap;
      setQueryResult(data);
      setNotice({ tone: String(data.status || "") === "used" ? "danger" : "success", text: "" });
    } catch (error) {
      setNotice({ tone: "danger", text: error instanceof Error ? error.message : "查询失败" });
    } finally {
      setQueryBusy(false);
    }
  }

  function resetFlow() {
    codeRef.current = "";
    setCode("");
    setSession("");
    setVerifiedPlan(null);
    setTask(null);
    setNotice(null);
  }

  const taskActive = Boolean(task && ["queued", "running"].includes(task.status));
  const taskDone = task?.status === "succeeded";
  const taskButtonText = task?.status === "succeeded" ? "激活成功" : task?.status === "failed" || task?.status === "manual" ? "重新尝试" : taskActive ? "开通中..." : "确认并启动自动化开通";
  const sessionChange = (value: string) => {
    setSession(value);
    if (!value.trim()) setNotice(null);
  };
  const noticeVisible = Boolean(notice && (notice.text || queryResult));
  const queryStatus = String(queryResult?.taskStatus || queryResult?.status || (queryResult?.usedAt ? "used" : ""));
  const queryProgress = Math.max(0, Math.min(100, Number(queryResult?.progress) || 0));

  function choosePurchasePlan(planCode: string) {
    if (taskActive) return;
    const selected = plans.find((item) => item.code === planCode);
    if (selected && planIsUnavailable(selected)) {
      setNotice({ tone: "danger", text: planIsSoldOut(selected) ? "已售罄，补货中" : "库存未配置" });
      return;
    }
    setPurchasePlanCode(planCode);
    selectTab("purchase");
    const params = new URLSearchParams(window.location.search);
    params.set("tab", "purchase");
    params.set("plan", planCode);
    window.history.replaceState(null, "", `/recharge?${params.toString()}#plans`);
    window.setTimeout(() => document.getElementById("plans")?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
  }

  const selectedPurchasePlan = plans.find((item) => item.code === purchasePlanCode) || plans[0];
  const selectedPurchasePlanSoldOut = Boolean(selectedPurchasePlan && planIsUnavailable(selectedPurchasePlan));

  function selectTab(tab: RechargeTab) {
    setActiveTab(tab);
    const params = new URLSearchParams(window.location.search);
    if (tab === "redeem") {
      params.delete("tab");
      params.delete("plan");
    } else {
      params.set("tab", tab);
    }
    const query = params.toString();
    window.history.replaceState(null, "", `/recharge${query ? `?${query}` : ""}`);
  }

  return (
    <main className="recharge-page">
      <PublicSiteHeader />
      <div className="page-shell">
        <header className="site-hero"><div className="hero-eyebrow">Instant AI Top-Ups</div><h1>ChatGPT 订阅自动开通</h1><p className="hero-subtitle">通过官方 Checkout 流程自助开通 KC ChatGPT PLUS，兑换卡密后粘贴 Session 即可自动完成支付开通。</p><div className="feature-badges"><span className="feature-badge"><ShieldCheck /> 官方结账</span><span className="feature-badge"><Zap /> 极速开通</span><span className="feature-badge"><RotateCcw /> 失败可回滚</span><Link href="/subscription" className="feature-badge feature-link"><Receipt /> 发票助手</Link></div></header>
        {activeTab !== "purchase" ? <section className="plan-showcase" aria-label="可购买套餐">{plans.map((plan) => { const soldOut = planIsSoldOut(plan); const unavailable = planIsUnavailable(plan); return <button key={plan.code} type="button" className={cn("plan-card", plan.code === "pro_5x" && "highlight", unavailable && "sold-out")} disabled={taskActive || unavailable} onClick={() => choosePurchasePlan(plan.code)}><div className="plan-card-head"><h3>{planTitle(plan)}</h3><span className={cn("plan-tag", plan.code !== "plus" && "pro")}>{planTag(plan)}</span></div><div className="plan-card-price">{planPrice(plan)}</div><p>{planDescription(plan)}</p><span className={cn("plan-card-stock", unavailable && "sold-out")}>{planStockLabel(plan)}</span><span className="plan-card-action">{unavailable ? (soldOut ? "已售罄，补货中" : "库存未配置") : "选择套餐"} <span aria-hidden="true">{unavailable ? "" : "→"}</span></span></button>; })}</section> : null}
        <section className="recharge-card"><div className="card-head"><div><h2>卡密兑换</h2><p>按步骤完成自助开通</p></div><span className={cn("flow-badge", flowClass(task, verifiedPlan))}>{task ? taskLabel(task.status) : verifiedPlan ? "待提交 Session" : "待开始"}</span></div><div className="stepper"><div className={cn("step-item", !verifiedPlan && "active", verifiedPlan && "done")}><span className="step-num">1</span><span className="step-label">输入卡密</span></div><div className="step-line" /><div className={cn("step-item", verifiedPlan && !task && "active", task && "done")}><span className="step-num">2</span><span className="step-label">提交 Session</span></div></div><div className="notice-banner"><Info /><p>ChatGPT 套餐自助开通：提交后系统将自动打开官方 Checkout 页面并完成订阅开通，无需提供账号密码。一卡一充，3-5分钟开通完毕，如遇到卡住，请联系客服进行处理。</p></div>
          <nav className="tab-nav"><button className={cn("tab-btn", activeTab === "redeem" && "active")} disabled={taskActive} onClick={() => selectTab("redeem")}>兑换开通</button><button className={cn("tab-btn", activeTab === "purchase" && "active")} disabled={taskActive} onClick={() => selectTab("purchase")}>购买卡密</button><button className={cn("tab-btn", activeTab === "query" && "active")} disabled={taskActive} onClick={() => selectTab("query")}>查询状态</button></nav>
          {activeTab === "redeem" ? <div className="step-content">{!verifiedPlan ? <><label className="field-label" htmlFor="cdkInput">输入您的充值码</label><section className="input-group"><input id="cdkInput" value={code} onChange={(event) => { codeRef.current = event.target.value; setCode(event.target.value); }} onKeyDown={(event) => { if (event.key === "Enter") void handleVerify(); }} placeholder="请输入卡密 / CDK" maxLength={32} /><button className={cn("btn-primary", busy && "is-loading")} onClick={() => void handleVerify()} disabled={busy}><span className="btn-text" style={{ display: busy ? "none" : undefined }}>兑换卡密</span><span className="loader" style={{ display: busy ? "block" : undefined }} /></button></section></> : <><div className="cdk-confirmed"><span>已验证卡密</span><code>{code}</code></div><div className="plan-confirmed"><span>套餐类型</span><strong>{planTitle(verifiedPlan)}</strong></div><div className="session-guide" role="note" aria-labelledby="session-guide-title"><div className="session-guide-copy"><strong id="session-guide-title">下一步：获取并提交 Session</strong><p>请先登录 ChatGPT，打开官方 Session 页面：</p><p className="session-guide-url"><a href={SESSION_URL} target="_blank" rel="noopener noreferrer">{SESSION_URL}</a></p><p>全选复制页面中的完整 JSON，返回本页粘贴到下方输入框，再点击确认并启动。</p></div><a href={SESSION_URL} target="_blank" rel="noopener noreferrer" className="session-guide-link">打开 Session 页面 <ExternalLink aria-hidden="true" /></a></div><label className="field-label" htmlFor="tokenInput">粘贴完整 Session JSON</label><section className="input-group"><div className="textarea-wrapper"><textarea id="tokenInput" value={session} onChange={(event) => sessionChange(event.target.value)} placeholder="粘贴从官方 Session 页面复制的完整 JSON（保留 user、expires、accessToken 等字段）" rows={5} /></div><button className={cn("btn-primary", "btn-lg", (busy || taskActive) && "is-loading")} onClick={() => void handleStart()} disabled={busy || taskActive || taskDone}><span className="btn-text">{taskButtonText}</span><span className="loader" style={{ display: busy || taskActive ? "block" : undefined }} /></button><p className="field-hint">必须点击「确认并启动」才会执行自动化；仅粘贴不会自动运行。</p><button className="btn-secondary" onClick={resetFlow} disabled={taskActive}>返回重新输入卡密</button></section></>}</div> : null}
          {activeTab === "purchase" ? <div className="purchase-section" id="plans"><label className="field-label">选择充值套餐</label><div className="purchase-plans">{plans.map((plan) => { const unavailable = planIsUnavailable(plan); return <button key={plan.code} type="button" className={cn("purchase-plan", purchasePlanCode === plan.code && "selected", unavailable && "sold-out")} aria-pressed={purchasePlanCode === plan.code} disabled={unavailable} onClick={() => choosePurchasePlan(plan.code)}><strong>{plan.name}</strong><span>{planPrice(plan)}</span><small className={cn("purchase-plan-stock", unavailable && "sold-out")}>{planStockLabel(plan)}</small><small>{plan.description}</small></button>; })}</div><label className="field-label" htmlFor="purchase-email">联系邮箱</label><input id="purchase-email" type="email" value={purchaseEmail} onChange={(event) => setPurchaseEmail(event.target.value)} placeholder="you@example.com" /><label className="field-label" htmlFor="purchase-phone">手机号</label><div className="phone-fields"><select id="purchase-phone-country" aria-label="国家或地区区号" value={purchasePhoneCountryCode} onChange={(event) => setPurchasePhoneCountryCode(event.target.value)}>{phoneCountries.map(([countryCode, label]) => <option key={countryCode} value={countryCode}>{countryCode} {label}</option>)}</select><input id="purchase-phone" type="text" inputMode="tel" value={purchasePhoneNumber} onChange={(event) => setPurchasePhoneNumber(event.target.value.replace(/[^0-9\s-]/g, ""))} placeholder="本地手机号" /></div><section className="input-group"><button className={cn("btn-primary", "btn-lg", "purchase-button", busy && "is-loading", selectedPurchasePlanSoldOut && "is-sold-out")} onClick={() => void handlePurchase()} disabled={busy || !selectedPurchasePlan || selectedPurchasePlanSoldOut}><span className="btn-text" style={{ display: busy ? "none" : undefined }}>{selectedPurchasePlanSoldOut ? (selectedPurchasePlan && planIsSoldOut(selectedPurchasePlan) ? "已售罄，补货中" : "库存未配置") : selectedPurchasePlan ? "购买并获取 CDK" : "暂无可售商品"}</span><span className="loader" style={{ display: busy ? "block" : undefined }} /></button></section>{storeOrder ? <div className="query-result-card" id="purchaseResult"><div className="query-row"><span>订单状态</span><strong>{storeOrder.status === "paid" ? "已支付" : storeOrder.status === "failed" ? "失败" : "待支付"}</strong></div>{storeOrder.cdkCode ? <div className="query-row"><span>兑换码</span><strong>{storeOrder.cdkCode}</strong></div> : null}<div className="query-row"><span>订单号</span><strong>{storeOrder.orderNo || storeOrder.id}</strong></div></div> : null}</div> : null}
          {activeTab === "query" ? <div className="query-section"><label className="field-label" htmlFor="queryInput">输入卡密查询状态</label><section className="input-group"><input id="queryInput" value={queryCode} onChange={(event) => setQueryCode(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void handleQuery(); }} placeholder="请输入要查询的激活码" maxLength={32} /><button className={cn("btn-primary", queryBusy && "is-loading")} onClick={() => void handleQuery()} disabled={queryBusy}><span className="btn-text" style={{ display: queryBusy ? "none" : undefined }}>查询状态</span><span className="loader" style={{ display: queryBusy ? "block" : undefined }} /></button></section></div> : null}
          {noticeVisible ? <div className={cn("status-message", notice?.tone === "danger" ? "status-error" : "status-success")} style={{ display: "block" }}>{notice?.text}{queryResult ? <div className="query-result-card"><div className="query-row"><span>CDK 类型</span><strong>自助激活码</strong></div><div className="query-row"><span>当前状态</span><strong className={statusClass(queryStatus)}>{statusLabel(queryStatus)}</strong></div>{queryResult.taskId ? <div className="query-row"><span>开通进度</span><strong>{queryProgress}%</strong></div> : null}{queryResult.taskId && ["queued", "running"].includes(String(queryResult.taskStatus || "")) ? <div className="task-progress-track query-task-progress"><div className="task-progress-bar" style={{ width: `${queryProgress}%` }} /></div> : null}{queryStatus === "used" ? <div className="query-row"><span>使用时间</span><strong>{String(queryResult.usedAt || "-")}</strong></div> : null}</div> : null}</div> : null}
          {task ? <section className="task-progress"><div className="task-progress-header"><span>开通进度</span><b>{task.progress}%</b></div><div className="task-progress-track"><div className={cn("task-progress-bar", (task.status === "failed" || task.status === "manual") && "error")} style={{ width: `${task.progress}%` }} /></div><p className="task-progress-text">{buyerTaskMessage(task.status)}</p></section> : null}
        </section>
        <details className="faq-panel" open><summary>常见问题 / 安全说明</summary><ul><li>Session 仅用于本次官方结账，不会保存您的账号密码。</li><li>开通失败时卡密会自动回滚，可更换账号或稍后重试。</li><li>若长时间停留在「开通中」，可在「查询状态」查看进度。</li></ul></details>
      </div>
    </main>
  );
}
