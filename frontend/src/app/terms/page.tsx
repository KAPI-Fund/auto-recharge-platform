import type { Metadata } from "next";
import { SimplePolicyPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "服务条款 | KC ChatGPT" };

export default function TermsPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><SimplePolicyPageView kind="terms" /></PublicTheme>;
}
