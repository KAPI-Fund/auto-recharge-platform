import type { Metadata } from "next";
import { GuidePageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = {
  title: "充值教程 | KC ChatGPT",
  description: "ChatGPT 充值卡密购买、Session 提交和自动开通完整教程。",
};

export default function GuidePage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><GuidePageView /></PublicTheme>;
}
