import type { Metadata } from "next";
import { HelpPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "帮助中心 | KC ChatGPT" };

export default function HelpPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><HelpPageView /></PublicTheme>;
}
