import type { Metadata } from "next";
import { ProductPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "OpenAI Codex | KC ChatGPT" };

export default function CodexPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><ProductPageView kind="codex" /></PublicTheme>;
}
