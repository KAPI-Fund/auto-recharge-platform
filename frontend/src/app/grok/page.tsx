import type { Metadata } from "next";
import { ProductPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "Grok 服务 | KC ChatGPT" };

export default function GrokPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><ProductPageView kind="grok" /></PublicTheme>;
}
