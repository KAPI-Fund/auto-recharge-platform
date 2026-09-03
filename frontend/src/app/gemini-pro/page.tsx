import type { Metadata } from "next";
import { ProductPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "Gemini 服务 | KC ChatGPT" };

export default function GeminiPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><ProductPageView kind="gemini" /></PublicTheme>;
}
