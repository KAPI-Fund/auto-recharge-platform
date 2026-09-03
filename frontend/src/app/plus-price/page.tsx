import type { Metadata } from "next";
import { ProductPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "ChatGPT Plus 价格 | KC ChatGPT" };

export default function PlusPricePage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><ProductPageView kind="plus" /></PublicTheme>;
}
