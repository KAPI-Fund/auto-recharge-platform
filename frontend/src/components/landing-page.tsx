"use client";

import Link from "next/link";
import { ArrowRight, Check, CircleHelp, ClipboardList, CreditCard, KeyRound, MessageCircle, RefreshCw, ShieldCheck, Sparkles, WalletCards } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { PublicSiteHeader } from "@/components/public-site-header";
import { listPlans } from "@/lib/platform-api";
import type { Plan } from "@/lib/platform-types";

const planHighlights: Record<string, { eyebrow: string; title: string; accent: string; bullets: string[] }> = {
  plus: {
    eyebrow: "日常首选",
    title: "轻量使用，快速开通",
    accent: "green",
    bullets: ["适合日常对话和写作", "标准套餐权益", "购买后自动发放 CDK"],
  },
  pro_5x: {
    eyebrow: "高用量",
    title: "给创作者更多余量",
    accent: "blue",
    bullets: ["更高的消息和推理额度", "适合开发与内容创作", "自动化任务可追踪"],
  },
  pro_20x: {
    eyebrow: "旗舰方案",
    title: "重度工作负载",
    accent: "orange",
    bullets: ["面向团队和高频使用", "优先处理开通任务", "后台可查询完整状态"],
  },
};

function priceText(plan: Plan) {
  if (plan.currency === "CNY") {
    return new Intl.NumberFormat("zh-CN", { style: "currency", currency: "CNY", maximumFractionDigits: 2 }).format(plan.price);
  }
  return new Intl.NumberFormat("en-US", { style: "currency", currency: plan.currency || "USD", maximumFractionDigits: 2 }).format(plan.price);
}

function planCopy(plan: Plan) {
  return planHighlights[plan.code] || {
    eyebrow: "可购买商品",
    title: "按商品配置完成开通",
    accent: "slate",
    bullets: ["商品权益以后台配置为准", "支付完成自动生成 CDK", "订单可按订单号查询"],
  };
}

function planIsSoldOut(plan: Plan) {
  if (typeof plan.soldOut === "boolean") return plan.soldOut;
  return Boolean((plan.saleLimit || 0) > 0 && (plan.soldCount || 0) >= (plan.saleLimit || 0));
}

function planStockLabel(plan: Plan) {
  if (planIsSoldOut(plan)) return "已售罄，补货中";
  if ((plan.saleLimit || 0) > 0) return `剩余 ${Math.max(0, plan.remainingQuantity ?? (plan.saleLimit || 0) - (plan.soldCount || 0))} 件`;
  return "不限量供应";
}

