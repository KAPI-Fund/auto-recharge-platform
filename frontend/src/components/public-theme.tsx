import type { ReactNode } from "react";
import { PublicThemeBody } from "@/components/public-theme-body";

export function PublicTheme({ bodyClassName, children, additionalStylesheets = [], legacyStylesheet = true }: { bodyClassName: string; children: ReactNode; additionalStylesheets?: string[]; legacyStylesheet?: boolean }) {
  // The reference stylesheet is loaded per public route so it cannot affect the admin console.
  // eslint-disable-next-line @next/next/no-css-tags
  return <>{legacyStylesheet ? <link rel="stylesheet" href="/style.css" data-public-theme="legacy" /> : null}{additionalStylesheets.map((href) => <link key={href} rel="stylesheet" href={href} data-public-theme="additional" />)}{children}<PublicThemeBody bodyClassName={bodyClassName} /></>;
}
