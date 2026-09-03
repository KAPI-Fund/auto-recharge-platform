import type { Metadata } from "next";
import { SimplePolicyPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "隐私政策 | KC ChatGPT" };

export default function PrivacyPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><SimplePolicyPageView kind="privacy" /></PublicTheme>;
}
