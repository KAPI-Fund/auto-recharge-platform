import type { Metadata } from "next";
import { FaqPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = {
  title: "常见问题 | KC ChatGPT",
  description: "ChatGPT 购买、充值、Session 和订单处理常见问题。",
};

export default function FaqPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><FaqPageView /></PublicTheme>;
}
