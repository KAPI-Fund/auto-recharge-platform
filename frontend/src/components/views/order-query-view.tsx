"use client";

import { useState } from "react";
import { Clipboard, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PublicSiteHeader } from "@/components/public-site-header";
import { queryStoreOrders } from "@/lib/platform-api";
import type { StoreOrder } from "@/lib/platform-types";
import { cn, formatDate, formatPrice } from "@/lib/utils";

function statusLabel(status: StoreOrder["status"]) {
  return status === "paid" ? "已支付" : status === "failed" ? "失败" : "待支付";
}

function statusClass(status: StoreOrder["status"]) {
  return status === "paid" ? "is-paid" : status === "failed" ? "is-failed" : "is-pending";
}

async function copyText(value: string) {
  const text = String(value || "").trim();
  if (!text) throw new Error("兑换码为空");

  if (navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // HTTP 页面或浏览器剪贴板权限受限时，继续使用用户手势内的降级方案。
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.top = "-9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);
  try {
    if (!document.execCommand("copy")) throw new Error("复制失败");
  } finally {
    textarea.remove();
  }
}

export function OrderQueryView() {
  const [query, setQuery] = useState("");
  const [orders, setOrders] = useState<StoreOrder[]>([]);
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(false);
  const [copyingOrderId, setCopyingOrderId] = useState("");
  const [copiedOrderId, setCopiedOrderId] = useState("");
  const [copyNotice, setCopyNotice] = useState("");

  async function search() {
    const value = query.trim();
    if (!value) {
      setOrders([]);
      setNotice("请输入订单号、邮箱或手机号");
      return;
    }

    setNotice("");
    setLoading(true);
    try {
      const result = await queryStoreOrders({ query: value });
      setOrders(result);
      if (!result.length) setNotice("最近 3 个月没有找到匹配订单");
    } catch (error) {
      setOrders([]);
      setNotice(error instanceof Error ? error.message : "查询失败");
    } finally {
      setLoading(false);
    }
  }

  async function handleCopy(orderId: string, code: string) {
    setCopyingOrderId(orderId);
    setCopyNotice("");
    try {
      await copyText(code);
      setCopiedOrderId(orderId);
      setCopyNotice("兑换码已复制");
      window.setTimeout(() => {
        setCopiedOrderId((current) => current === orderId ? "" : current);
      }, 1800);
    } catch {
      setCopyNotice("复制失败，请手动复制兑换码");
    } finally {
      setCopyingOrderId("");
    }
  }

  return (
    <main className="public-utility-page orders-page">
      <PublicSiteHeader />
      <section className="orders-hero">
        <div className="orders-hero__inner">
          <p className="orders-overline">ORDER CENTER</p>
          <h1>订单中心</h1>
          <p>查询、处理并跟踪你的 ChatGPT 订阅订单</p>
        </div>
      </section>

      <div className="orders-shell">
        <form className="orders-query-card" onSubmit={(event) => { event.preventDefault(); void search(); }}>
          <label htmlFor="order-query">下单时填写的联系方式或订单号</label>
          <div className="orders-query-row">
            <input
              id="order-query"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="请输入订单号、邮箱或手机号"
              autoComplete="off"
            />
            <Button type="submit" variant="dark" className="orders-query-button" disabled={loading}>
              <Search aria-hidden="true" />
              {loading ? "查询中..." : "查询订单"}
            </Button>
          </div>
          <p className="orders-query-hint">支持订单号、邮箱或手机号，可查询最近 3 个月的订单</p>
          <p className="orders-retention">订单数据仅展示最近 3 个月</p>
        </form>

        {notice ? <p className="orders-notice" role="status">{notice}</p> : null}
        {copyNotice ? <p className="orders-notice orders-notice--copy" role="status">{copyNotice}</p> : null}

        {orders.length ? (
          <section className="orders-results" aria-live="polite">
            <div className="orders-results__heading">
              <h2>查询结果</h2>
              <span>最近 3 个月</span>
            </div>
            <div className="orders-results__list">
              {orders.map((order) => (
                <article key={order.id} className="orders-result-card">
                  <div className="orders-result-card__top">
                    <div>
                      <p className="orders-result-card__number">{order.orderNo}</p>
                      <p className="orders-result-card__meta">{order.planName} · {formatPrice(order.amount, order.currency)} · {formatDate(order.createdAt)}</p>
                    </div>
                    <span className={cn("orders-status", statusClass(order.status))}>{statusLabel(order.status)}</span>
                  </div>
                  {order.cdkCode ? (
                    <div className="orders-cdk-row">
                      <code>{order.cdkCode}</code>
                      <button
                        type="button"
                        title={copiedOrderId === order.id ? "已复制兑换码" : "复制兑换码"}
                        aria-label={copiedOrderId === order.id ? "已复制兑换码" : "复制兑换码"}
                        disabled={copyingOrderId === order.id}
                        onClick={() => { void handleCopy(order.id, order.cdkCode); }}
                      >
                        <Clipboard aria-hidden="true" />
                      </button>
                    </div>
                  ) : null}
                </article>
              ))}
            </div>
          </section>
        ) : (
          <section className="orders-guide" aria-label="查询说明">
            <article><strong>01</strong><h2>找到订单</h2><p>使用下单时填写的联系方式或订单号查询。</p></article>
            <article><strong>02</strong><h2>优先处理</h2><p>待支付、待激活与处理中订单会优先显示。</p></article>
            <article><strong>03</strong><h2>继续操作</h2><p>按订单状态支付、激活或查看处理进度。</p></article>
          </section>
        )}
      </div>
    </main>
  );
}
