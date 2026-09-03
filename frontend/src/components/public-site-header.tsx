"use client";

import Link from "next/link";
import { ArrowRight, BookOpen, ClipboardList, FileQuestion, Home, Menu, ShoppingCart, X } from "lucide-react";
import { usePathname } from "next/navigation";
import { useState } from "react";

const links = [
  { href: "/", label: "首页", icon: Home },
  { href: "/faq", label: "常见问题", icon: FileQuestion },
  { href: "/guide", label: "教程", icon: BookOpen },
] as const;

function isCurrent(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  if (href === "/order") return pathname === "/order" || pathname === "/orders";
  if (href === "/gptpro") return pathname === "/gptpro" || pathname === "/chatgpt-pro";
  return pathname === href;
}

export function PublicSiteHeader() {
  const pathname = usePathname();
  const [menuOpen, setMenuOpen] = useState(false);

  function closeMenu() {
    setMenuOpen(false);
  }

  return (
    <header className="public-site-header">
      <div className="public-announcement">
        <span className="public-announcement__dot" />
        <span>平台支持 ChatGPT Plus / Pro 自助开通，购买 CDK 后按教程提交 Session。</span>
        <Link href="/guide">查看使用指南 <ArrowRight aria-hidden="true" /></Link>
      </div>
      <div className="public-site-header__inner">
        <Link href="/" className="public-brand" onClick={closeMenu}>
          <span className="public-brand__mark"><img src="/favicon.svg" alt="" /></span>
          <span className="public-brand__text"><strong>KC ChatGPT</strong><small>自动充值平台</small></span>
        </Link>

        <nav className="public-header-nav" aria-label="前台导航">
          {links.map((link) => (
            <Link key={link.href} href={link.href} className={isCurrent(pathname, link.href) ? "public-header-nav__link is-current" : "public-header-nav__link"}>
              <link.icon aria-hidden="true" />{link.label}
            </Link>
          ))}
        </nav>

        <div className="public-header-actions">
          <Link href="/order" className={isCurrent(pathname, "/order") ? "public-header-link is-current" : "public-header-link"}>
            <ClipboardList aria-hidden="true" />查询订单
          </Link>
          <Link href="/recharge?tab=purchase" className="public-header-cta">
            <ShoppingCart aria-hidden="true" />立即购买<ArrowRight aria-hidden="true" />
          </Link>
        </div>

        <button type="button" className="public-menu-toggle" aria-label={menuOpen ? "关闭菜单" : "打开菜单"} aria-expanded={menuOpen} onClick={() => setMenuOpen((open) => !open)}>
          {menuOpen ? <X aria-hidden="true" /> : <Menu aria-hidden="true" />}
        </button>
      </div>

      {menuOpen ? (
        <nav className="public-mobile-menu" aria-label="移动端导航">
          {links.map((link) => (
            <Link key={link.href} href={link.href} onClick={closeMenu}><link.icon aria-hidden="true" />{link.label}</Link>
          ))}
          <Link href="/order" onClick={closeMenu}><ClipboardList aria-hidden="true" />查询订单</Link>
          <Link href="/recharge?tab=purchase" className="public-mobile-menu__primary" onClick={closeMenu}><ShoppingCart aria-hidden="true" />立即购买<ArrowRight aria-hidden="true" /></Link>
        </nav>
      ) : null}
    </header>
  );
}
