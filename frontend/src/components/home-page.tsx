import { PublicHomeView } from "@/components/public-reference-pages";
import { PublicTheme } from "@/components/public-theme";

export function HomePage() {
  return <PublicTheme bodyClassName="reference-page-body" legacyStylesheet={false}><PublicHomeView /></PublicTheme>;
}
