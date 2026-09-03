export { metadata } from "@/app/page";

import { PublicTheme } from "@/components/public-theme";
import { RechargeView } from "@/components/views/recharge-view";

type RechargeSearchParams = Promise<Record<string, string | string[] | undefined>>;

export default async function RechargePage({ searchParams }: { searchParams: RechargeSearchParams }) {
  const params = await searchParams;
  const tab = params.tab === "purchase" || params.tab === "query" ? params.tab : "redeem";
  const plan = typeof params.plan === "string" ? params.plan : "";
  return <PublicTheme bodyClassName="recharge-page" legacyStylesheet={false}><RechargeView initialTab={tab} initialPlanCode={plan} /></PublicTheme>;
}
