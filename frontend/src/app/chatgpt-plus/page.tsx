import type { Metadata } from "next";
import { ProductPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "ChatGPT Plus 代充 | KC ChatGPT" };

export default function ChatGPTPlusPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><ProductPageView kind="plus" /></PublicTheme>;
}
