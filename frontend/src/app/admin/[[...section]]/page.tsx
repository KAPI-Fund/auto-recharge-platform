import {
  AdminConsoleView,
  type AdminSection,
} from "@/components/views/admin-console-view";

const sectionByPath: Record<string, AdminSection> = {
  settings: "config",
  config: "config",
  proxies: "proxies",
  browser_pool: "browser_pool",
  tax_addresses: "tax_addresses",
  checkout_debug: "checkout_debug",
  cards: "cards",
  cdks: "cdks",
  store_products: "store_products",
  pool_emails: "pool_emails",
  phones: "phones",
  products: "products",
  sessions: "sessions",
  cancel_renewal: "cancel_renewal",
  logs: "logs",
  automation_tasks: "automation_tasks",
  billing: "billing",
  runtime_logs: "runtime_logs",
  admin_login_logs: "admin_login_logs",
};

export default async function AdminPage({
  params,
}: {
  params: Promise<{ section?: string[] }>;
}) {
  const { section = [] } = await params;
  const key = section[0] || "overview";
  const selected = sectionByPath[key] || "overview";
  return <AdminConsoleView section={selected} />;
}
