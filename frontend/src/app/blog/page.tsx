import type { Metadata } from "next";
import { BlogPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "教程与博客 | KC ChatGPT" };

export default function BlogPage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><BlogPageView /></PublicTheme>;
}