export function LandingPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");

  useEffect(() => {
    let active = true;
    listPlans()
      .then((items) => {
        if (!active) return;
        setPlans(items);
        setLoadError("");
      })
      .catch((error) => {
        if (!active) return;
        setLoadError(error instanceof Error ? error.message : "暂时无法读取平台商品");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, []);

  const featuredPlans = useMemo(() => {
    const preferred = plans.filter((plan) => ["plus", "pro_5x", "pro_20x"].includes(plan.code));
    const other = plans.filter((plan) => !["plus", "pro_5x", "pro_20x"].includes(plan.code));
    return [...preferred, ...other].slice(0, 6);
  }, [plans]);
  const heroPlans = featuredPlans.slice(0, 3);

  return (
    <main className="landing-page">
      <PublicSiteHeader />

      <section className="landing-hero">
        <div className="landing-hero__inner">
          <div className="landing-hero__content">
            <p className="landing-kicker"><span className="landing-kicker__dot" />公开套餐 · 透明下单</p>
            <h1>选择你的 ChatGPT 套餐，<em>立即开通</em></h1>
            <p className="landing-hero__lead">公开展示真实套餐与价格，完成平台 Checkout 支付后自动获得 CDK。再使用自己的 Session 完成开通，订单和处理进度全程可查询。</p>
            <div className="landing-hero__actions">
              <a href="#plans" className="landing-button landing-button--primary">查看套餐与价格<ArrowRight aria-hidden="true" /></a>
              <Link href="/orders" className="landing-button landing-button--quiet"><ClipboardList aria-hidden="true" />查询已有订单</Link>
            </div>
            <div className="landing-proof-row" aria-label="服务特点">
              <span><Check aria-hidden="true" />无需账号密码</span>
              <span><Check aria-hidden="true" />支付后自动发 CDK</span>
              <span><Check aria-hidden="true" />订单状态可查</span>
            </div>
          </div>

          <div className="hero-pricing-preview" aria-label="热门套餐速览">
            <div className="hero-pricing-preview__head">
              <div><span>热门套餐</span><strong>按需选择，透明购买</strong></div>
              <small>价格来自平台实时配置</small>
            </div>
            <div className="hero-pricing-preview__list">
              {loading ? <div className="hero-pricing-preview__loading"><span className="landing-spinner" />正在读取套餐</div> : heroPlans.map((plan, index) => {
                const copy = planCopy(plan);
                const soldOut = planIsSoldOut(plan);
                const card = <>
                  <div className="hero-price-card__top"><span>{index === 0 ? "最受欢迎" : copy.eyebrow}</span><strong>{priceText(plan)}</strong></div>
                  <h2>{plan.name}</h2>
                  <p>{plan.description || copy.title}</p>
                  <small className={`hero-price-card__stock ${soldOut ? "is-sold-out" : ""}`}>{planStockLabel(plan)}</small>
                  <span className={`hero-price-card__link ${soldOut ? "is-sold-out" : ""}`}>{soldOut ? "已售罄，补货中" : <>查看方案 <ArrowRight aria-hidden="true" /></>}</span>
                </>;
                return soldOut
                  ? <div key={plan.code} className={`hero-price-card hero-price-card--${copy.accent} ${index === 0 ? "is-featured" : ""} is-sold-out`}>{card}</div>
                  : <Link key={plan.code} href={`/recharge?tab=purchase&plan=${encodeURIComponent(plan.code)}`} className={`hero-price-card hero-price-card--${copy.accent} ${index === 0 ? "is-featured" : ""}`}>{card}</Link>;
              })}
            </div>
            <div className="hero-pricing-preview__foot"><span><ShieldCheck aria-hidden="true" />支付后自动发放 CDK</span><a href="#plans">查看全部套餐<ArrowRight aria-hidden="true" /></a></div>
          </div>
        </div>
      </section>

      <section className="landing-trust-strip"><div className="landing-section__inner landing-trust-strip__inner"><div><ShieldCheck aria-hidden="true" /><span><strong>自己的账号</strong><small>只提交本次开通所需 Session</small></span></div><div><CreditCard aria-hidden="true" /><span><strong>平台支付</strong><small>Stripe 支付链路可追踪</small></span></div><div><RefreshCw aria-hidden="true" /><span><strong>自动处理</strong><small>Worker 执行后回传状态与日志</small></span></div><div><MessageCircle aria-hidden="true" /><span><strong>问题可排查</strong><small>订单状态与处理进度可查</small></span></div></div></section>

      <section id="plans" className="landing-section landing-section--plans">
        <div className="landing-section__heading"><div><p className="landing-overline">PLANS & PRICING</p><h2>选择适合你的套餐</h2><p>商品名称、价格与可售状态均来自后台配置，购买后由 Go API 创建订单并发放 CDK。</p></div><Link href="/recharge?tab=purchase" className="landing-text-link">查看全部套餐<ArrowRight aria-hidden="true" /></Link></div>
        {loadError ? <div className="landing-inline-error"><CircleHelp aria-hidden="true" /><span>{loadError}</span><button type="button" onClick={() => window.location.reload()}>重新加载</button></div> : null}
        {loading ? <div className="landing-loading"><span className="landing-spinner" />正在读取可购买商品</div> : null}
        {!loading && !plans.length && !loadError ? <div className="landing-empty"><CircleHelp aria-hidden="true" /><p>当前暂无可购买商品，请稍后再试。</p></div> : null}
        <div className="landing-plans-grid">
          {featuredPlans.map((plan, index) => {
            const copy = planCopy(plan);
            return <article key={plan.code} className={`landing-plan-card landing-plan-card--${copy.accent} ${index === 1 ? "is-featured" : ""}`}>
              <div className="landing-plan-card__top"><span>{copy.eyebrow}</span>{index === 1 ? <b>推荐</b> : null}</div>
              <h3>{plan.name}</h3><p className="landing-plan-card__description">{plan.description || copy.title}</p>
              <div className="landing-plan-card__price"><strong>{priceText(plan)}</strong><span>平台价</span></div>
              <ul>{copy.bullets.map((bullet) => <li key={bullet}><Check aria-hidden="true" />{bullet}</li>)}</ul>
              {planIsSoldOut(plan) ? <span className="landing-plan-card__cta is-sold-out">已售罄，补货中</span> : <Link href={`/recharge?tab=purchase&plan=${encodeURIComponent(plan.code)}`} className="landing-plan-card__cta">购买此商品<ArrowRight aria-hidden="true" /></Link>}
            </article>;
          })}
        </div>
        {plans.length > featuredPlans.length ? <p className="landing-section-note">还有 {plans.length - featuredPlans.length} 个后台已发布商品，可在购买页面查看。</p> : null}
      </section>

      <section id="flow" className="landing-section landing-section--tint"><div className="landing-section__inner"><div className="landing-section__heading landing-section__heading--center"><div><p className="landing-overline">HOW IT WORKS</p><h2>三步完成购买与开通</h2><p>购买链路和兑换链路分开，支付、发卡、Worker 执行状态各自可追踪。</p></div></div><div className="landing-flow-grid"><div className="landing-flow-step"><span>01</span><WalletCards aria-hidden="true" /><h3>选择商品并支付</h3><p>填写联系邮箱和国家地区手机号，Go API 创建订单并拉起平台 Stripe Checkout。</p></div><div className="landing-flow-step"><span>02</span><KeyRound aria-hidden="true" /><h3>订单完成后领取 CDK</h3><p>支付回调确认成功后，系统生成属于本订单的 CDK，可在订单查询页找回。</p></div><div className="landing-flow-step"><span>03</span><Sparkles aria-hidden="true" /><h3>粘贴 Session 启动任务</h3><p>在兑换页验证 CDK，提交自己的 Session，Worker 自动执行并回写任务进度。</p></div></div></div></section>

      <section className="landing-section landing-section--actions"><div className="landing-section__heading"><div><p className="landing-overline">SELF SERVICE</p><h2>已经有订单或 CDK？</h2><p>不需要重新走首页流程，直接进入对应的操作页面。</p></div></div><div className="landing-action-grid"><Link href="/recharge" className="landing-action-card"><span className="landing-action-card__icon landing-action-card__icon--blue"><KeyRound aria-hidden="true" /></span><span><strong>CDK 兑换开通</strong><small>验证兑换码，提交 Session，查看 Worker 进度</small></span><ArrowRight aria-hidden="true" /></Link><Link href="/orders" className="landing-action-card"><span className="landing-action-card__icon landing-action-card__icon--green"><ClipboardList aria-hidden="true" /></span><span><strong>查询购买订单</strong><small>支持订单号、邮箱或手机号，范围为最近 3 个月</small></span><ArrowRight aria-hidden="true" /></Link></div></section>

      <section id="faq" className="landing-section landing-section--faq"><div className="landing-section__heading landing-section__heading--center"><div><p className="landing-overline">FAQ</p><h2>常见问题</h2><p>公开说明以当前平台真实处理方式为准。</p></div></div><div className="landing-faq-list"><details><summary>购买后什么时候能拿到 CDK？<span>+</span></summary><p>平台支付成功并收到 Stripe 回调后，Go API 会生成并关联 CDK。调试模式下会直接模拟支付并发放 CDK，正式模式仍以支付回调为准。</p></details><details><summary>订单可以怎么查询？<span>+</span></summary><p>进入订单查询页，可使用订单号、下单时的联系邮箱，或国家地区区号加手机号查询最近 3 个月订单。</p></details><details><summary>兑换 CDK 需要提供账号密码吗？<span>+</span></summary><p>不需要提交账号密码。兑换时只需要验证 CDK，并粘贴本次开通所需的 Session JSON，任务执行过程会返回状态和处理进度。</p></details><details><summary>开通失败后如何处理？<span>+</span></summary><p>任务失败会保留失败状态与错误信息；CDK 是否回滚由 Go 后端业务状态决定，用户可以根据提示重试或联系管理员处理。</p></details></div></section>

      <footer className="public-site-footer"><div className="public-site-footer__inner"><div className="public-brand public-brand--footer"><span className="public-brand__mark"><img src="/favicon.svg" alt="" /></span><span className="public-brand__text"><strong>KC ChatGPT</strong><small>自动充值平台</small></span></div><p>独立的第三方自动充值平台，商品、订单和自动化任务均由本站系统处理。</p><nav><Link href="/recharge">兑换开通</Link><Link href="/orders">订单查询</Link><Link href="/subscription">订阅查询</Link></nav><small className="public-site-footer__copyright">© 2026 KC ChatGPT. 服务状态与价格以平台实时数据为准。</small></div></footer>
    </main>
  );
}
