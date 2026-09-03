import Link from "next/link";
import { ArrowUpRight } from "lucide-react";

export function PublicSiteFooter() {
  return (
    <footer className="reference-footer">
      <div className="reference-footer__inner">
        <div className="reference-footer__brand">
          <Link href="/" className="public-brand">
            <span className="public-brand__mark"><img src="/favicon.svg" alt="" /></span>
            <span className="public-brand__text"><strong>KC ChatGPT</strong><small>自动充值平台</small></span>
          </Link>
          <p>独立的 ChatGPT 自助开通服务，平台购买、订单查询与自动化开通均由本站系统处理。</p>
        </div>

        <div className="reference-footer__column">
          <h2>ChatGPT 服务</h2>
          <Link href="/chatgpt-plus">ChatGPT Plus 代充</Link>
          <Link href="/chatgpt-pro">ChatGPT Pro 升级</Link>
          <Link href="/plus-price">套餐价格</Link>
          <Link href="/recharge?tab=purchase">购买卡密</Link>
        </div>
        <div className="reference-footer__column">
          <h2>帮助中心</h2>
          <Link href="/guide">充值教程</Link>
          <Link href="/faq">常见问题</Link>
          <Link href="/help">失败排查</Link>
          <Link href="/order">查询订单</Link>
        </div>
        <div className="reference-footer__column">
          <h2>其他</h2>
          <Link href="/blog">教程与博客</Link>
          <Link href="/about">关于我们</Link>
          <Link href="/privacy">隐私政策</Link>
          <Link href="/terms">服务条款</Link>
        </div>

        <div className="reference-footer__bottom">
          <span>本站为独立第三方服务平台，与 OpenAI 无隶属关系。</span>
          <span>© 2026 KC ChatGPT</span>
        </div>
      </div>
    </footer>
  );
}

export function PublicSupportButton() {
  return (
    <Link href="/help" className="reference-support-button" aria-label="打开帮助中心">
      <span className="reference-support-button__dot" />
      <span>帮助中心</span>
      <ArrowUpRight aria-hidden="true" />
    </Link>
  );
}
