import type { Metadata } from "next";
import { SimplePolicyPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "关于我们 | KC ChatGPT" };

export default function AboutPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><SimplePolicyPageView kind="about" /></PublicTheme>;
}
