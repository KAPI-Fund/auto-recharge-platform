import type { Metadata } from "next";
import { PublicTheme } from "@/components/public-theme";
import { SubscriptionView } from "@/components/views/subscription-view";

export const metadata: Metadata = {
  title: "发票助手 | AI 自助充值中心",
};

export default function SubscriptionPage() {
  return (
    <PublicTheme bodyClassName="recharge-page subscription-page">
      <SubscriptionView />
    </PublicTheme>
  );
}
