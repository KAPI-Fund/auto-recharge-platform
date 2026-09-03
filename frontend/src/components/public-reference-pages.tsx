"use client";

import Link from "next/link";
import { ArrowRight, Check, ChevronDown, ChevronRight, CircleHelp, Clock3, Code2, CreditCard, ExternalLink, FileQuestion, KeyRound, LifeBuoy, LockKeyhole, Mail, MessageCircle, Search, ShieldCheck, Sparkles, WalletCards, Zap } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { listPlans } from "@/lib/platform-api";
import type { Plan } from "@/lib/platform-types";
import { PublicSiteFooter, PublicSupportButton } from "@/components/public-site-footer";
import { PublicSiteHeader } from "@/components/public-site-header";
import { cn } from "@/lib/utils";

type PageShellProps = {
  children: React.ReactNode;
  className?: string;
};

export function PublicReferenceShell({ children, className = "" }: PageShellProps) {
  return (
    <main className={cn("reference-page", className)}>
      <PublicSiteHeader />
      {children}
      <PublicSiteFooter />
      <PublicSupportButton />
    </main>
  );
}

function ReferenceHero({ eyebrow, title, description, children }: { eyebrow: string; title: string; description: string; children?: React.ReactNode }) {
  return (
    <section className="reference-hero">
      <div className="reference-hero__inner">
        <p className="reference-overline">{eyebrow}</p>
        <h1>{title}</h1>
        <p>{description}</p>
        {children}
      </div>
    </section>
  );
}

const homeFaq = [
  ["ChatGPT Plus 代充安全吗？会影响账号吗？", "平台不要求提供账号密码，兑换时只提交当前开通所需的 Session。"],
  ["没有海外信用卡可以充值吗？支持哪些支付方式？", "平台购买方式以当前 Checkout 配置为准，支付成功后会按订单发放 CDK。"],
  ["充值流程是怎样的？需要多长时间？", "购买 CDK、提交 Session、等待 Worker 自动处理，状态可以随时查询。"],
  ["购买后没有收到卡密怎么办？", "先用订单号、联系邮箱或手机号查询最近三个月订单，再联系客服处理。"],
  ["充值失败如何处理？", "保留订单号和页面状态，不要重复提交同一张 CDK，进入帮助中心获取处理建议。"],
] as const;

const homeUseCases = [
  { icon: ShieldCheck, title: "不需要账号密码", text: "只提交官方页面生成的 Session，账号凭据不进入平台表单。" },
  { icon: Clock3, title: "分钟级处理", text: "提交后由独立 Worker 执行，页面和订单中心持续显示状态。" },
  { icon: CreditCard, title: "订单清晰可查", text: "订单、CDK 和开通状态由 Go API 统一记录，方便后续查询。" },
];

