import type { Metadata } from "next";
import { OrderQueryView } from "@/components/views/order-query-view";

export const metadata: Metadata = {
  title: "订单查询 | KC GPT自动充值系统",
};

export default function OrdersPage() {
  return <OrderQueryView />;
}
