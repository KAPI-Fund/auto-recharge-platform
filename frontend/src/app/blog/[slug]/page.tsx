import type { Metadata } from "next";
import { BlogArticleView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export const metadata: Metadata = { title: "文章 | KC ChatGPT" };

export default async function BlogArticlePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><BlogArticleView slug={slug} /></PublicTheme>;
}