export function PublicHomeView() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [activeFlow, setActiveFlow] = useState(0);
  useEffect(() => { listPlans().then(setPlans).catch(() => undefined); }, []);
  const featuredPlans = plans.slice(0, 3);
  const flowSteps = [
    { title: "购买充值卡密", text: "选择当前商品，填写联系邮箱和手机号，完成平台 Checkout 支付。", icon: WalletCards },
    { title: "提交账号信息", text: "打开官方 Session 页面复制完整 JSON，返回充值页粘贴提交。", icon: KeyRound },
    { title: "完成账户充值", text: "点击确认并启动，Worker 自动处理，任务状态和结果可查询。", icon: Sparkles },
  ];
  const ActiveFlowIcon = flowSteps[activeFlow].icon;
  return (
    <PublicReferenceShell className="reference-home-page">
      <section className="reference-home-hero">
        <div className="reference-home-hero__inner">
          <div className="reference-home-hero__copy">
            <p className="reference-overline">CHATGPT PLUS / PRO</p>
            <h1>ChatGPT 订阅<br /><em>自动开通服务</em></h1>
            <p>无需海外信用卡，通过本站购买 CDK 并提交 Session，按官方 Checkout 流程完成 ChatGPT Plus / Pro 开通。</p>
            <div className="reference-home-hero__actions"><Link href="/recharge?tab=purchase#plans" className="reference-primary-button">立即购买 <ArrowRight aria-hidden="true" /></Link><Link href="/guide" className="reference-quiet-button">查看开通流程</Link></div>
            <div className="reference-home-proof"><span><Check aria-hidden="true" />不需要账号密码</span><span><Check aria-hidden="true" />支付后自动发 CDK</span><span><Check aria-hidden="true" />失败可联系客服</span></div>
            <a href="#tabs" className="reference-scroll-link">下滑查看充值流程 <ChevronDown aria-hidden="true" /></a>
          </div>
        </div>
      </section>

      <section className="reference-promise-strip"><div className="reference-promise-strip__inner">{homeUseCases.map((item) => <article key={item.title}><item.icon aria-hidden="true" /><div><h2>{item.title}</h2><p>{item.text}</p></div></article>)}</div></section>

      <section id="features-section" className="reference-section reference-section--features"><div className="reference-section__inner"><div className="reference-section__heading"><p className="reference-overline">WHY KC CHATGPT</p><h2>为什么选择自助开通</h2><p>把购买、发卡、兑换和自动化处理拆成清晰的步骤，每个环节都可以回到订单继续操作。</p></div><div className="reference-feature-grid"><article><span>01</span><ShieldCheck aria-hidden="true" /><h3>安全的账号交接</h3><p>不收集账号密码，只处理本次开通所需的 Session 信息。</p></article><article><span>02</span><Zap aria-hidden="true" /><h3>真实自动化执行</h3><p>提交后由独立 Worker 执行官方流程，不把业务逻辑藏在页面脚本中。</p></article><article><span>03</span><CreditCard aria-hidden="true" /><h3>支付与发卡分离</h3><p>平台支付确认后由 Go API 生成并绑定订单的 CDK。</p></article><article><span>04</span><MessageCircle aria-hidden="true" /><h3>问题可以追踪</h3><p>订单号、CDK 状态和任务结果可以从公开页面继续查询。</p></article></div></div></section>

      <section id="tabs" className="reference-section reference-section--flow"><div className="reference-section__inner"><div className="reference-section__heading reference-section__heading--center"><p className="reference-overline">HOW IT WORKS</p><h2>三步完成购买与开通</h2><p>按照当前页面的引导操作，完成后可以从订单中心继续查看。</p></div><div className="reference-flow-tabs" role="tablist" aria-label="开通流程">{flowSteps.map((step, index) => <button key={step.title} type="button" role="tab" aria-selected={activeFlow === index} className={cn(activeFlow === index && "is-active")} onClick={() => setActiveFlow(index)}><span>{index + 1}</span>{step.title}</button>)}</div><div className="reference-flow-panel"><div className="reference-flow-panel__number">0{activeFlow + 1}</div><div><p className="reference-overline">STEP 0{activeFlow + 1}</p><h3>{flowSteps[activeFlow].title}</h3><p>{flowSteps[activeFlow].text}</p></div><ActiveFlowIcon aria-hidden="true" /></div><div className="reference-plan-strip"><div><p className="reference-overline">AVAILABLE PLANS</p><h3>当前可购买商品</h3></div><div className="reference-plan-strip__items">{featuredPlans.length ? featuredPlans.map((plan) => <Link key={plan.code} href={`/recharge?tab=purchase&plan=${encodeURIComponent(plan.code)}#plans`} className="hero-price-card"><span>{plan.name}</span><strong>{plan.currency} {plan.price}</strong><ArrowRight aria-hidden="true" /></Link>) : <span className="reference-loading">正在读取商品…</span>}</div></div></div></section>

      <section id="testimonials" className="reference-section reference-section--scenarios"><div className="reference-section__inner"><div className="reference-section__heading"><p className="reference-overline">SERVICE NOTES</p><h2>把常见问题提前说清楚</h2><p>每个入口都指向真实可操作的页面，减少来回寻找和重复提交。</p></div><div className="reference-scenario-grid"><article><span className="reference-scenario-mark">A</span><h3>已经购买了 CDK</h3><p>直接进入兑换开通，验证卡密后按引导获取 Session。</p><Link href="/recharge">去兑换开通 <ArrowRight aria-hidden="true" /></Link></article><article><span className="reference-scenario-mark">B</span><h3>想找回购买记录</h3><p>使用订单号、邮箱或手机号查询最近三个月的订单。</p><Link href="/order">查询订单 <ArrowRight aria-hidden="true" /></Link></article><article><span className="reference-scenario-mark">C</span><h3>开通过程遇到问题</h3><p>先查看帮助中心和 FAQ，再带订单号联系客服处理。</p><Link href="/help">打开帮助中心 <ArrowRight aria-hidden="true" /></Link></article></div></div></section>

      <section id="faq" className="reference-section reference-section--faq"><div className="reference-section__inner"><div className="reference-section__heading reference-section__heading--center"><p className="reference-overline">FAQ</p><h2>有什么可以帮到你？</h2><p>整理了购买、充值和订单处理中的常见问题。</p></div><div className="reference-home-faq">{homeFaq.map(([question, answer]) => <details key={question}><summary>{question}<ChevronDown aria-hidden="true" /></summary><p>{answer}</p></details>)}</div><Link href="/faq" className="reference-centered-link">查看完整常见问题 <ArrowRight aria-hidden="true" /></Link></div></section>

    </PublicReferenceShell>
  );
}

const faqCategories = [
  { id: "presale", label: "售前咨询", icon: CircleHelp },
  { id: "recharge", label: "充值流程", icon: Zap },
  { id: "payment", label: "价格与支付", icon: CreditCard },
  { id: "usage", label: "账号使用", icon: ShieldCheck },
  { id: "aftersale", label: "售后支持", icon: LifeBuoy },
  { id: "codex", label: "Codex", icon: Code2 },
] as const;

