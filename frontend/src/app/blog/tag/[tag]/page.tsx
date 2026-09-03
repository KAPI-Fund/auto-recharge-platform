import { BlogPageView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export default async function BlogTagPage({ params }: { params: Promise<{ tag: string }> }) {
  const { tag } = await params;
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><BlogPageView tag={tag} /></PublicTheme>;
}