const faqItems = [
  { category: "presale", question: "ChatGPT Plus 代充安全吗？会影响账号吗？", answer: "本平台不要求提供账号密码，兑换时仅提交当前开通所需的 Session。任务由系统通过官方结账流程处理，具体到账时间和可用权益以官方账户状态为准。" },
  { category: "presale", question: "什么账号都可以充值吗？", answer: "可以使用能正常登录 ChatGPT 网页的账号。请先确认目标账号所在地区、当前套餐和官方页面状态，避免把充值码提交到错误账号。" },
  { category: "presale", question: "ChatGPT Plus 和 Pro 有什么区别，哪个更适合我？", answer: "Plus 更适合日常对话、写作和常规开发；Pro 适合高频推理、长上下文和更高用量场景。可在购买页查看当前平台发布的具体商品。" },
  { category: "presale", question: "企业或团队可以批量代充吗？可以开具发票吗？", answer: "当前公开流程面向个人自助开通。企业批量需求请先联系客服确认服务范围和收款凭证安排，不要直接提交多份 Session。" },
  { category: "presale", question: "代充和自己开信用卡充值有什么区别？", answer: "平台负责订单收款、CDK 发放和自动化开通，你只需要在自己的浏览器会话中复制 Session。官方账户规则、地区限制和付款结果仍由官方系统决定。" },
  { category: "recharge", question: "购买的充值卡密有效期多久？暂时不使用可以吗？", answer: "卡密生成后会绑定到订单并处于未使用状态。建议在准备好目标账号 Session 后再兑换，具体有效期以后台商品配置和订单页面提示为准。" },
  { category: "recharge", question: "充值流程是怎样的？需要多长时间？", answer: "购买并完成支付后获得 CDK，验证 CDK 后打开官方 Session 页面复制完整 JSON，返回兑换页提交，Worker 会执行开通并持续回传状态。" },
  { category: "recharge", question: "充值需要提供账号密码吗？", answer: "不需要。不要在任何地方提交账号密码；本流程只使用你从官方页面复制的 Session，提交后由后台加密保存并在任务完成后按策略清理。" },
  { category: "recharge", question: "充值完成后在哪里确认权益？", answer: "可以回到 ChatGPT 官方账户页面确认套餐，也可以在本平台的订单查询和兑换状态页查看订单、CDK 和任务进度。" },
  { category: "recharge", question: "充值失败是什么原因？怎么解决？", answer: "可能与官方结账地区、账号状态、支付验证、代理或 Session 有关。页面只展示面向用户的处理结果，遇到失败请保留订单号并联系客服。" },
  { category: "payment", question: "支持哪些支付方式？", answer: "平台支付方式以当前 Checkout 页面和后台配置为准。调试环境可能直接发放 CDK，正式环境以平台 Stripe 支付回调确认结果为准。" },
  { category: "payment", question: "付款后没收到卡密怎么办？", answer: "先使用订单号、下单邮箱或手机号在订单查询页查询最近三个月订单。支付成功后 CDK 会与订单绑定；如果仍未显示，请联系客服提供订单号。" },
  { category: "payment", question: "充值失败可以退款吗？", answer: "平台会依据订单和任务的最终状态处理后续动作。请勿重复提交同一张 CDK，保留订单号和失败时间联系人工处理。" },
  { category: "usage", question: "我已经有 Plus 了，可以提前续费吗？", answer: "是否可以续费由官方账户状态和当前商品规则决定。请先在官方账户确认当前套餐，再选择平台支持的商品。" },
  { category: "usage", question: "Plus 到期后会自动续费扣款吗？", answer: "本平台不会保存你的官方支付方式，也不会替你设置官方自动续费。是否自动续费请在 ChatGPT 官方账户中查看。" },
  { category: "usage", question: "充值后可以和别人共用账号吗？", answer: "请遵守 ChatGPT 官方服务条款和账号安全规则。平台只负责当前订单的开通处理，不建议共享账号或 Session。" },
  { category: "usage", question: "Plus 包含哪些功能？", answer: "具体模型、额度和功能以官方账号当前页面为准。购买页的商品说明只代表平台配置，不替代官方服务说明。" },
  { category: "aftersale", question: "充值完还是提示升级到 Plus，没有生效？", answer: "先刷新官方页面并确认登录的是提交 Session 对应的账号。若仍未生效，请在订单查询页找到订单号并联系客服。" },
  { category: "aftersale", question: "如何联系客服？响应时间是多久？", answer: "打开帮助中心查看当前支持方式，并准备订单号、发生时间和页面状态。不要发送账号密码、完整 Session 或银行卡信息。" },
  { category: "codex", question: "OpenAI Codex 是什么？需要 ChatGPT Plus 才能用吗？", answer: "Codex 是面向开发工作的工具能力，是否可用取决于官方账号套餐、地区与当前服务政策。详情请以官方说明为准。" },
  { category: "codex", question: "Codex 消息额度是多少？Plus 和 Pro 额度有什么区别？", answer: "额度会随官方产品和账户状态变化，本平台不对官方额度做静态承诺。可进入 Codex 说明页查看当前整理信息。" },
  { category: "codex", question: "Codex 和 Cursor、Claude Code 有什么区别？", answer: "它们是不同产品和工作流。建议根据代码库规模、模型偏好、工具集成和预算选择，不要仅按套餐名称比较。" },
] as const;

export function FaqPageView() {
  const [category, setCategory] = useState<(typeof faqCategories)[number]["id"]>("presale");
  const [search, setSearch] = useState("");
  const [openQuestion, setOpenQuestion] = useState<string>(faqItems[0].question);
  useEffect(() => {
    const applyCategoryHash = () => {
      const hash = window.location.hash.replace(/^#/, "");
      if (faqCategories.some((item) => item.id === hash)) {
        setCategory(hash as (typeof faqCategories)[number]["id"]);
        setOpenQuestion("");
      }
    };
    applyCategoryHash();
    window.addEventListener("hashchange", applyCategoryHash);
    return () => window.removeEventListener("hashchange", applyCategoryHash);
  }, []);
  const normalizedSearch = search.trim().toLowerCase();
  const visibleItems = useMemo(() => faqItems.filter((item) => item.category === category && (!normalizedSearch || `${item.question} ${item.answer}`.toLowerCase().includes(normalizedSearch))), [category, normalizedSearch]);

  return (
    <PublicReferenceShell className="faq-reference-page">
      <ReferenceHero eyebrow="FAQ / SUPPORT" title="有什么可以帮到你？" description="整理了购买、充值和账号使用过程中常见的问题，找不到答案可以直接进入帮助中心。">
        <div className="reference-search"><Search aria-hidden="true" /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索问题，例如：退款、到账、支付方式…" aria-label="搜索常见问题" /></div>
      </ReferenceHero>
      <div className="faq-reference-shell">
        <Link href="/help" className="faq-help-callout"><LifeBuoy aria-hidden="true" /><span><strong>已经购买？遇到充值失败、账号登录或使用限制问题？</strong><small>点击前往帮助中心，快速定位常见问题</small></span><ChevronRight aria-hidden="true" /></Link>
        <div className="faq-reference-layout">
          <aside className="faq-category-nav" aria-label="问题分类">
            <h2>问题分类</h2>
            {faqCategories.map((item) => {
              const count = faqItems.filter((faq) => faq.category === item.id).length;
              return <button id={item.id} key={item.id} type="button" className={cn(category === item.id && "is-active")} onClick={() => { setCategory(item.id); setOpenQuestion(""); }}><item.icon aria-hidden="true" /><span>{item.label}</span><small>{count}</small></button>;
            })}
            <div className="faq-category-note"><strong>没有找到答案？</strong><p>准备订单号后进入帮助中心，客服会根据订单状态协助处理。</p></div>
          </aside>
          <section className="faq-reference-list" aria-live="polite">
            <div className="faq-reference-list__heading"><div><p className="reference-overline">{faqCategories.find((item) => item.id === category)?.label}</p><h2>{faqCategories.find((item) => item.id === category)?.label}</h2></div><span>{visibleItems.length} 个问题</span></div>
            {visibleItems.length ? visibleItems.map((item) => <article key={item.question} className={cn("faq-reference-item", openQuestion === item.question && "is-open")}><button type="button" onClick={() => setOpenQuestion((current) => current === item.question ? "" : item.question)} aria-expanded={openQuestion === item.question}><span>{item.question}</span><ChevronDown aria-hidden="true" /></button>{openQuestion === item.question ? <div className="faq-reference-answer">{item.answer}</div> : null}</article>) : <div className="faq-empty"><Search aria-hidden="true" /><p>这个分类没有匹配的问题。</p><button type="button" onClick={() => setSearch("")}>清空搜索</button></div>}
          </section>
        </div>
      </div>
    </PublicReferenceShell>
  );
}

const guideSections = [
  { id: "overview", label: "流程概览" },
  { id: "flow-preview", label: "操作示意" },
  { id: "step1", label: "第 1 步：购买卡密" },
  { id: "step2", label: "第 2 步：提交账号信息" },
  { id: "step3", label: "第 3 步：完成充值" },
  { id: "guide-faq", label: "常见问题" },
  { id: "support", label: "客服支持" },
] as const;

const SESSION_URL = "https://chatgpt.com/api/auth/session";

function GuideSessionLink() {
  return <div className="guide-session-link-card" role="note"><span className="guide-session-link-card__icon"><KeyRound aria-hidden="true" /></span><div className="guide-session-link-card__copy"><strong>官方 Session 获取地址</strong><p>先登录自己的 ChatGPT 账号，再打开下面的官方地址。</p><code>{SESSION_URL}</code></div><a href={SESSION_URL} target="_blank" rel="noopener noreferrer" className="guide-session-link-card__action">打开地址 <ExternalLink aria-hidden="true" /></a></div>;
}

function GuideFlowVisual({ kind }: { kind: "purchase" | "session" | "recharge" }) {
  if (kind === "purchase") {
    return <div className="guide-flow-visual" aria-label="本站购买流程示意"><div className="guide-flow-visual__top"><span className="guide-flow-visual__brand"><span>KC</span>KC ChatGPT</span><span className="guide-flow-visual__label">本站流程示意</span></div><div className="guide-flow-visual__body"><p className="guide-flow-visual__overline">PURCHASE</p><h3>选择商品并完成支付</h3><div className="guide-flow-visual__rows"><div><span>商品套餐</span><strong>选择当前可售商品</strong></div><div><span>联系邮箱</span><strong>用于接收订单通知</strong></div><div><span>手机号</span><strong>选择国家区号后填写</strong></div></div><div className="guide-flow-visual__button">继续支付 <ArrowRight aria-hidden="true" /></div></div></div>;
  }
  if (kind === "session") {
    return <div className="guide-flow-visual" aria-label="官方 Session 提交流程示意"><div className="guide-flow-visual__top"><span className="guide-flow-visual__brand"><span><KeyRound aria-hidden="true" /></span>Session</span><span className="guide-flow-visual__label">官方地址</span></div><div className="guide-flow-visual__body"><p className="guide-flow-visual__overline">SESSION</p><h3>复制完整 JSON，返回本站提交</h3><div className="guide-flow-visual__url"><span>GET</span><code>chatgpt.com/api/auth/session</code><Check aria-hidden="true" /></div><p className="guide-flow-visual__hint">保留 user、expires、accessToken 等字段，不要提交账号密码。</p></div></div>;
  }
  return <div className="guide-flow-visual" aria-label="本站充值任务流程示意"><div className="guide-flow-visual__top"><span className="guide-flow-visual__brand"><span><Sparkles aria-hidden="true" /></span>自动化任务</span><span className="guide-flow-visual__label">状态可查询</span></div><div className="guide-flow-visual__body"><p className="guide-flow-visual__overline">PROCESSING</p><h3>提交后等待任务完成</h3><div className="guide-progress-line"><span /><b>任务已提交</b></div><div className="guide-flow-visual__status-list"><span><i />排队中</span><span><i />开通中</span><span><i />已完成</span></div><p className="guide-flow-visual__hint">可在兑换页面或订单查询页查看处理状态。</p></div></div>;
}

export function GuidePageView() {
  return (
    <PublicReferenceShell className="guide-reference-page">
      <div className="guide-reference-shell">
        <aside className="guide-toc"><h2>目录导航</h2><nav>{guideSections.map((item) => <a key={item.id} href={`#${item.id}`}>{item.label}</a>)}</nav></aside>
        <article className="guide-article">
          <header className="guide-article__head"><h1>ChatGPT 充值卡密代充指引</h1><span className="guide-time-badge"><span />只需 3 步 · 约 2 分钟完成</span></header>
          <section id="overview" className="guide-overview"><div className="guide-overview-card"><b>1</b><WalletCards aria-hidden="true" /><h2>购买卡密</h2><p>首页下单并完成支付，获得 CDK</p></div><div className="guide-overview-card"><b>2</b><KeyRound aria-hidden="true" /><h2>提交账号信息</h2><p>复制 Session，粘贴到充值页</p></div><div className="guide-overview-card"><b>3</b><Sparkles aria-hidden="true" /><h2>完成充值</h2><p>提交后自动处理，等待到账</p></div></section>
          <Link href="/recharge?tab=purchase" className="guide-primary-cta">立即开始充值 <ArrowRight aria-hidden="true" /></Link>
          <section id="flow-preview" className="guide-flow-section"><div className="guide-flow-section__heading"><p className="reference-overline">HOW IT WORKS</p><h2>三步操作示意</h2><p>下面是本站流程示意，不使用第三方网站的视频或截图。</p></div><div className="guide-flow-grid"><GuideFlowVisual kind="purchase" /><GuideFlowVisual kind="session" /><GuideFlowVisual kind="recharge" /></div></section>
          <section id="step1" className="guide-step-section"><div className="guide-section-title"><span>1</span><div><p className="reference-overline">STEP 01</p><h2>购买充值卡密</h2><p>在购买页选择商品、填写联系邮箱和手机号，完成平台 Checkout 支付。</p></div></div><GuideFlowVisual kind="purchase" /></section>
          <section id="step2" className="guide-step-section"><div className="guide-section-title"><span>2</span><div><p className="reference-overline">STEP 02</p><h2>提交账号信息</h2><p>登录自己的 ChatGPT 账号，打开官方 Session 页面，复制完整 JSON 后返回充值页粘贴。</p></div></div><div className="guide-safety-note"><LockKeyhole aria-hidden="true" /><span><strong>不需要账号密码</strong><small>只提交当前开通所需 Session；不要把密码、银行卡或其他敏感信息粘贴到输入框。</small></span></div><GuideSessionLink /><GuideFlowVisual kind="session" /></section>
          <section id="step3" className="guide-step-section"><div className="guide-section-title"><span>3</span><div><p className="reference-overline">STEP 03</p><h2>完成充值</h2><p>点击确认并启动后，后台 Worker 会执行自动化开通。可以在页面或订单查询页查看状态。</p></div></div><GuideFlowVisual kind="recharge" /></section>
          <section id="guide-faq" className="guide-mini-faq"><h2>常见问题</h2><Link href="/faq">查看完整 FAQ <ArrowRight aria-hidden="true" /></Link><p>如果任务长时间没有变化，请保留订单号和状态截图，进入帮助中心联系客服。</p></section>
          <section id="support" className="guide-support"><LifeBuoy aria-hidden="true" /><div><h2>需要帮助？</h2><p>准备订单号后进入帮助中心，我们会根据实际订单状态协助处理。</p></div><Link href="/help">打开帮助中心 <ArrowRight aria-hidden="true" /></Link></section>
        </article>
      </div>
    </PublicReferenceShell>
  );
}

const productData = {
  plus: { name: "ChatGPT Plus", eyebrow: "CHATGPT PLUS", title: "日常工作与学习的稳定选择", description: "选择当前平台发布的 Plus 商品，完成支付后获得 CDK，再使用自己的 Session 完成自动化开通。", accent: "teal", features: ["适合日常对话、写作与学习", "购买后自动生成一次性 CDK", "订单和 Worker 处理状态可查询"] },
  pro: { name: "ChatGPT Pro", eyebrow: "CHATGPT PRO", title: "为高频创作与开发提供更多余量", description: "面向高频使用和专业工作流的 ChatGPT 订阅入口，实际商品和价格以平台实时配置为准。", accent: "blue", features: ["适合高频推理与长时间工作", "通过平台统一订单完成购买", "遇到异常可以按订单号追踪"] },
  gemini: { name: "Gemini", eyebrow: "GEMINI", title: "把常用 AI 工具集中到一个入口", description: "Gemini 服务页面用于承载产品说明和后续服务入口。当前平台主流程仍以 ChatGPT 商品为准。", accent: "violet", features: ["独立的产品说明入口", "服务开通状态以页面提示为准", "可从顶部导航返回 ChatGPT 商品"] },
  grok: { name: "Grok", eyebrow: "GROK", title: "探索更多 AI 订阅工具", description: "Grok 产品信息页用于统一站点导航和服务说明，当前公开购买能力以后台已发布商品为准。", accent: "orange", features: ["统一的产品信息布局", "不要求提交账号密码", "支持从帮助中心获得服务说明"] },
  codex: { name: "OpenAI Codex", eyebrow: "CODEX", title: "面向开发者的 AI 工作流", description: "Codex 说明页整理常见使用问题和额度概念。具体能力、额度和可用性以官方账号与服务政策为准。", accent: "indigo", features: ["适合代码阅读与开发协作", "额度和模型以官方页面为准", "在 FAQ 查看常见 Codex 问题"] },
} as const;

type ProductKey = keyof typeof productData;

function ProductIcon({ kind }: { kind: ProductKey }) {
  if (kind === "codex") return <Code2 aria-hidden="true" />;
  if (kind === "gemini") return <Sparkles aria-hidden="true" />;
  if (kind === "grok") return <Zap aria-hidden="true" />;
  return <MessageCircle aria-hidden="true" />;
}

export function ProductPageView({ kind }: { kind: ProductKey }) {
  const data = productData[kind];
  const [plans, setPlans] = useState<Plan[]>([]);
  useEffect(() => { listPlans().then(setPlans).catch(() => undefined); }, []);
  const productPlan = kind === "plus" ? plans.find((plan) => plan.code === "plus") : kind === "pro" ? plans.find((plan) => plan.code === "pro_5x") || plans.find((plan) => plan.code === "pro_20x") : undefined;
  return (
    <PublicReferenceShell className={`product-reference-page product-reference-page--${data.accent}`}>
      <section className="product-hero"><div className="product-hero__inner"><div className="product-hero__copy"><p className="reference-overline">{data.eyebrow}</p><h1>{data.name}<br /><em>{data.title}</em></h1><p>{data.description}</p><div className="product-hero__actions"><Link href={kind === "plus" || kind === "pro" ? `/recharge?tab=purchase${productPlan ? `&plan=${encodeURIComponent(productPlan.code)}` : ""}#plans` : "/recharge?tab=purchase#plans"} className="reference-primary-button">{kind === "plus" || kind === "pro" ? "立即购买" : "查看当前套餐"}<ArrowRight aria-hidden="true" /></Link><Link href="/guide" className="reference-quiet-button">查看开通教程</Link></div><div className="product-proof-row"><span><Check aria-hidden="true" />不需要账号密码</span><span><Check aria-hidden="true" />订单可查询</span><span><Check aria-hidden="true" />失败可联系客服</span></div></div><div className="product-hero__visual"><div className="product-icon"><ProductIcon kind={kind} /></div><div className="product-status"><span className="product-status__dot" />当前页面已接入站点导航</div><div className="product-visual-lines"><span /><span /><span /></div><div className="product-visual-caption">{productPlan ? `${productPlan.name} · ${productPlan.currency} ${productPlan.price}` : "以后台已发布商品为准"}</div></div></div></section>
      <section className="product-content"><div className="product-content__heading"><p className="reference-overline">WHY THIS SERVICE</p><h2>清晰的购买与开通路径</h2><p>从产品说明进入真实的购买、CDK 兑换和订单查询流程，页面之间保持统一导航。</p></div><div className="product-feature-grid">{data.features.map((feature, index) => <article key={feature}><span>0{index + 1}</span><Check aria-hidden="true" /><h3>{feature}</h3><p>{index === 0 ? "根据当前商品和官方账户状态完成服务选择。" : index === 1 ? "平台支付成功后由 Go API 生成并绑定订单的 CDK。" : "出现异常时可从订单号、邮箱或手机号回到处理链路。"}</p></article>)}</div><div className="product-next-step"><div><p className="reference-overline">NEXT STEP</p><h2>准备好开始了吗？</h2><p>查看当前可售商品，或先阅读完整教程了解 Session 提交流程。</p></div><div><Link href="/recharge?tab=purchase#plans" className="reference-primary-button">查看套餐 <ArrowRight aria-hidden="true" /></Link><Link href="/order" className="reference-quiet-button">查询订单</Link></div></div></section>
    </PublicReferenceShell>
  );
}

const blogPosts = [
  { slug: "chatgpt-recharge-flow", date: "2026-08-20", time: "8 min read", title: "ChatGPT Plus 充值完整流程：从购买 CDK 到自动开通", description: "整理购买、Session 提交、Worker 处理和订单查询的完整步骤，适合第一次使用平台的用户。", tags: ["ChatGPT", "Plus", "充值教程"] },
  { slug: "session-safety-guide", date: "2026-08-18", time: "6 min read", title: "Session 是什么？如何安全完成一次自动化开通", description: "解释为什么流程不需要账号密码，以及如何从官方页面复制完整 Session JSON。", tags: ["Session", "账号安全", "教程"] },
  { slug: "order-status-guide", date: "2026-08-12", time: "5 min read", title: "订单查询与 CDK 状态说明", description: "订单支付、CDK 发放、兑换和 Worker 处理状态如何对应，遇到问题如何准备信息。", tags: ["订单", "CDK", "帮助"] },
  { slug: "codex-workflow", date: "2026-08-06", time: "7 min read", title: "Codex 工作流与 ChatGPT 订阅的关系", description: "从开发者角度了解 Codex 使用场景、额度和官方账户状态之间的关系。", tags: ["Codex", "开发者", "AI"] },
] as const;

export function BlogPageView({ tag }: { tag?: string } = {}) {
  const [search, setSearch] = useState("");
  const normalizedTag = tag?.trim().toLowerCase() || "";
  const tagPosts = normalizedTag ? blogPosts.filter((post) => post.tags.some((postTag) => postTag.toLowerCase() === normalizedTag)) : blogPosts;
  const filtered = tagPosts.filter((post) => `${post.title} ${post.description} ${post.tags.join(" ")}`.toLowerCase().includes(search.trim().toLowerCase()));
  return <PublicReferenceShell className="blog-reference-page"><ReferenceHero eyebrow={tag ? `BLOG TAG / ${tag}` : "BLOG / GUIDE"} title={tag ? `标签：${tag}` : "教程与博客"} description="了解 ChatGPT 充值、Session 使用和订单处理，把每一步都做得清楚。"><div className="reference-search"><Search aria-hidden="true" /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索教程、订单、Session…" aria-label="搜索博客" /></div></ReferenceHero><section className="blog-reference-shell"><div className="blog-reference-heading"><div><p className="reference-overline">LATEST NOTES</p><h2>{tag ? `#${tag}` : "精选更新"}</h2></div><Link href="/guide">查看充值教程 <ArrowRight aria-hidden="true" /></Link></div><div className="blog-grid">{filtered.map((post) => <article className="blog-card" key={post.slug}><div className="blog-card__meta"><span>{post.date}</span><span>•</span><span>{post.time}</span></div><h3>{post.title}</h3><p>{post.description}</p><div className="blog-card__tags">{post.tags.map((postTag) => <Link key={postTag} href={`/blog/tag/${encodeURIComponent(postTag)}`}>#{postTag}</Link>)}</div><Link href={`/blog/${post.slug}`}>阅读全文 <ArrowRight aria-hidden="true" /></Link></article>)}</div>{!filtered.length ? <div className="faq-empty"><Search aria-hidden="true" /><p>{tag ? `没有找到标签“${tag}”下的文章。` : "没有匹配的文章。"}</p><button type="button" onClick={() => setSearch("")}>清空搜索</button></div> : null}</section></PublicReferenceShell>;
}

export function BlogArticleView({ slug }: { slug: string }) {
  const post = blogPosts.find((item) => item.slug === slug) || blogPosts[0];
  return <PublicReferenceShell className="blog-article-page"><article className="blog-article"><Link href="/blog" className="back-reference-link"><ChevronRight aria-hidden="true" />教程与博客</Link><p className="reference-overline">{post.date} · {post.time}</p><h1>{post.title}</h1><p className="blog-article__lead">{post.description}</p><div className="blog-article__body"><h2>先确认你的目标</h2><p>使用平台服务前，请确认目标账号可以正常登录 ChatGPT 官方页面，并准备好本次开通所需的 Session。平台购买和开通是两个步骤，订单完成后再进入兑换页。</p><h2>按三步完成</h2><ol><li><strong>购买卡密：</strong>进入购买页面选择当前可售商品，填写联系邮箱和国家地区手机号，完成平台支付。</li><li><strong>提交 Session：</strong>打开官方 Session 页面，全选复制完整 JSON，返回兑换页粘贴到输入框。</li><li><strong>等待处理：</strong>点击确认并启动，Worker 会执行自动化任务，订单查询页可查看已购买订单。</li></ol><div className="article-callout"><ShieldCheck aria-hidden="true" /><span>平台不会要求你提供账号密码。不要把 Session、银行卡号或其他敏感信息发送给客服。</span></div><h2>遇到问题怎么办</h2><p>请通过订单号、下单邮箱或手机号查询最近三个月订单，记录当前状态后进入帮助中心。不要重复提交同一张 CDK，以免造成状态冲突。</p></div><div className="blog-article__actions"><Link href="/recharge?tab=purchase#plans" className="reference-primary-button">开始购买 <ArrowRight aria-hidden="true" /></Link><Link href="/faq" className="reference-quiet-button">查看常见问题</Link></div></article></PublicReferenceShell>;
}

export function HelpPageView() {
  const [query, setQuery] = useState("");
  const [submitted, setSubmitted] = useState(false);
  return <PublicReferenceShell className="help-reference-page"><ReferenceHero eyebrow="HELP CENTER" title="遇到问题？先从这里开始" description="按订单状态和常见现象自助排查。联系客服时只需要订单号和页面提示，不要发送密码或完整 Session。" /><section className="help-reference-shell"><div className="help-search-card"><Search aria-hidden="true" /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入现象，例如：没有收到卡密、开通失败…" aria-label="搜索帮助" /><button type="button" onClick={() => setSubmitted(true)}>搜索帮助</button></div>{submitted ? <div className="help-search-result" role="status"><strong>{query.trim() ? `正在为“${query.trim()}”匹配处理建议` : "请输入需要排查的现象"}</strong><p>请先查看订单状态；如果仍无法解决，准备订单号后联系客服。</p></div> : null}<div className="help-card-grid"><Link href="/order" className="help-card"><ClipboardIcon /><span><strong>先查询订单</strong><small>使用订单号、邮箱或手机号查找最近三个月订单。</small></span><ArrowRight aria-hidden="true" /></Link><Link href="/guide#step2" className="help-card"><LockKeyhole aria-hidden="true" /><span><strong>Session 提交问题</strong><small>查看如何从官方页面复制完整 JSON 并粘贴回充值页。</small></span><ArrowRight aria-hidden="true" /></Link><Link href="/faq#aftersale" className="help-card"><LifeBuoy aria-hidden="true" /><span><strong>充值失败处理</strong><small>了解失败状态、订单信息和联系客服时需要准备的内容。</small></span><ArrowRight aria-hidden="true" /></Link></div><div className="help-contact-card"><Mail aria-hidden="true" /><div><h2>仍然需要人工协助？</h2><p>请提供订单号、发生时间和页面显示的用户提示。客服不会要求你提供账号密码。</p></div><Link href="/faq#aftersale">查看支持说明 <ArrowRight aria-hidden="true" /></Link></div></section></PublicReferenceShell>;
}

function ClipboardIcon() {
  return <FileQuestion aria-hidden="true" />;
}

export function SimplePolicyPageView({ kind }: { kind: "about" | "privacy" | "terms" }) {
  const content = {
    about: { eyebrow: "ABOUT US", title: "关于 KC ChatGPT", description: "一个围绕 ChatGPT 商品购买、CDK 兑换和自动化开通搭建的独立第三方服务平台。", heading: "把每一步做得更清楚", paragraphs: ["我们提供公开的商品、订单、CDK 和开通状态页面，让用户知道自己买了什么、当前进行到哪一步。", "平台支付、订单生成、CDK 发放和 Worker 执行由不同服务负责，发生问题时可以沿着订单号和状态回到正确的处理环节。", "本站为独立第三方服务，与 OpenAI 无隶属关系。具体功能、价格和服务可用性以平台实时配置及官方政策为准。"] },
    privacy: { eyebrow: "PRIVACY", title: "隐私政策", description: "说明平台在购买、订单查询和自动化开通过程中如何处理必要的信息。", heading: "只处理完成服务所需的信息", paragraphs: ["购买订单会记录联系邮箱、国家地区区号、手机号、商品、金额和订单状态，用于支付确认、发送 CDK 和订单查询。", "兑换开通时提交的 Session 会由后端加密保存，并只用于对应任务。请不要提交账号密码、银行卡完整信息或与本次服务无关的内容。", "运行日志和任务记录用于故障排查。对外公开页面会隐藏内部 trace、原始错误、Worker 细节和敏感字段。"] },
    terms: { eyebrow: "TERMS", title: "服务条款", description: "使用平台购买、兑换和查询服务前，请阅读以下基本约定。", heading: "使用服务前请确认", paragraphs: ["你应当对提交开通的 ChatGPT 账号拥有合法使用权，并遵守 ChatGPT、Stripe 以及所在地适用的法律法规和服务条款。", "平台只按照订单和商品配置提供自动化处理，不承诺突破官方地区、账号、支付或风控限制。遇到官方流程失败时，请通过订单号联系客服。", "请妥善保管订单号、CDK 和 Session。不要与他人共享，也不要在公开渠道发布。"] },
  }[kind];
  return <PublicReferenceShell className="policy-reference-page"><ReferenceHero eyebrow={content.eyebrow} title={content.title} description={content.description} /><article className="policy-article"><p className="reference-overline">KC CHATGPT</p><h2>{content.heading}</h2>{content.paragraphs.map((paragraph) => <p key={paragraph}>{paragraph}</p>)}<div className="policy-article__links"><Link href="/faq">查看常见问题 <ArrowRight aria-hidden="true" /></Link><Link href="/help">帮助中心 <ArrowRight aria-hidden="true" /></Link></div></article></PublicReferenceShell>;
}
