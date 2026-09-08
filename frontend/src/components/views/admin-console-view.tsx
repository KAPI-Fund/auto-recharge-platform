"use client";

import {
  Activity,
  Bot,
  BadgeCheck,
  BadgePlus,
  Check,
  CheckCircle2,
  CheckCheck,
  CheckSquare,
  ChevronDown,
  CircleX,
  Clipboard,
  Copy,
  Cpu,
  CreditCard,
  Database,
  Download,
  FileText,
  Globe,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  Layers,
  LogOut,
  Mail,
  MemoryStick,
  MapPin,
  MonitorDot,
  PackageCheck,
  PackagePlus,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Save,
  Search,
  Send,
  Settings,
  Sparkles,
  ShieldOff,
  ShieldCheck,
  SquareCheck,
  Shuffle,
  Smartphone,
  Terminal,
  Timer,
  Tickets,
  Trash2,
  TriangleAlert,
  Upload,
  Wallet,
  Receipt,
  X,
  Zap,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ElementType,
  type FormEvent,
  type MouseEvent,
} from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  addProxies,
  adminToken,
  batchRenewalStatus,
  cancelRenewal,
  changeSessionRenewal,
  changeAdminPassword,
  createProviderCard,
  clearAddresses,
  clearAuthTokens,
  clearRuntimeLogs,
  confirmTOTP,
  createAddress,
  deleteCDKs,
  deleteAddress,
  deleteBilling,
  deleteCard,
  deleteCDK,
  deleteFailedBilling,
  deleteProxy,
  deleteTaskLog,
  disableTOTP,
  downloadBilling,
  downloadAdminMedia,
  downloadSession,
  enableRenewal,
  generateAddresses,
  generateCDKs,
  generateCheckout,
  getAddresses,
  getAdminData,
  getAdminSession,
  getBilling,
  getBillingSummary,
  getBrowserPool,
  getCards,
  getCardActivity,
  getCardPools,
  getCDKs,
  getCheckoutPlans,
  getCheckoutStatus,
  getConfig,
  getGPTConfig,
  getGPTStatus,
  getHcaptchaConfig,
  getHcaptchaLogs,
  getKimooxCardBINs,
  getLoginLogs,
  getPhones,
  getPoolEmails,
  getProducts,
  getProxies,
  getPublicAdminPaths,
  getRegion,
  getRuntimeLogs,
  getSecurityStatus,
  getSession,
  getSessions,
  getTaskLogs,
  getTelegramConfig,
  importCardPool,
  importCDKs,
  loginAdmin,
  reloadBrowserPool,
  save2FAMode,
  saveAdminPaths,
  saveConfig,
  saveGPTConfig,
  saveHcaptchaConfig,
  saveRegion,
  saveTelegramConfig,
  sendEmailTest,
  sendTelegramCode,
  setAuthTokens,
  setBrowserPoolMode,
  setupTOTP,
  shipCDKs,
  testGPTConfig,
  testHcaptcha,
  testAllProxies,
  testProxy,
  testTelegram,
  triggerActivation,
  toggleProxy,
  updateAddress,
  verifyAdmin2FA,
} from "@/lib/legacy-api";
import type { JsonMap } from "@/lib/legacy-api";
import { getStoreProducts, saveStoreProduct, toggleStoreProductPublished } from "@/lib/platform-api";
import { cn } from "@/lib/utils";
import {
  PhonePoolPanel,
  PoolEmailsPanel,
  ProductPoolPanel,
} from "@/components/views/pool-assets-panels";

export type AdminSection =
  | "overview"
  | "config"
  | "settings"
  | "proxies"
  | "browser_pool"
  | "tax_addresses"
  | "checkout_debug"
  | "cards"
  | "cdks"
  | "store_products"
  | "pool_emails"
  | "phones"
  | "products"
  | "sessions"
  | "cancel_renewal"
  | "logs"
  | "automation_tasks"
  | "billing"
  | "runtime_logs"
  | "admin_login_logs";

type Row = Record<string, unknown>;
type AuthPhase = "checking" | "login" | "2fa" | "authorized";
type ConfirmAction = (message: string, title?: string) => Promise<boolean>;
type ProviderConfigKey =
  | "LOCAL_TEXT"
  | "AIRWALLEX"
  | "STRIPE_ISSUING"
  | "PHOTONPAY"
  | "DOGPAY"
  | "KIMOOX";

// The current admin UI is intentionally scoped to Kimoox while the other
// adapters remain registered in the Go backend for backward compatibility.
const visibleCardProviders = ["LOCAL_TEXT", "KIMOOX"] as const;
const visibleCardProvider = "KIMOOX" as const;

function creatableProviderOptions(providerRows: Row[]) {
  return providerRows.filter((provider) => bool(provider, "enabled") && bool(provider, "canCreate"));
}

const inputClass =
  "asset-input mt-2 h-10 w-full rounded-md border border-slate-200 bg-slate-50 px-3 text-sm text-slate-900 outline-none transition focus:border-cyan-400 focus:bg-white";
const areaClass =
  "import-textarea mt-2 w-full resize-y rounded-md border border-slate-200 bg-slate-50 p-3 text-sm text-slate-900 outline-none transition focus:border-cyan-400 focus:bg-white";

const executionConfigKeys = [
  "max_concurrent_activations",
  "recharge_queued_timeout_seconds",
  "recharge_task_lease_timeout_seconds",
  "worker_log_level",
  "maintenance_mode",
  "checkout_mode",
] as const;

const providerConfigKeys = [
  "card_pool_routing",
  "card_pool_default_provider",
  "card_pool_card_creation_mode",
  "card_pool_cancel_after_payment",
  "card_provider_local_text_enabled",
  "card_provider_airwallex_enabled",
  "card_provider_stripe_issuing_enabled",
  "card_provider_photonpay_enabled",
  "card_provider_dogpay_enabled",
  "card_provider_kimoox_enabled",
  "airwallex_base_url",
  "airwallex_client_id",
  "airwallex_api_key",
  "airwallex_cardholder_id",
  "airwallex_primary_currency",
  "airwallex_card_type",
  "airwallex_card_purpose",
  "airwallex_created_by",
  "airwallex_form_factor",
  "airwallex_webhook_tolerance_seconds",
  "airwallex_activate_on_issue",
  "airwallex_webhook_secret",
  "stripe_issuing_base_url",
  "stripe_issuing_secret_key",
  "stripe_issuing_cardholder_id",
  "stripe_issuing_currency",
  "stripe_issuing_webhook_secret",
  "stripe_issuing_webhook_tolerance_seconds",
  "photonpay_base_url",
  "photonpay_app_id",
  "photonpay_app_secret",
  "photonpay_private_key",
  "photonpay_webhook_public_key",
  "photonpay_card_bin",
  "photonpay_card_type",
  "photonpay_card_form_factor",
  "photonpay_card_scheme",
  "photonpay_cardholder_id",
  "photonpay_account_id",
  "photonpay_member_id",
  "photonpay_matrix_account",
  "photonpay_nickname",
  "photonpay_primary_currency",
  "photonpay_transaction_limit_type",
  "photonpay_webhook_tolerance_seconds",
  "dogpay_base_url",
  "dogpay_appid",
  "dogpay_secret",
  "dogpay_private_key",
  "dogpay_webhook_secret",
  "dogpay_channel_id",
  "dogpay_cardholder_id",
  "dogpay_entity_id",
  "dogpay_card_type",
  "dogpay_budget_id",
  "dogpay_velocity_amount_limit",
  "dogpay_webhook_tolerance_seconds",
  "kimoox_base_url",
  "kimoox_api_key",
  "kimoox_api_secret",
  "kimoox_webhook_secret",
  "kimoox_card_bin_ids",
  "kimoox_card_type",
  "kimoox_prepaid_amount_mode",
  "kimoox_prepaid_recharge_amount",
  "kimoox_cardholder_id",
  "kimoox_holder_id",
  "kimoox_card_group_id",
  "kimoox_budget_id",
  "kimoox_webhook_tolerance_seconds",
  "kimoox_apply_poll_attempts",
  "kimoox_apply_poll_interval_seconds",
] as const;

const emailConfigKeys = [
  "emailEnabled",
  "emailNotifyPurchase",
  "emailNotifyRedeem",
  "emailSiteName",
  "emailSMTPHost",
  "emailSMTPPort",
  "emailSMTPUsername",
  "emailSMTPPassword",
  "emailSMTPFrom",
  "emailSMTPFromName",
  "emailSMTPUseTLS",
  "emailSMTPTimeoutSeconds",
] as const;

const storePaymentConfigKeys = [
  "storeDebugMode",
  "stripeSecretKey",
  "stripeWebhookSecret",
  "stripeSuccessURL",
  "stripeCancelURL",
] as const;

const navItems: Array<{
  key: Exclude<AdminSection, "settings">;
  label: string;
  icon: ElementType;
  path: string;
}> = [
  { key: "overview", label: "概览中心", icon: LayoutDashboard, path: "/admin" },
  {
    key: "config",
    label: "系统配置",
    icon: Settings,
    path: "/admin/settings",
  },
  { key: "proxies", label: "代理池", icon: Globe, path: "/admin/proxies" },
  {
    key: "browser_pool",
    label: "浏览器池",
    icon: MonitorDot,
    path: "/admin/browser_pool",
  },
  {
    key: "tax_addresses",
    label: "免税地址",
    icon: MapPin,
    path: "/admin/tax_addresses",
  },
  {
    key: "checkout_debug",
    label: "支付链接调试",
    icon: CreditCard,
    path: "/admin/checkout_debug",
  },
  { key: "cards", label: "银行卡池", icon: CreditCard, path: "/admin/cards" },
  { key: "cdks", label: "CDK 管理", icon: Tickets, path: "/admin/cdks" },
  { key: "store_products", label: "售卡商品", icon: PackagePlus, path: "/admin/store_products" },
  {
    key: "pool_emails",
    label: "邮箱池",
    icon: Mail,
    path: "/admin/pool_emails",
  },
  {
    key: "phones",
    label: "手机号池",
    icon: Smartphone,
    path: "/admin/phones",
  },
  {
    key: "products",
    label: "成品号库",
    icon: PackageCheck,
    path: "/admin/products",
  },
  {
    key: "sessions",
    label: "Session 管理",
    icon: KeyRound,
    path: "/admin/sessions",
  },
  { key: "billing", label: "账单记录", icon: Database, path: "/admin/billing" },
  {
    key: "cancel_renewal",
    label: "续费管理",
    icon: CircleX,
    path: "/admin/cancel_renewal",
  },
  { key: "logs", label: "任务管理", icon: FileText, path: "/admin/logs" },
  {
    key: "automation_tasks",
    label: "自动化任务",
    icon: Bot,
    path: "/admin/automation_tasks",
  },
  {
    key: "runtime_logs",
    label: "运行日志",
    icon: Terminal,
    path: "/admin/runtime_logs",
  },
  {
    key: "admin_login_logs",
    label: "登录日志",
    icon: ShieldCheck,
    path: "/admin/admin_login_logs",
  },
];

const businessSections: Array<Exclude<AdminSection, "settings">> = [
  "checkout_debug",
  "cards",
  "cdks",
  "store_products",
  "sessions",
  "billing",
];

type NavigableAdminSection = Exclude<AdminSection, "settings">;

const sectionByPath: Record<string, NavigableAdminSection> = {
  admin: "overview",
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

function detectPublicAdminBase(pathname: string) {
  const [segment] = pathname.split("/").filter(Boolean);
  return segment && segment !== "admin" && segment !== "admin-login"
    ? `/${segment}`
    : "";
}

function sectionFromPathname(
  pathname: string,
  publicAdminBase: string,
): NavigableAdminSection {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  const internalPath = publicAdminBase &&
    (normalized === publicAdminBase || normalized.startsWith(`${publicAdminBase}/`))
    ? `/admin${normalized.slice(publicAdminBase.length)}`
    : normalized;
  const [, section = "admin"] = internalPath.split("/").filter(Boolean);
  return sectionByPath[section] || "overview";
}

function asRow(value: unknown): Row {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Row)
    : {};
}

function camelKey(key: string) {
  return key.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase());
}

function rowValue(row: Row | undefined, key: string) {
  if (!row) return undefined;
  return row[key] ?? row[camelKey(key)];
}

function rows(value: unknown): Row[] {
  return Array.isArray(value)
    ? (value.filter((item): item is Row =>
        Boolean(item && typeof item === "object"),
      ) as Row[])
    : [];
}

function mediaPathList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string")
    .map((item) => item.trim())
    .filter(Boolean);
}

function text(row: Row | undefined, key: string, fallback = "-") {
  const value = rowValue(row, key);
  if (value === null || value === undefined || value === "") return fallback;
  if (typeof value === "string") return value;
  if (typeof value === "object") {
    try {
      return JSON.stringify(value);
    } catch {
      return fallback;
    }
  }
  return String(value);
}

function editableConfigValue(row: Row | undefined, key: string) {
  const value = rowValue(row, key);
  if (value === null || value === undefined || value === "") return "";
  const stringValue = String(value);
  return /^[-.·•]+$/.test(stringValue) ? "" : stringValue;
}

function num(row: Row | undefined, key: string, fallback = 0) {
  const value = Number(rowValue(row, key));
  return Number.isFinite(value) ? value : fallback;
}

function bool(row: Row | undefined, key: string) {
  const value = rowValue(row, key);
  return value === true || value === 1 || value === "1" || value === "true";
}

function child(row: Row | undefined, key: string) {
  return asRow(rowValue(row, key));
}

function sessionPayload(row: Row | undefined): string {
  const nested = child(row, "session");
  const raw =
    rowValue(nested, "session_payload") ??
    rowValue(row, "session_payload") ??
    rowValue(row, "session");
  // Go returns the decrypted payload as a canonical string. Do not parse or
  // reshape Session data in the browser.
  return typeof raw === "string" ? raw : "";
}

type BadgeTone = "neutral" | "success" | "warning" | "danger" | "info";

function backendTone(row: Row | undefined, key: string, fallback: BadgeTone = "neutral"): BadgeTone {
  const tone = text(row, key, fallback);
  return ["neutral", "success", "warning", "danger", "info"].includes(tone)
    ? (tone as BadgeTone)
    : fallback;
}

function backendStatusTone(row: Row | undefined, fallback: BadgeTone = "neutral"): BadgeTone {
  return backendTone(row, "status_tone", fallback);
}

function LegacyTaskStatusBadge({ row }: { row: Row }) {
  const tone = backendStatusTone(row);
  const className = tone === "success"
    ? "status-success"
    : tone === "danger"
      ? "status-failed"
      : tone === "warning"
        ? "status-warning"
        : tone === "info"
          ? "status-running"
          : "";
  return <span className={cn("status-badge", className)}>{text(row, "status_label", "-")}</span>;
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : "请求失败，请稍后重试";
}

async function resolveAdminPanelPath() {
  let panelPath = "/admin";
  try {
    const paths = asRow(await getPublicAdminPaths());
    panelPath = text(paths, "panelUrl", `/${text(paths, "panelPath", panelPath)}`);
  } catch {
    // Keep the configured local fallback when the path lookup is temporarily unavailable.
  }
  return panelPath.startsWith("/") ? panelPath : `/${panelPath}`;
}

async function redirectToAdminPanel() {
  window.location.assign(await resolveAdminPanelPath());
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("复制失败，请手动复制");
}

function scrollLogToBottom(element: HTMLElement | null) {
  if (!element) return () => {};

  const scroll = () => {
    element.scrollTop = element.scrollHeight;
  };
  let secondFrame = 0;
  scroll();
  const firstFrame = window.requestAnimationFrame(() => {
    scroll();
    secondFrame = window.requestAnimationFrame(scroll);
  });

  return () => {
    window.cancelAnimationFrame(firstFrame);
    if (secondFrame) window.cancelAnimationFrame(secondFrame);
  };
}

function SectionHeader({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="page-header">
      <div>
        <h1>
          {title}
        </h1>
        <p>
          {description}
        </p>
      </div>
    </div>
  );
}

function Panel({
  title,
  description,
  actions,
  children,
  className = "",
  header = true,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  header?: boolean;
}) {
  return (
    <section className={cn("panel", className)}>
      {header ? (
        <div className="panel-header flex items-start justify-between gap-4">
          <div>
            <h2 className="panel-title">{title}</h2>
            {description ? (
              <p className="mt-1 text-xs leading-5 text-slate-400">
                {description}
              </p>
            ) : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      <div className={header ? "mt-5" : undefined}>{children}</div>
    </section>
  );
}

function ConfigToggle({
  label,
  checked,
  onChange,
  description,
  disabled = false,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  description?: string;
  disabled?: boolean;
}) {
  return (
    <div className="toggle-field">
      <div className="toggle-label">
        <span>{label}</span>
        {description ? <p>{description}</p> : null}
      </div>
      <label className="toggle-control">
        <input
          type="checkbox"
          className="toggle-input"
          checked={checked}
          disabled={disabled}
          onChange={(event) => onChange(event.target.checked)}
        />
        <span className="toggle-switch" />
      </label>
    </div>
  );
}

function LegacyConfigPanel({
  title,
  children,
  className = "",
}: {
  title: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("panel config-legacy-panel", className)}>
      <h2 className="panel-title">{title}</h2>
      {children}
    </section>
  );
}

function LegacyBrandIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="white"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      color="white"
      data-lucide="zap"
      className="lucide lucide-zap"
    >
      <path d="M15.914 4a1.5 1.5 0 00-2.474-1.561l-9 9A1.5 1.5 0 005.5 14h4.002a.5.5 0 01.471.666L8.086 20a1.5 1.5 0 002.475 1.56l9-9A1.5 1.5 0 0018.5 10h-3.997a.5.5 0 01-.472-.667z" />
    </svg>
  );
}

function LegacyEraserIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M21 21H8a2 2 0 0 1-1.42-.587l-3.994-3.999a2 2 0 0 1 0-2.828l10-10a2 2 0 0 1 2.829 0l5.999 6a2 2 0 0 1 0 2.828L12.834 21" />
      <path d="m5.082 11.09 8.828 8.828" />
    </svg>
  );
}

function ConfirmDialog({
  request,
  close,
}: {
  request: { message: string; title: string } | null;
  close: (accepted: boolean) => void;
}) {
  if (!request) return null;
  return (
    <div
      className="admin-confirm-overlay is-open"
      role="presentation"
      aria-hidden="false"
      onClick={() => close(false)}
    >
      <div
        className="admin-confirm-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="admin-confirm-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div id="admin-confirm-title" className="admin-confirm-title">
          {request.title}
        </div>
        <p className="admin-confirm-text">{request.message}</p>
        <div className="admin-confirm-actions">
          <Button variant="outline" onClick={() => close(false)}>
            取消
          </Button>
          <Button onClick={() => close(true)}>确定</Button>
        </div>
      </div>
    </div>
  );
}

function DataTable({
  columns,
  data,
  empty = "暂无数据",
  minWidth = 760,
  tableClassName = "",
  tableContainerClassName = "",
  onRowClick,
}: {
  columns: Array<{
    key: string;
    label: string;
    width?: string | number;
    headerClassName?: string;
    cellClassName?: string;
    render?: (row: Row) => React.ReactNode;
  }>;
  data: Row[];
  empty?: string;
  minWidth?: number;
  tableClassName?: string;
  tableContainerClassName?: string;
  onRowClick?: (row: Row) => void;
}) {
  return (
    <div className={cn("table-container", tableContainerClassName)}>
      <table className={cn("w-full text-left text-sm", tableClassName)} style={{ minWidth }}>
        <thead className="bg-slate-50 text-xs text-slate-400">
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                className={cn(
                  "font-medium",
                  column.key === "select" && "select-header",
                  column.headerClassName,
                )}
                {...(column.width === undefined
                  ? {}
                  : ({ width: column.width } as Record<string, string | number>))}
              >
                {column.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className={tableClassName === "task-table" ? "" : "divide-y divide-slate-100"}>
          {data.length ? data.map((row, index) => (
            <tr
              key={text(row, "id", text(row, "job_key", String(index)))}
              className={onRowClick ? "table-row-clickable" : undefined}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              {columns.map((column) => (
                <td
                  key={column.key}
                  className={cn(
                    "align-top text-slate-600",
                    column.key === "select" && "select-cell",
                    column.cellClassName,
                  )}
                >
                  {column.render ? column.render(row) : text(row, column.key)}
                </td>
              ))}
            </tr>
          )) : (
            <tr>
              <td colSpan={columns.length} className="table-empty text-center text-sm text-slate-400">
                {empty}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

export function AdminConsoleView({ section }: { section: AdminSection }) {
  const initialSection = section === "settings" ? "config" : section;
  const [activeSection, setActiveSection] = useState<Exclude<AdminSection, "settings">>(initialSection);
  const [publicAdminBase, setPublicAdminBase] = useState(() =>
    typeof window === "undefined" ? "" : detectPublicAdminBase(window.location.pathname),
  );
  const [phase, setPhase] = useState<AuthPhase>("checking");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [challenge, setChallenge] = useState("");
  const [methods, setMethods] = useState<string[]>([]);
  const [method, setMethod] = useState("totp");
  const [code, setCode] = useState("");
  const [loginBusy, setLoginBusy] = useState(false);
  const [error, setError] = useState("");
  const [, setLoading] = useState(false);
  const [notice, setNotice] = useState("");
  const [confirmRequest, setConfirmRequest] = useState<{
    message: string;
    title: string;
    resolve: (accepted: boolean) => void;
  } | null>(null);
  const [adminData, setAdminData] = useState<Row>({});
  const [taskRows, setTaskRows] = useState<Row[]>([]);
  const [taskMeta, setTaskMeta] = useState<Row>({ page: 1, pageSize: 12, total: 0, totalPages: 1 });
  const [cdkRows, setCdkRows] = useState<Row[]>([]);
  const [cdkMeta, setCdkMeta] = useState<Row>({ page: 1, pageSize: 12, total: 0, totalPages: 1 });
  const [cardRows, setCardRows] = useState<Row[]>([]);
  const [cardMeta, setCardMeta] = useState<Row>({});
  const [proxyRows, setProxyRows] = useState<Row[]>([]);
  const [proxyMeta, setProxyMeta] = useState<Row>({});
  const [addressRows, setAddressRows] = useState<Row[]>([]);
  const [poolEmailRows, setPoolEmailRows] = useState<Row[]>([]);
  const [phoneRows, setPhoneRows] = useState<Row[]>([]);
  const [productRows, setProductRows] = useState<Row[]>([]);
  const [storeProductRows, setStoreProductRows] = useState<Row[]>([]);
  const [sessionRows, setSessionRows] = useState<Row[]>([]);
  const [billingRows, setBillingRows] = useState<Row[]>([]);
  const [billingMeta, setBillingMeta] = useState<Row>({});
  const [runtimeRows, setRuntimeRows] = useState<Row[]>([]);
  const [loginRows, setLoginRows] = useState<Row[]>([]);
  const [settings, setSettings] = useState<Row>({ mode: "browser" });
  const [securityStatus, setSecurityStatus] = useState<Row>({});
  const [gpt, setGPT] = useState<Row>({});
  const [hcaptcha, setHcaptcha] = useState<Row>({});
  const [telegram, setTelegram] = useState<Row>({});
  const [region, setRegion] = useState("PH");
  const [regionInfo, setRegionInfo] = useState<Row>({});
  const [regionOptions, setRegionOptions] = useState<Row[]>([]);
  const [browserPool, setBrowserPool] = useState<Row>({});
  const [checkoutPlans, setCheckoutPlans] = useState<Row>({});
  const [storeProductOptions, setStoreProductOptions] = useState<Row>({});
  const [businessExpanded, setBusinessExpanded] = useState(
    businessSections.includes(activeSection),
  );

  useEffect(() => {
    setActiveSection(initialSection);
    if (businessSections.includes(initialSection)) setBusinessExpanded(true);
  }, [initialSection]);

  useEffect(() => {
    if (phase !== "authorized") return;
    const fontLink = document.createElement("link");
    fontLink.rel = "stylesheet";
    fontLink.href = "https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;600;700&display=swap";
    fontLink.dataset.adminFont = "legacy";
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = "/legacy-admin.css?v=20260831-provider-config-v1";
    link.dataset.adminTheme = "legacy";
    document.head.appendChild(fontLink);
    document.head.appendChild(link);
    return () => {
      fontLink.remove();
      link.remove();
    };
  }, [phase]);

  useEffect(() => {
    if (phase !== "authorized" || (!notice && !error)) return;
    const timer = window.setTimeout(() => {
      setNotice("");
      setError("");
    }, 3500);
    return () => window.clearTimeout(timer);
  }, [error, notice, phase]);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      if (activeSection === "overview") {
        setAdminData(asRow(await getAdminData()));
      } else if (activeSection === "config") {
        const [
          configResult,
          securityResult,
          gptResult,
          captchaResult,
          telegramResult,
          regionResult,
        ] = await Promise.all([
          getConfig(),
          getSecurityStatus(),
          getGPTConfig(),
          getHcaptchaConfig(),
          getTelegramConfig(),
          getRegion(),
        ]);
        setSettings(asRow(asRow(configResult).config));
        setSecurityStatus(asRow(securityResult));
        setGPT(asRow(asRow(gptResult).config));
        setHcaptcha(asRow(asRow(captchaResult).config));
        setTelegram(asRow(asRow(telegramResult).config));
        const regionPayload = asRow(regionResult);
        setRegionInfo(regionPayload);
        setRegionOptions(rows(regionPayload.options));
        setRegion(text(regionPayload, "region", "PH"));
      } else if (activeSection === "proxies") {
        const result = asRow(await getProxies());
        setProxyRows(rows(result.proxies));
        setProxyMeta(asRow(result.summary));
      } else if (activeSection === "browser_pool") {
        setBrowserPool(asRow(await getBrowserPool()));
      } else if (activeSection === "tax_addresses") {
        const result = asRow(await getAddresses("US"));
        setAddressRows(rows(result.addresses));
      } else if (activeSection === "checkout_debug") {
        setCheckoutPlans(asRow(await getCheckoutPlans()));
      } else if (activeSection === "cdks") {
        const result = asRow(await getCDKs({ page: 1, pageSize: 12 }));
        setCdkRows(rows(result.cdks));
        setCdkMeta(result);
      } else if (activeSection === "store_products") {
        const result = await getStoreProducts();
        setStoreProductRows(rows(result.products));
        setStoreProductOptions(asRow(result.options));
      } else if (activeSection === "cards") {
        const result = asRow(await getCards());
        setCardRows(rows(result.cards));
        setCardMeta(asRow(result.stats));
      } else if (activeSection === "pool_emails") {
        setPoolEmailRows(rows(asRow(await getPoolEmails()).items));
      } else if (activeSection === "phones") {
        setPhoneRows(rows(asRow(await getPhones()).phones));
      } else if (activeSection === "products") {
        setProductRows(rows(asRow(await getProducts()).items));
      } else if (activeSection === "sessions") {
        setSessionRows(rows(asRow(await getSessions()).sessions));
      } else if (
        activeSection === "logs" ||
        activeSection === "automation_tasks"
      ) {
        const result = asRow(await getTaskLogs({ page: 1, pageSize: 12 }));
        setTaskRows(rows(result.logs || result.tasks));
        setTaskMeta(result);
      } else if (activeSection === "billing") {
        const result = asRow(await getBilling("page=1&page_size=20"));
        setBillingRows(rows(result.records || result.billing));
        setBillingMeta(result);
      } else if (activeSection === "runtime_logs") {
        setRuntimeRows(rows(asRow(await getRuntimeLogs(500)).logs));
      } else if (activeSection === "admin_login_logs") {
        setLoginRows(rows(asRow(await getLoginLogs(200)).logs));
      }
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoading(false);
    }
  }, [activeSection]);

  useEffect(() => {
    let disposed = false;
    const token = adminToken();
    if (!token) {
      setPhase("login");
      return () => {
        disposed = true;
      };
    }
    getAdminSession()
      .then(async () => {
        if (disposed) return;
        const panelPath = await resolveAdminPanelPath();
        const currentPath = window.location.pathname.replace(/\/+$/, "") || "/";
        if (currentPath !== panelPath && !currentPath.startsWith(`${panelPath}/`)) {
          await redirectToAdminPanel();
          return;
        }
        setPhase("authorized");
      })
      .catch(() => {
        clearAuthTokens();
        if (!disposed) setPhase("login");
      });
    return () => {
      disposed = true;
    };
  }, []);

  useEffect(() => {
    const handlePopState = () => {
      const nextPublicAdminBase = detectPublicAdminBase(window.location.pathname);
      setPublicAdminBase(nextPublicAdminBase);
      const nextSection = sectionFromPathname(
        window.location.pathname,
        nextPublicAdminBase,
      );
      setActiveSection(nextSection);
      if (businessSections.includes(nextSection)) setBusinessExpanded(true);
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const adminHref = (internalPath: string) => {
    if (!publicAdminBase) return internalPath;
    return `${publicAdminBase}${internalPath.replace(/^\/admin/, "")}`;
  };

  function navigateAdminSection(item: (typeof navItems)[number]) {
    if (businessSections.includes(item.key)) setBusinessExpanded(true);
    const href = adminHref(item.path);
    const currentPath = window.location.pathname.replace(/\/+$/, "") || "/";
    if (currentPath !== href) {
      window.history.pushState({ adminSection: item.key }, "", href);
    }
    setActiveSection(item.key);
  }

  function handleAdminLinkClick(
    event: MouseEvent<HTMLAnchorElement>,
    item: (typeof navItems)[number],
  ) {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) return;
    event.preventDefault();
    navigateAdminSection(item);
  }

  useEffect(() => {
    if (phase === "authorized") void loadData();
  }, [phase, loadData]);

  useEffect(() => {
    if (phase !== "authorized" || activeSection !== "overview") return;
    const refreshTimer = window.setInterval(() => {
      void loadData();
    }, 3000);
    return () => window.clearInterval(refreshTimer);
  }, [activeSection, loadData, phase]);

  async function submitLogin(event: FormEvent) {
    event.preventDefault();
    setLoginBusy(true);
    setError("");
    try {
      const result = asRow(
        await loginAdmin({
          email,
          password,
          fingerprint:
            typeof navigator === "undefined" ? "" : navigator.userAgent,
        }),
      );
      if (bool(result, "requires2fa")) {
        const available = Array.isArray(result.methods)
          ? result.methods.map(String)
          : [];
        setChallenge(text(result, "challengeToken", ""));
        setMethods(available);
        setMethod(text(result, "defaultMethod", available[0] || "totp"));
        setPhase("2fa");
      } else {
        const token = text(result, "token", "");
        if (!token) throw new Error("登录响应缺少管理员 Token");
        setAuthTokens(token);
        await redirectToAdminPanel();
      }
      setPassword("");
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoginBusy(false);
    }
  }

  async function submit2FA(event: FormEvent) {
    event.preventDefault();
    setLoginBusy(true);
    setError("");
    try {
      const result = asRow(
        await verifyAdmin2FA({
          challengeToken: challenge,
          method,
          code,
          fingerprint: navigator.userAgent,
        }),
      );
      const token = text(result, "token", "");
      if (!token) throw new Error("二次验证响应缺少管理员 Token");
      setAuthTokens(token);
      setCode("");
      await redirectToAdminPanel();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoginBusy(false);
    }
  }

  function logout() {
    clearAuthTokens();
    setEmail("");
    setPassword("");
    setChallenge("");
    setCode("");
    setPhase("login");
  }

  const confirmAction: ConfirmAction = useCallback(
    (message, title = "请确认") =>
      new Promise<boolean>((resolve) => {
        setConfirmRequest({ message, title, resolve });
      }),
    [],
  );

  const closeConfirm = (accepted: boolean) => {
    setConfirmRequest((request) => {
      request?.resolve(accepted);
      return null;
    });
  };

  if (phase === "checking")
    return (
      <main className="flex min-h-screen items-center justify-center bg-[#f4f7f9]">
        <RefreshCw className="h-5 w-5 animate-spin text-cyan-600" />
      </main>
    );
  if (phase === "login" || phase === "2fa")
    return (
      <LoginPanel
        phase={phase}
        email={email}
        password={password}
        methods={methods}
        method={method}
        code={code}
        busy={loginBusy}
        error={error}
        setEmail={setEmail}
        setPassword={setPassword}
        setMethod={setMethod}
        setCode={setCode}
        submitLogin={submitLogin}
        submit2FA={submit2FA}
        sendCode={async () => {
          try {
            await sendTelegramCode({ challengeToken: challenge });
            setNotice("Telegram 验证码已发送");
          } catch (reason) {
            setError(errorMessage(reason));
          }
        }}
        back={() => {
          setPhase("login");
          setError("");
        }}
        notice={notice}
      />
    );

  const title =
    ({
      overview: "概览中心",
      config: "系统配置",
      settings: "系统配置",
      proxies: "代理池",
      browser_pool: "浏览器池",
      tax_addresses: "免税地址",
      checkout_debug: "支付链接调试",
      cards: "银行卡池管理",
      cdks: "CDK 激活码管理",
      sessions: "Session 管理",
      cancel_renewal: "续费管理",
      logs: "任务管理",
      automation_tasks: "自动化任务",
      billing: "账单记录",
      runtime_logs: "运行日志",
      admin_login_logs: "后台登录日志",
      store_products: "售卡商品",
      pool_emails: "邮箱池",
      phones: "手机号池",
      products: "成品号库",
    } satisfies Record<AdminSection, string>)[activeSection] || "概览中心";
  const description = {
    overview: "实时监控任务执行状态与资源分配情况",
    config: "自助开通并发与维护模式（保存后立即生效，无需重启服务）。Telegram / hCaptcha 需分别点各自区域的「保存」按钮；配置由 Go API 写入 PostgreSQL，重启容器不会丢失（请勿使用 docker compose down -v）。",
    proxies: "管理 Playwright 自动化使用的代理。一行一条 URL，支持 http(s) / socks5；用户名可含 {session} 占位符走 sticky session。每次任务从「启用」的代理中随机抽取。",
    browser_pool: "常驻 Chromium 进程与本地 Profile 缓存；任务通过 CDP 接入，每单独立 Context。页面每 2 秒自动刷新。",
    tax_addresses: "管理美国免税州账单地址池（Oregon / Delaware / Montana / New Hampshire / Alaska）。支付时随机选取；州名使用完整英文（如 Oregon）以匹配结账下拉框。",
    checkout_debug: "启动 Playwright 浏览器，注入 Session 后按系统配置的 Checkout 建单方式打开支付页（不执行填卡/订阅，正式开通请走前台 CDK）",
    cards: "管理 Stripe 支付使用的银行卡资源（通过 API 导入与维护）",
    cdks: "生成并管理用于前台兑换的激活码",
    store_products: "发布平台内购商品，调试模式下支付成功后立即发放 CDK。",
    pool_emails: "管理邮箱池资源",
    phones: "管理手机号池资源",
    products: "管理成品号库资源",
    sessions: "手动提交 CDK + Session 启动 Playwright 自动化开通；下方列表为历史记录",
    cancel_renewal: "粘贴 Session 后管理 ChatGPT Plus/Pro 自动续费（App Store / Google Play 订阅需在对应平台操作）",
    logs: "查看与管理自助开通任务记录",
    automation_tasks: "查看 Playwright 自动化各阶段是否完成，快速判断 Checkout 页面是否已打开、Stripe 表单是否定位成功",
    billing: "查看支付账单明细、筛选与导出，追踪每张卡的消费记录",
    runtime_logs: "子进程标准输出、注册/开通脚本详情与任务节点日志（内存环形缓冲，重启后清空）",
    admin_login_logs: "记录后台登录、二次验证等安全事件（IP、浏览器指纹）",
  }[activeSection] || "";

  const legacyNav = (key: AdminSection) => navItems.find((item) => item.key === key)!;
  const navigateToSection = (key: NavigableAdminSection) => {
    const item = navItems.find((candidate) => candidate.key === key);
    if (item) navigateAdminSection(item);
  };
  const navLink = (item: (typeof navItems)[number], sub = false) => {
    const Icon = item.icon;
    return (
      <a
        key={item.key}
        href={adminHref(item.path)}
        onClick={(event) => handleAdminLinkClick(event, item)}
        aria-current={activeSection === item.key ? "page" : undefined}
        className={cn(
          "nav-item flex items-center gap-3 rounded-[10px] text-sm font-medium transition",
          sub ? "nav-subitem px-3 py-2 pl-[38px] text-[13px]" : "px-3.5 py-2.5",
          activeSection === item.key
            ? "active bg-blue-600 font-semibold text-white shadow-[0_4px_10px_rgba(37,99,235,0.18)]"
            : "text-slate-700 hover:bg-blue-50 hover:text-blue-600",
        )}
      >
        {!sub ? <Icon /> : null}
        <span>{item.label}</span>
      </a>
    );
  };
  return (
    <main className="admin-console flex h-screen w-screen overflow-hidden bg-[#f4f7fb] text-slate-950">
      <aside className="sidebar hidden w-[260px] shrink-0 flex-col overflow-y-auto border-r border-slate-200 bg-white px-[18px] py-7 lg:flex">
        <a
          href={adminHref("/admin")}
          onClick={(event) => handleAdminLinkClick(event, legacyNav("overview"))}
          className="sidebar-brand mb-8 flex items-center gap-3 px-2"
        >
          <span className="brand-logo flex h-[38px] w-[38px] items-center justify-center rounded-[10px] bg-gradient-to-br from-blue-600 to-blue-500 text-white shadow-[0_6px_14px_rgba(37,99,235,0.18)]">
            <LegacyBrandIcon />
          </span>
          <span className="brand-name text-[17px] font-bold text-slate-950">KC GPT自动充值系统</span>
        </a>
        <nav className="nav-menu flex flex-1 flex-col gap-1">
          {navLink(legacyNav("overview"))}
          {navLink(legacyNav("config"))}
          {navLink(legacyNav("proxies"))}
          {navLink(legacyNav("browser_pool"))}
          {navLink(legacyNav("tax_addresses"))}
          <div className={cn("nav-group mt-0 flex flex-col gap-0.5", businessExpanded && "expanded")}>
            <button type="button" className="nav-item nav-group-toggle flex items-center gap-3 rounded-[10px] px-3.5 py-2.5 text-left text-sm font-medium text-slate-700" onClick={() => setBusinessExpanded((value) => !value)}>
              <Layers />
              <span>支付与资产</span>
              <ChevronDown className="nav-chevron ml-auto h-4 w-4" />
            </button>
            {businessExpanded ? <div className="nav-subitems flex flex-col gap-0.5 pl-2">
              {navLink(legacyNav("checkout_debug"), true)}
              {navLink(legacyNav("cards"), true)}
              {navLink(legacyNav("cdks"), true)}
              {navLink(legacyNav("store_products"), true)}
              {navLink(legacyNav("sessions"), true)}
              {navLink(legacyNav("billing"), true)}
            </div> : null}
          </div>
          {navLink(legacyNav("cancel_renewal"))}
          {navLink(legacyNav("logs"))}
          {navLink(legacyNav("automation_tasks"))}
          {navLink(legacyNav("runtime_logs"))}
          {navLink(legacyNav("admin_login_logs"))}
        </nav>
        <div className="sidebar-footer mt-auto border-t border-slate-200 pt-4">
          <div
            role="link"
            tabIndex={0}
            onClick={() => window.location.assign("/")}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") window.location.assign("/");
            }}
            className="nav-item"
            style={{ color: "var(--error)" }}
          >
            <LogOut />
            <span>返回前台</span>
          </div>
          <div role="button" tabIndex={0} onClick={logout} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") logout(); }} className="nav-item">
            <ShieldOff />
            <span>退出登录</span>
          </div>
        </div>
      </aside>
      <section className="main-content min-w-0 flex-1 overflow-y-auto px-5 py-9 lg:px-12">
        <div className="mx-auto max-w-[1280px]">
          <SectionHeader
            title={title}
            description={description}
          />
            {error ? (
              <div className="message-container admin-message-container" role="alert" aria-live="assertive">
                <div className="message-item message-error">
                  <TriangleAlert className="h-4 w-4" />
                  {error}
                </div>
              </div>
            ) : null}
            {notice ? (
              <div className="message-container admin-message-container" role="status" aria-live="polite">
                <div className="message-item message-success">
                  <CheckCircle2 className="h-4 w-4" />
                  {notice}
                </div>
              </div>
            ) : null}
            <SectionContent
                section={activeSection}
                adminData={adminData}
                taskRows={taskRows}
                setTaskRows={setTaskRows}
                taskMeta={taskMeta}
                setTaskMeta={setTaskMeta}
                cdkRows={cdkRows}
                setCdkRows={setCdkRows}
                cdkMeta={cdkMeta}
                setCdkMeta={setCdkMeta}
                cardRows={cardRows}
                setCardRows={setCardRows}
                cardMeta={cardMeta}
                setCardMeta={setCardMeta}
                proxyRows={proxyRows}
                setProxyRows={setProxyRows}
                proxyMeta={proxyMeta}
                setProxyMeta={setProxyMeta}
                addressRows={addressRows}
                setAddressRows={setAddressRows}
                poolEmailRows={poolEmailRows}
                setPoolEmailRows={setPoolEmailRows}
                phoneRows={phoneRows}
                setPhoneRows={setPhoneRows}
                productRows={productRows}
                setProductRows={setProductRows}
                storeProductRows={storeProductRows}
                setStoreProductRows={setStoreProductRows}
                sessionRows={sessionRows}
                setSessionRows={setSessionRows}
                billingRows={billingRows}
                setBillingRows={setBillingRows}
                billingMeta={billingMeta}
                setBillingMeta={setBillingMeta}
                runtimeRows={runtimeRows}
                setRuntimeRows={setRuntimeRows}
                loginRows={loginRows}
                setLoginRows={setLoginRows}
                settings={settings}
                setSettings={setSettings}
                securityStatus={securityStatus}
                setSecurityStatus={setSecurityStatus}
                gpt={gpt}
                setGPT={setGPT}
                hcaptcha={hcaptcha}
                setHcaptcha={setHcaptcha}
                telegram={telegram}
                setTelegram={setTelegram}
                region={region}
                setRegion={setRegion}
                regionInfo={regionInfo}
                regionOptions={regionOptions}
                browserPool={browserPool}
                setBrowserPool={setBrowserPool}
                checkoutPlans={checkoutPlans}
                storeProductOptions={storeProductOptions}
                setNotice={setNotice}
                setError={setError}
                confirm={confirmAction}
                navigate={navigateToSection}
                reload={loadData}
              />
        </div>
      </section>
      <ConfirmDialog
        request={confirmRequest}
        close={closeConfirm}
      />
    </main>
  );
}

function LoginPanel({
  phase,
  email,
  password,
  methods,
  method,
  code,
  busy,
  error,
  notice,
  setEmail,
  setPassword,
  setMethod,
  setCode,
  submitLogin,
  submit2FA,
  sendCode,
  back,
}: {
  phase: AuthPhase;
  email: string;
  password: string;
  methods: string[];
  method: string;
  code: string;
  busy: boolean;
  error: string;
  notice: string;
  setEmail: (value: string) => void;
  setPassword: (value: string) => void;
  setMethod: (value: string) => void;
  setCode: (value: string) => void;
  submitLogin: (event: FormEvent) => void;
  submit2FA: (event: FormEvent) => void;
  sendCode: () => Promise<void>;
  back: () => void;
}) {
  const is2FA = phase === "2fa";
  return (
    <main className="admin-login-page">
      <div className="w-full max-w-[440px] rounded-[20px] border border-slate-200 bg-white p-9 shadow-[0_12px_32px_rgba(15,23,42,.10)]">
        <h1 className="text-2xl font-bold">{is2FA ? "二次验证" : "后台登录"}</h1>
        <p className="mt-2 mb-5 text-sm leading-6 text-slate-500">
          {is2FA
            ? "请选择验证方式并输入验证码。"
            : "使用管理员邮箱和密码登录。若已启用二次验证，登录后还需验证。"}
        </p>
        {is2FA ? (
          <form onSubmit={submit2FA} className="space-y-3.5">
            {methods.length > 1 ? (
              <div className="flex gap-2 rounded-xl border border-slate-200 bg-slate-50 p-1">
                {methods.map((item) => (
                  <button
                    key={item}
                    type="button"
                    onClick={() => setMethod(item)}
                    className={cn(
                      "flex-1 rounded-[10px] border border-transparent bg-transparent px-2 py-2.5 text-[13px] font-semibold",
                      method === item
                        ? "border-blue-200 bg-white text-blue-600 shadow-[0_2px_8px_rgba(37,99,235,.12)]"
                        : "text-slate-500",
                    )}
                  >
                    {item === "telegram" ? "Telegram 验证码" : "Google Authenticator"}
                  </button>
                ))}
              </div>
            ) : null}
            {methods.length === 1 ? (
              <div className="rounded-[10px] bg-blue-50 px-3 py-2.5 text-[13px] font-semibold text-blue-600">
                验证方式：{method === "telegram" ? "Telegram 验证码" : "Google Authenticator"}
              </div>
            ) : null}
            <p className="-mb-1 text-[13px] leading-6 text-slate-500">
              {method === "telegram"
                ? "点击下方按钮发送验证码到管理员 Telegram，再输入 6 位数字。"
                : "请打开 Google Authenticator，输入当前 6 位动态码。"}
            </p>
            <label
              className="block text-[13px] font-semibold text-slate-500"
              htmlFor="admin-2fa-code"
            >
              验证码
            </label>
            <input
              id="admin-2fa-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={code}
              onChange={(event) => setCode(event.target.value)}
              className="mt-2 mb-0 h-11 w-full rounded-xl border border-slate-200 bg-white px-3.5 text-sm outline-none focus:border-blue-600 focus:ring-4 focus:ring-blue-100"
              placeholder={method === "telegram" ? "Telegram 6 位验证码" : "Google Authenticator 6 位码"}
            />
            {method === "telegram" ? (
              <Button
                type="button"
                variant="outline"
                className="w-full"
                onClick={() => void sendCode()}
              >
                <Send className="h-4 w-4" />
                发送 Telegram 验证码
              </Button>
            ) : null}
            <Button type="submit" className="w-full" disabled={busy}>
              <ShieldCheck className="h-4 w-4" />
              {busy ? "验证中..." : "验证并登录"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="w-full"
              onClick={back}
            >
              返回
            </Button>
          </form>
        ) : (
          <form onSubmit={submitLogin} className="space-y-0">
            <label
              className="block text-[13px] font-semibold text-slate-500"
              htmlFor="admin-email"
            >
              管理员邮箱
            </label>
            <input
              id="admin-email"
              type="email"
              autoComplete="username"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              className="mb-3.5 mt-2 h-11 w-full rounded-xl border border-slate-200 bg-white px-3.5 text-sm outline-none focus:border-blue-600 focus:ring-4 focus:ring-blue-100"
              placeholder="请输入管理员邮箱"
            />
            <label
              className="block text-[13px] font-semibold text-slate-500"
              htmlFor="admin-password"
            >
              登录密码
            </label>
            <input
              id="admin-password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              className="mb-0 mt-2 h-11 w-full rounded-xl border border-slate-200 bg-white px-3.5 text-sm outline-none focus:border-blue-600 focus:ring-4 focus:ring-blue-100"
              placeholder="请输入登录密码"
            />
            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? "登录中..." : "继续"}
            </Button>
          </form>
        )}
        <div className="admin-login-message">
          {notice ? <p className="text-sm text-emerald-700">{notice}</p> : null}
          {error ? <p className="text-sm text-red-700">{error}</p> : null}
        </div>
        <p className="admin-login-hint mt-3 text-xs leading-5 text-slate-500">
          登录失败 5 次将锁定 30 分钟。可在后台「系统配置 → 安全设置」切换验证方式。
        </p>
      </div>
    </main>
  );
}

type ContentProps = {
  section: Exclude<AdminSection, "settings">;
  adminData: Row;
  taskRows: Row[];
  setTaskRows: (value: Row[]) => void;
  taskMeta: Row;
  setTaskMeta: (value: Row) => void;
  cdkRows: Row[];
  setCdkRows: (value: Row[]) => void;
  cdkMeta: Row;
  setCdkMeta: (value: Row) => void;
  cardRows: Row[];
  setCardRows: (value: Row[]) => void;
  cardMeta: Row;
  setCardMeta: (value: Row) => void;
  proxyRows: Row[];
  setProxyRows: (value: Row[]) => void;
  proxyMeta: Row;
  setProxyMeta: (value: Row) => void;
  addressRows: Row[];
  setAddressRows: (value: Row[]) => void;
  poolEmailRows: Row[];
  setPoolEmailRows: (value: Row[]) => void;
  phoneRows: Row[];
  setPhoneRows: (value: Row[]) => void;
  productRows: Row[];
  setProductRows: (value: Row[]) => void;
  storeProductRows: Row[];
  setStoreProductRows: (value: Row[]) => void;
  sessionRows: Row[];
  setSessionRows: (value: Row[]) => void;
  billingRows: Row[];
  setBillingRows: (value: Row[]) => void;
  billingMeta: Row;
  setBillingMeta: (value: Row) => void;
  runtimeRows: Row[];
  setRuntimeRows: (value: Row[]) => void;
  loginRows: Row[];
  setLoginRows: (value: Row[]) => void;
  settings: Row;
  setSettings: (value: Row) => void;
  securityStatus: Row;
  setSecurityStatus: (value: Row) => void;
  gpt: Row;
  setGPT: (value: Row) => void;
  hcaptcha: Row;
  setHcaptcha: (value: Row) => void;
  telegram: Row;
  setTelegram: (value: Row) => void;
  region: string;
  setRegion: (value: string) => void;
  regionInfo: Row;
  regionOptions: Row[];
  browserPool: Row;
  setBrowserPool: (value: Row) => void;
  checkoutPlans: Row;
  storeProductOptions: Row;
  setNotice: (value: string) => void;
  setError: (value: string) => void;
  confirm: ConfirmAction;
  navigate: (section: NavigableAdminSection) => void;
  reload: () => Promise<void>;
};

function SectionContent(props: ContentProps) {
  switch (props.section) {
    case "overview":
      return <OverviewPanel data={props.adminData} />;
    case "config":
      return <ConfigPanel {...props} />;
    case "proxies":
      return <ProxyPanel {...props} />;
    case "browser_pool":
      return <BrowserPoolPanel {...props} />;
    case "tax_addresses":
      return <AddressPanel {...props} />;
    case "checkout_debug":
      return <CheckoutPanel {...props} />;
    case "cards":
      return <CardsPanel {...props} />;
    case "cdks":
      return <CDKPanel {...props} />;
    case "store_products":
      return <StoreProductsPanel {...props} />;
    case "pool_emails":
      return (
        <PoolEmailsPanel
          rows={props.poolEmailRows}
          setRows={props.setPoolEmailRows}
          setNotice={props.setNotice}
          setError={props.setError}
          confirm={props.confirm}
        />
      );
    case "phones":
      return (
        <PhonePoolPanel
          rows={props.phoneRows}
          setRows={props.setPhoneRows}
          setNotice={props.setNotice}
          setError={props.setError}
          confirm={props.confirm}
        />
      );
    case "products":
      return (
        <ProductPoolPanel
          rows={props.productRows}
          setRows={props.setProductRows}
          setNotice={props.setNotice}
          setError={props.setError}
          confirm={props.confirm}
        />
      );
    case "sessions":
      return <SessionsPanel {...props} />;
    case "cancel_renewal":
      return <RenewalPanel {...props} />;
    case "logs":
      return <TaskPanel {...props} automation={false} />;
    case "automation_tasks":
      return <TaskPanel {...props} automation />;
    case "billing":
      return <BillingPanel {...props} />;
    case "runtime_logs":
      return <RuntimePanel {...props} />;
    case "admin_login_logs":
      return <LoginLogPanel {...props} />;
    default:
      return null;
  }
}

function OverviewPanel({ data }: { data: Row }) {
	const metrics = rows(rowValue(data, "metrics"));
	const design: Record<string, { Icon: ElementType; valueColor: string; iconColor: string; iconBackground: string }> = {
		cpu: { Icon: Cpu, valueColor: "#db2777", iconColor: "#f472b6", iconBackground: "rgba(244, 114, 182, 0.1)" },
		memory: { Icon: MemoryStick, valueColor: "#047857", iconColor: "#4ade80", iconBackground: "rgba(34, 197, 94, 0.1)" },
		disk: { Icon: HardDrive, valueColor: "#b45309", iconColor: "#fbbf24", iconBackground: "rgba(251, 191, 36, 0.1)" },
		uptime: { Icon: Timer, valueColor: "#0f766e", iconColor: "#5eead4", iconBackground: "rgba(45, 212, 191, 0.1)" },
		task_total: { Icon: Activity, valueColor: "#0f172a", iconColor: "#2563eb", iconBackground: "rgba(99, 102, 241, 0.1)" },
		task_success: { Icon: CheckCircle2, valueColor: "#047857", iconColor: "#10b981", iconBackground: "rgba(16, 185, 129, 0.1)" },
		task_failed: { Icon: CircleX, valueColor: "#b91c1c", iconColor: "#ef4444", iconBackground: "rgba(239, 68, 68, 0.1)" },
		cdk_total: { Icon: Tickets, valueColor: "#1d4ed8", iconColor: "#60a5fa", iconBackground: "rgba(59, 130, 246, 0.1)" },
		cdk_used: { Icon: BadgeCheck, valueColor: "#047857", iconColor: "#10b981", iconBackground: "rgba(16, 185, 129, 0.1)" },
		cdk_unused: { Icon: BadgePlus, valueColor: "#b45309", iconColor: "#f59e0b", iconBackground: "rgba(245, 158, 11, 0.1)" },
		card_total: { Icon: CreditCard, valueColor: "#7e22ce", iconColor: "#c084fc", iconBackground: "rgba(168, 85, 247, 0.1)" },
		billing_revenue: { Icon: Wallet, valueColor: "#047857", iconColor: "#10b981", iconBackground: "rgba(16, 185, 129, 0.1)" },
		billing_paid_count: { Icon: Receipt, valueColor: "#0f766e", iconColor: "#5eead4", iconBackground: "rgba(45, 212, 191, 0.1)" },
		foreground_slots: { Icon: Activity, valueColor: "#1d4ed8", iconColor: "#60a5fa", iconBackground: "rgba(59, 130, 246, 0.1)" },
	};
	return (
    <div>
      <div className="stat-grid">
		{metrics.map((metric) => {
			const key = text(metric, "key", "metric");
			const style = design[key] || design.task_total;
			const Icon = style.Icon;
			return <div key={key} className="stat-card">
				<div className="stat-header">
					<span className="stat-label">{text(metric, "label")}</span>
					<span className="stat-icon" style={{ background: style.iconBackground, color: style.iconColor }}>
						<Icon />
					</span>
				</div>
				<div
					className="stat-value"
					style={{
						color: style.valueColor,
						fontSize: key === "foreground_slots" ? 30 : undefined,
					}}
				>
					{text(metric, "value")}
				</div>
				{text(metric, "meta", "") ? <div style={{ fontSize: 13, color: "var(--text-dim)" }}>{text(metric, "meta", "")}</div> : null}
			</div>
		})}
      </div>
    </div>
  );
}

function InfoItem({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-md bg-slate-50 p-3">
      <span className="block text-xs text-slate-400">{label}</span>
      <strong className="mt-1 block text-sm text-slate-900">{value}</strong>
    </div>
  );
}

function ConfigPanel({
  settings,
  setSettings,
  securityStatus,
  setSecurityStatus,
  gpt,
  setGPT,
  hcaptcha,
  setHcaptcha,
  telegram,
  setTelegram,
  region,
  setRegion,
  regionInfo,
  regionOptions,
  setNotice,
  setError,
  reload,
}: ContentProps) {
  const [busy, setBusy] = useState(false);
  const [adminPassword, setAdminPassword] = useState({ current: "", next: "" });
  const [totp, setTotp] = useState<Row>({});
  const [gptStatus, setGPTStatus] = useState<Row>({});
  const [gptStatusError, setGPTStatusError] = useState("");
  void gptStatusError;
  const [captchaLogs, setCaptchaLogs] = useState<Row>({});
  const [captchaLogError, setCaptchaLogError] = useState("");
  const [diagnosticsBusy, setDiagnosticsBusy] = useState(false);
  const [emailTestRecipient, setEmailTestRecipient] = useState("");
  const [openProviderConfig, setOpenProviderConfig] = useState<ProviderConfigKey | null>(null);
  const [kimooxBins, setKimooxBins] = useState<Row[]>([]);
  const [kimooxBinsBusy, setKimooxBinsBusy] = useState(false);
  const [pathForm, setPathForm] = useState({
    login: text(securityStatus, "loginPath", "admin-login"),
    panel: text(securityStatus, "panelPath", "admin"),
  });
  useEffect(() => {
    setPathForm({
      login: text(securityStatus, "loginPath", "admin-login"),
      panel: text(securityStatus, "panelPath", "admin"),
    });
  }, [securityStatus]);
  const field = (key: string, fallback = "") => text(settings, key, fallback);
  const update = (key: string, value: string) =>
    setSettings({ ...settings, [key]: value });
  const refreshDiagnostics = async () => {
    setDiagnosticsBusy(true);
    const [gptResult, captchaResult] = await Promise.allSettled([
      getGPTStatus(),
      getHcaptchaLogs(),
    ]);
    if (gptResult.status === "fulfilled") {
      setGPTStatus(asRow(gptResult.value));
      setGPTStatusError("");
    } else {
      setGPTStatusError(errorMessage(gptResult.reason));
    }
    if (captchaResult.status === "fulfilled") {
      setCaptchaLogs(asRow(captchaResult.value));
      setCaptchaLogError("");
    } else {
      setCaptchaLogError(errorMessage(captchaResult.reason));
    }
    setDiagnosticsBusy(false);
  };
  useEffect(() => {
    void refreshDiagnostics();
  }, []);
  const saveSettingsSection = async (
    keys: readonly string[],
    successMessage: string,
  ) => {
    setBusy(true);
    try {
      const payload: JsonMap = {};
      for (const key of keys) {
        if (key === "checkout_mode") {
          payload[key] = field("checkout_mode", "api");
          continue;
        }
        if (Object.prototype.hasOwnProperty.call(settings, key)) {
          payload[key] = settings[key];
        }
      }
      await saveConfig(payload);
      setNotice(successMessage);
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };
  const saveRegionValue = async () => {
    try {
      await saveRegion(region);
      setNotice(`支付地区已保存：${region}`);
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const saveGpt = async () => {
    try {
      await saveGPTConfig(gpt as JsonMap);
      setNotice("第三方代充 API 配置已保存");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const saveCaptcha = async () => {
    try {
      await saveHcaptchaConfig(hcaptcha as JsonMap);
      setNotice("hCaptcha 配置已保存");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const saveTG = async () => {
    try {
      await saveTelegramConfig(telegram as JsonMap);
      setNotice("Telegram 配置已保存");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const testEmail = async () => {
    try {
      const recipient = emailTestRecipient.trim();
      if (!recipient) {
        setError("请输入测试收件人邮箱");
        return;
      }
      await sendEmailTest(recipient);
      setNotice("测试邮件已发送");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const updatePassword = async () => {
    try {
      await changeAdminPassword(adminPassword.current, adminPassword.next);
      setNotice("管理员密码已修改，请重新登录");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const savePaths = async () => {
    try {
      const result = await saveAdminPaths(pathForm.login, pathForm.panel);
      setSecurityStatus({ ...securityStatus, ...asRow(result) });
      setNotice("入口路径已保存");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const save2FAModeValue = async () => {
    try {
      const result = await save2FAMode(text(securityStatus, "login2faMode", "either"));
      setSecurityStatus({ ...securityStatus, ...asRow(result) });
      setNotice("登录验证方式已保存");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const setup = async () => {
    try {
      const result = asRow(await setupTOTP());
      setTotp({ ...result, code: "" });
      setNotice(text(result, "message", "请使用 Google Authenticator 扫码后输入验证码确认启用"));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const confirm = async () => {
    try {
      const result = await confirmTOTP(text(totp, "code", ""));
      setTotp({});
      setNotice(text(result, "message", "Google Authenticator 已启用"));
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const disable = async () => {
    const currentPassword = window.prompt("请输入当前登录密码") || "";
    if (!currentPassword) return;
    const code = window.prompt("若已启用 Authenticator，请输入当前 6 位验证码（未启用可留空）") || "";
    try {
      await disableTOTP(currentPassword, code);
      setTotp({});
      setNotice("Google Authenticator 已关闭");
      await reload();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const runTest = async (runner: () => Promise<unknown>, success: string) => {
    try {
      await runner();
      setNotice(success);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const providerEnabledKeys: Record<string, string> = {
    LOCAL_TEXT: "card_provider_local_text_enabled",
    AIRWALLEX: "card_provider_airwallex_enabled",
    STRIPE_ISSUING: "card_provider_stripe_issuing_enabled",
    PHOTONPAY: "card_provider_photonpay_enabled",
    DOGPAY: "card_provider_dogpay_enabled",
    KIMOOX: "card_provider_kimoox_enabled",
  };
  const defaultProvider = text(settings, "card_pool_default_provider", "LOCAL_TEXT").toUpperCase();
  const providerIsEnabled = (provider: string) => bool(settings, providerEnabledKeys[provider] || "");
  const providerOptionDisabled = (provider: string) => !providerIsEnabled(provider) && defaultProvider !== provider;
  const updateDefaultProvider = (provider: string) => {
    const normalized = provider.toUpperCase();
    if (!providerIsEnabled(normalized)) {
      setError(`请先启用 ${normalized}，再将它设为默认 Provider`);
      return;
    }
    update("card_pool_default_provider", normalized);
  };
  const updateProviderEnabled = (provider: string, enabled: boolean) => {
    const normalized = provider.toUpperCase();
    if (!enabled && defaultProvider === normalized) {
      setError(`当前默认 Provider 是 ${normalized}，请先切换默认 Provider 后再关闭`);
      return;
    }
    update(providerEnabledKeys[normalized], enabled ? "true" : "false");
  };
  const providerDescription = (provider: string, description: string) =>
    defaultProvider === provider
      ? `${description} 当前为默认 Provider，请先切换后才能关闭。`
      : description;
  const toggleProviderConfig = (provider: ProviderConfigKey) => {
    setOpenProviderConfig((current) => (current === provider ? null : provider));
  };
  const selectedKimooxBINs = () => {
    const configured = field("kimoox_card_bin_ids");
    return configured
      .replace(/^\[/, "")
      .replace(/\]$/, "")
      .split(/[\s,;]+/)
      .map((value) => value.replace(/^['"]|['"]$/g, "").trim())
      .filter(Boolean);
  };
  const loadKimooxBINs = async () => {
    const typedKey = field("kimoox_api_key").trim();
    const typedSecret = field("kimoox_api_secret").trim();
    const savedKey = bool(settings, "kimooxAPIKeySavedValue");
    const savedSecret = bool(settings, "kimooxAPISecretSavedValue");
    if ((!typedKey && !savedKey) || (!typedSecret && !savedSecret)) {
      setError("请先填写 Kimoox API Key 和 API Secret");
      return;
    }
    setKimooxBinsBusy(true);
    try {
      if (typedKey || typedSecret) {
        const payload: JsonMap = { kimoox_base_url: field("kimoox_base_url", "https://card.kimoox.com") };
        if (typedKey) payload.kimoox_api_key = typedKey;
        if (typedSecret) payload.kimoox_api_secret = typedSecret;
        await saveConfig(payload);
        await reload();
      }
      const result = await getKimooxCardBINs();
      const loaded = rows(result.bins);
      setKimooxBins(loaded);
      const remapped = selectedKimooxBINs().map((value) => {
        const match = loaded.find((bin) => text(bin, "id") === value || text(bin, "bin") === value);
        return match ? text(match, "bin", text(match, "id")) : value;
      });
      const rewritten = remapped.join(",") !== selectedKimooxBINs().join(",");
      if (rewritten) {
        updateKimooxBINs(remapped);
      }
      setNotice(rewritten
        ? `已读取 ${result.bins.length} 个 Kimoox BIN，已把内部 ID 换成卡 BIN 号，请保存`
        : `已读取 ${result.bins.length} 个 Kimoox BIN`);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setKimooxBinsBusy(false);
    }
  };
  const updateKimooxBINs = (values: string[]) => {
    const unique = Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)));
    update("kimoox_card_bin_ids", unique.join(","));
  };
  return (
    <div className="config-page">
      <LegacyConfigPanel title="第三方代充 API（协议对接）" className="config-gpt">
        <p className="config-description">
          開啟後，前台兌換開通將呼叫第三方 GPT 代充平台提交訂單並輪詢狀態；將不再使用本地開通流程。 銀行卡與代理會從「銀行卡池」「代理池」自動取得並隨訂單提交。
        </p>
        <div className="config-toggle-spaced">
          <ConfigToggle label="啟用第三方代充 API（開啟後不再使用本地開通）" checked={bool(gpt, "enabled")} onChange={(checked) => setGPT({ ...gpt, enabled: checked })} />
        </div>
        <label className="config-field-spaced">
          API 基础地址（Base URL）
          <input value={text(gpt, "base_url")} onChange={(event) => setGPT({ ...gpt, base_url: event.target.value })} className="asset-input" placeholder="https://kc.vpss.eu.cc/" />
        </label>
        <label className="config-field-spaced">
          API Key
          <input type="password" value={text(gpt, "api_key", "")} onChange={(event) => setGPT({ ...gpt, api_key: event.target.value })} className="asset-input" placeholder={bool(gpt, "api_key_saved") ? "已保存，留空保持不变" : "gptk_..."} autoComplete="off" />
          <p className="config-help">{bool(gpt, "api_key_saved") ? "已配置 API Key" : "尚未配置 API Key"}</p>
        </label>
        <p className="config-help config-gpt-plan-hint">套餐（plan_key）将自动同步 CDK 的套餐类型；国家/币种使用协议默认值（PH / PHP），无需填写。</p>
        <div className="config-provider-status">
          <div className="config-status-heading">
            <strong>供應商帳戶狀態</strong>
            <Button variant="outline" onClick={() => void refreshDiagnostics()} disabled={diagnosticsBusy}><RefreshCw className="h-4 w-4" />重新整理</Button>
          </div>
          <div className="config-status-grid">
            {[
              ["可用積分", text(asRow(gptStatus.balance), "credits", "—")],
              ["USD 餘額", text(asRow(gptStatus.balance), "balance_usd", text(asRow(gptStatus.balance), "balance", "—"))],
              ["可用 GPT 套餐", rows(gptStatus.gpt_plans).map((item) => text(item, "name", text(item, "key"))).join("、") || "—"],
              ["積分套餐", rows(gptStatus.credit_plans).map((item) => text(item, "name", text(item, "id"))).join("、") || "—"],
            ].map(([label, value]) => <div key={label}><div className="config-help">{label}</div><div className="config-status-value">{value}</div></div>)}
          </div>
          <div className="config-help config-status-failure">最近失敗代充（僅顯示供應商回傳的卡密前綴）</div>
          <div className="config-status-recent">{rows(gptStatus.recent_orders).length ? rows(gptStatus.recent_orders).map((item) => text(item, "order_id", text(item, "task_id"))).join("、") : "尚未查詢"}</div>
        </div>
        <div className="config-actions-row">
          <Button variant="outline" onClick={() => void runTest(() => testGPTConfig(gpt as JsonMap), "上游连接测试成功")}><Activity className="h-4 w-4" />测试连接</Button>
          <Button onClick={() => void saveGpt()}><Save className="h-4 w-4" />保存 API 配置</Button>
        </div>
      </LegacyConfigPanel>
      <div className="config-grid">
        <LegacyConfigPanel title="并发与维护" className="config-execution">
          <label className="config-field-spaced config-field-large">
            最大并发激活数
            <input type="number" min={1} value={field("max_concurrent_activations", "1")} onChange={(event) => update("max_concurrent_activations", event.target.value)} className="asset-input" placeholder="1" />
            <p className="config-description config-field-note">超过该数量时，前台将提示「当前任务过多，请稍后再试」。</p>
          </label>
          <label className="config-field-spaced config-field-large">
            排队超时（秒）
            <input type="number" min={1} max={86400} value={field("recharge_queued_timeout_seconds", "600")} onChange={(event) => update("recharge_queued_timeout_seconds", event.target.value)} className="asset-input" placeholder="600" />
            <p className="config-description config-field-note">任务在队列中超过该时间未被 Worker 接管，会自动失败并释放 CDK。</p>
          </label>
          <label className="config-field-spaced config-field-large">
            Worker 租约超时（秒）
            <input type="number" min={5} max={3600} value={field("recharge_task_lease_timeout_seconds", "60")} onChange={(event) => update("recharge_task_lease_timeout_seconds", event.target.value)} className="asset-input" placeholder="60" />
            <p className="config-description config-field-note">Worker 心跳超过该时间未更新，任务会自动回收，旧 Worker 不能继续写入状态。</p>
          </label>
          <label className="config-field-spaced config-field-large">
            Checkout 建单方式
            <select
              value={field("checkout_mode", "api")}
              onChange={(event) => update("checkout_mode", event.target.value)}
              className="asset-input"
            >
              <option value="api">API 建单（生成支付链接后打开）</option>
              <option value="ui">定价页 UI（Playwright 点升级）</option>
              <option value="api_then_ui">先 API，失败再回退定价页 UI</option>
            </select>
            <p className="config-description config-field-note">
              影响正式开通与支付链接调试。API 建单快但支付页有时加载失败；定价页 UI 更接近真人升级路径。保存后下一单生效，无需重启。
            </p>
          </label>
          <label className="config-field-spaced config-field-large">
            Worker 浏览器日志级别
            <select
              value={field("worker_log_level", "info")}
              onChange={(event) => update("worker_log_level", event.target.value)}
              className="asset-input"
            >
              <option value="off">关闭浏览器调试日志</option>
              <option value="info">INFO：关键导航、错误和失败请求</option>
              <option value="debug">DEBUG：完整浏览器 console 与网络调试</option>
            </select>
            <p className="config-description config-field-note">
              只影响浏览器调试采集；任务状态、业务步骤和错误日志仍会保留。保存后下一次 Worker 任务生效，无需重启。
            </p>
          </label>
          <div className="config-toggle-block">
            <ConfigToggle label="维护模式" checked={bool(settings, "maintenance_mode")} onChange={(checked) => update("maintenance_mode", checked ? "1" : "0")} />
            <p className="config-description config-field-note">开启后立即拒绝所有新任务。</p>
          </div>
          <div className="config-actions-row">
            <Button onClick={() => void saveSettingsSection(executionConfigKeys, "并发与维护配置已保存")} disabled={busy}><Save className="h-4 w-4" />保存并发与维护配置</Button>
          </div>
        </LegacyConfigPanel>
      </div>
      <LegacyConfigPanel title="银行卡卡池与 Provider" className="config-card-pools">
        <p className="config-description config-description-top">
          支付任务由 Go 后端使用默认卡池，并按下方路由策略选择 Provider。这里主要配置当前使用的 Provider；卡号和 CVC 不会保存到普通配置或管理列表中。
        </p>
        <div className="config-field-grid">
          <label>
            路由策略
            <select value={field("card_pool_routing", "FIXED")} onChange={(event) => update("card_pool_routing", event.target.value)} className="asset-input">
              <option value="FIXED">固定 Provider</option>
              <option value="PRIORITY">按优先级</option>
              <option value="WEIGHTED">按权重</option>
              <option value="FAILOVER">技术故障自动切换</option>
            </select>
          </label>
          <label>
            默认 Provider
            <select value={visibleCardProviders.includes(defaultProvider as typeof visibleCardProviders[number]) ? defaultProvider : ""} onChange={(event) => updateDefaultProvider(event.target.value)} className="asset-input">
              <option value="" disabled>{defaultProvider && !visibleCardProviders.includes(defaultProvider as typeof visibleCardProviders[number]) ? "当前配置的 Provider 暂未在页面开放" : "请选择 Provider"}</option>
              <option value="LOCAL_TEXT" disabled={providerOptionDisabled("LOCAL_TEXT")}>LOCAL_TEXT（本地文本卡池）</option>
              <option value={visibleCardProvider} disabled={providerOptionDisabled(visibleCardProvider)}>KIMOOX</option>
            </select>
          </label>
          <label>
            充值卡创建模式
            <select value={field("card_pool_card_creation_mode", "CREATE_ON_DEMAND")} onChange={(event) => update("card_pool_card_creation_mode", event.target.value)} className="asset-input">
              <option value="POOL_ONLY">使用已有卡池卡片</option>
              <option value="CREATE_ON_DEMAND">充值前调用 Provider 创建新卡</option>
            </select>
            <p className="config-help">POOL_ONLY 使用银行卡池中已有卡；CREATE_ON_DEMAND 每个充值任务先向当前 Provider 申请一张新卡，再交给 Worker 支付。</p>
          </label>
        </div>
        <div className="config-toggle-block">
          <ConfigToggle
            label="支付完成后调用 API 销卡"
            checked={["1", "true"].includes(field("card_pool_cancel_after_payment", "1"))}
            onChange={(checked) => update("card_pool_cancel_after_payment", checked ? "1" : "0")}
            description="开启后，支付成功会调用 Provider 销卡接口（可能产生费用）。关闭后，支付成功只把卡池卡片标为已销毁，不调用销卡 API。无论开关，支付失败都只本地报废，不调用销卡 API。"
          />
        </div>
        <div className="config-toggle-list">
          <ConfigToggle label="启用 LOCAL_TEXT" checked={providerIsEnabled("LOCAL_TEXT")} disabled={defaultProvider === "LOCAL_TEXT"} onChange={(checked) => updateProviderEnabled("LOCAL_TEXT", checked)} description={providerDescription("LOCAL_TEXT", "兼容现有 TXT / CSV 导入卡池；卡片在银行卡池页面管理。")} />
          <ConfigToggle label="启用 KIMOOX" checked={providerIsEnabled(visibleCardProvider)} disabled={defaultProvider === visibleCardProvider} onChange={(checked) => updateProviderEnabled(visibleCardProvider, checked)} description={providerDescription(visibleCardProvider, "Kimoox VCC Open API 适配器，建议先使用文档要求的环境和卡 BIN。")} />
        </div>
        <div className="config-provider-accordions" data-testid="provider-config-accordions">
        <details className="config-provider-section config-provider-local-text" data-testid="local-text-config" open={openProviderConfig === "LOCAL_TEXT"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "LOCAL_TEXT"} aria-controls="provider-config-local-text" onClick={(event) => { event.preventDefault(); toggleProviderConfig("LOCAL_TEXT"); }}>
            <div>
              <h3>LOCAL_TEXT 本地文本卡池配置</h3>
              <p>兼容现有 TXT / CSV 文本导入卡池；卡片导入、启用和删除在「银行卡池」页面完成。</p>
            </div>
            <span className="config-provider-code">LOCAL_TEXT</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-local-text" className="config-provider-content">
          <div className="config-provider-empty">
            <p>LOCAL_TEXT 不需要外部 API 凭据。启用后，充值任务会从已导入且可用的本地卡片中分配银行卡。</p>
            <p>如果需要新增卡片，请打开左侧「银行卡池」并使用文本导入功能；这里仅控制 Provider 是否参与路由。</p>
          </div>
          </div>
        </details>
        <details hidden className="config-provider-section config-provider-airwallex" data-testid="airwallex-config" open={openProviderConfig === "AIRWALLEX"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "AIRWALLEX"} aria-controls="provider-config-airwallex" onClick={(event) => { event.preventDefault(); toggleProviderConfig("AIRWALLEX"); }}>
            <div>
              <h3>Airwallex 发卡配置</h3>
              <p>连接 Airwallex Issuing API 创建和管理支付卡。建议先使用 Sandbox 环境。</p>
            </div>
            <span className="config-provider-code">AIRWALLEX</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-airwallex" className="config-provider-content">
          <div className="config-field-grid">
          <label>
            Airwallex Base URL
            <input value={field("airwallex_base_url", "https://api.sandbox.airwallex.com")} onChange={(event) => update("airwallex_base_url", event.target.value)} className="asset-input" />
          </label>
          <label>
            Airwallex Client ID
            <input type="password" value={field("airwallex_client_id")} onChange={(event) => update("airwallex_client_id", event.target.value)} className="asset-input" placeholder={bool(settings, "airwallexClientIDSaved") ? "已配置，输入新值可替换" : "Client ID"} autoComplete="new-password" />
          </label>
          <label>
            Airwallex API Key
            <input type="password" value={field("airwallex_api_key")} onChange={(event) => update("airwallex_api_key", event.target.value)} className="asset-input" placeholder={bool(settings, "airwallexApiKeySaved") ? "已配置，输入新值可替换" : "API Key"} autoComplete="new-password" />
          </label>
          <label>
            Airwallex Cardholder ID
            <input value={field("airwallex_cardholder_id")} onChange={(event) => update("airwallex_cardholder_id", event.target.value)} className="asset-input" placeholder={bool(settings, "airwallexCardholderIDSavedValue") ? "已配置，输入新值可替换" : "Cardholder ID"} />
          </label>
          <label>
            Airwallex 币种
            <input value={field("airwallex_primary_currency", "USD")} onChange={(event) => update("airwallex_primary_currency", event.target.value)} className="asset-input" maxLength={3} />
          </label>
          <label>
            Airwallex 卡片类型
            <input value={field("airwallex_card_type", "DEBIT")} onChange={(event) => update("airwallex_card_type", event.target.value)} className="asset-input" placeholder="DEBIT" />
          </label>
          <label>
            Airwallex 卡片用途
            <input value={field("airwallex_card_purpose", "COMMERCIAL")} onChange={(event) => update("airwallex_card_purpose", event.target.value)} className="asset-input" placeholder="COMMERCIAL" />
          </label>
          <label>
            Airwallex 创建者标识
            <input value={field("airwallex_created_by", "auto-recharge-platform")} onChange={(event) => update("airwallex_created_by", event.target.value)} className="asset-input" />
          </label>
          <label>
            Airwallex Form Factor
            <select value={field("airwallex_form_factor", "VIRTUAL")} onChange={(event) => update("airwallex_form_factor", event.target.value)} className="asset-input">
              <option value="VIRTUAL">VIRTUAL</option>
              <option value="PHYSICAL">PHYSICAL</option>
            </select>
          </label>
          <label>
            Webhook 容差（秒）
            <input type="number" min={1} max={86400} value={field("airwallex_webhook_tolerance_seconds", "300")} onChange={(event) => update("airwallex_webhook_tolerance_seconds", event.target.value)} className="asset-input" />
          </label>
          </div>
          <div className="config-toggle-list">
            <ConfigToggle label="Airwallex 发卡后自动激活" checked={bool(settings, "airwallex_activate_on_issue")} onChange={(checked) => update("airwallex_activate_on_issue", checked ? "true" : "false")} />
          </div>
          <label className="config-field-spaced">
            Airwallex Webhook Secret
            <input type="password" value={field("airwallex_webhook_secret")} onChange={(event) => update("airwallex_webhook_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "airwallexWebhookSecretSaved") ? "已配置，输入新值可替换" : "Webhook Secret"} autoComplete="new-password" />
          </label>
          </div>
        </details>
        <details hidden className="config-provider-section config-provider-stripe-issuing" data-testid="stripe-issuing-config" open={openProviderConfig === "STRIPE_ISSUING"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "STRIPE_ISSUING"} aria-controls="provider-config-stripe-issuing" onClick={(event) => { event.preventDefault(); toggleProviderConfig("STRIPE_ISSUING"); }}>
            <div>
              <h3>Stripe Issuing 发卡配置</h3>
              <p>这里配置的是 Stripe Issuing 发卡接口，不是平台售卡使用的 Stripe Payments。</p>
            </div>
            <span className="config-provider-code">STRIPE_ISSUING</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-stripe-issuing" className="config-provider-content">
          <div className="config-field-grid">
          <label>
            Stripe Issuing Base URL
            <input value={field("stripe_issuing_base_url", "https://api.stripe.com")} onChange={(event) => update("stripe_issuing_base_url", event.target.value)} className="asset-input" />
          </label>
          <label>
            Stripe Issuing Secret Key
            <input type="password" value={field("stripe_issuing_secret_key")} onChange={(event) => update("stripe_issuing_secret_key", event.target.value)} className="asset-input" placeholder={bool(settings, "stripeIssuingSecretKeySaved") ? "已配置，输入新值可替换" : "sk_test_..."} autoComplete="new-password" />
          </label>
          <label>
            Stripe Issuing Cardholder ID
            <input value={field("stripe_issuing_cardholder_id")} onChange={(event) => update("stripe_issuing_cardholder_id", event.target.value)} className="asset-input" placeholder={bool(settings, "stripeIssuingCardholderIDSavedValue") ? "已配置，输入新值可替换" : "ich_..."} />
          </label>
          <label>
            Stripe Issuing 币种
            <input value={field("stripe_issuing_currency", "USD")} onChange={(event) => update("stripe_issuing_currency", event.target.value)} className="asset-input" maxLength={3} />
          </label>
          <label>
            Stripe Issuing Webhook Secret
            <input type="password" value={field("stripe_issuing_webhook_secret")} onChange={(event) => update("stripe_issuing_webhook_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "stripeIssuingWebhookSecretSaved") ? "已配置，输入新值可替换" : "whsec_..."} autoComplete="new-password" />
          </label>
          <label>
            Webhook 容差（秒）
            <input type="number" min={1} max={86400} value={field("stripe_issuing_webhook_tolerance_seconds", "300")} onChange={(event) => update("stripe_issuing_webhook_tolerance_seconds", event.target.value)} className="asset-input" />
          </label>
          </div>
          </div>
        </details>
        <details hidden className="config-provider-section config-provider-photonpay" data-testid="photonpay-config" open={openProviderConfig === "PHOTONPAY"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "PHOTONPAY"} aria-controls="provider-config-photonpay" onClick={(event) => { event.preventDefault(); toggleProviderConfig("PHOTONPAY"); }}>
            <div>
              <h3>PhotonPay 发卡配置</h3>
              <p>连接 PhotonPay VCC Issuing API。请使用 Sandbox 地址和对应的 RSA 密钥进行联调。</p>
            </div>
            <span className="config-provider-code">PHOTONPAY</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-photonpay" className="config-provider-content">
          <div className="config-field-grid">
            <label>PhotonPay Base URL<input value={field("photonpay_base_url", "https://x-api.sandbox.photontech.cc")} onChange={(event) => update("photonpay_base_url", event.target.value)} className="asset-input" /></label>
            <label>App ID<input type="password" value={field("photonpay_app_id")} onChange={(event) => update("photonpay_app_id", event.target.value)} className="asset-input" placeholder={bool(settings, "photonpayAppIDSavedValue") ? "已配置，输入新值可替换" : "App ID"} autoComplete="new-password" /></label>
            <label>App Secret<input type="password" value={field("photonpay_app_secret")} onChange={(event) => update("photonpay_app_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "photonpayAppSecretSavedValue") ? "已配置，输入新值可替换" : "App Secret"} autoComplete="new-password" /></label>
            <label>Cardholder ID<input value={field("photonpay_cardholder_id")} onChange={(event) => update("photonpay_cardholder_id", event.target.value)} className="asset-input" placeholder={bool(settings, "photonpayCardholderIDSavedValue") ? "已配置，输入新值可替换" : "Cardholder ID"} /></label>
            <label>Account ID（可选）<input value={field("photonpay_account_id")} onChange={(event) => update("photonpay_account_id", event.target.value)} className="asset-input" /></label>
            <label>Member ID（可选）<input value={field("photonpay_member_id")} onChange={(event) => update("photonpay_member_id", event.target.value)} className="asset-input" /></label>
            <label>Matrix Account（可选）<input value={field("photonpay_matrix_account")} onChange={(event) => update("photonpay_matrix_account", event.target.value)} className="asset-input" /></label>
            <label>卡 BIN<input value={field("photonpay_card_bin")} onChange={(event) => update("photonpay_card_bin", event.target.value)} className="asset-input" placeholder="Provider 分配的 BIN" /></label>
            <label>卡类型<input value={field("photonpay_card_type", "recharge")} onChange={(event) => update("photonpay_card_type", event.target.value)} className="asset-input" /></label>
            <label>卡片用途<input value={field("photonpay_nickname")} onChange={(event) => update("photonpay_nickname", event.target.value)} className="asset-input" placeholder="卡片备注（可选）" /></label>
            <label>Card Scheme<input value={field("photonpay_card_scheme", "Discover")} onChange={(event) => update("photonpay_card_scheme", event.target.value)} className="asset-input" /></label>
            <label>卡片形态<select value={field("photonpay_card_form_factor", "virtual_card")} onChange={(event) => update("photonpay_card_form_factor", event.target.value)} className="asset-input"><option value="virtual_card">virtual_card</option><option value="physical_card">physical_card</option></select></label>
            <label>币种<input value={field("photonpay_primary_currency", "USD")} onChange={(event) => update("photonpay_primary_currency", event.target.value)} className="asset-input" maxLength={3} /></label>
            <label>交易限额类型<select value={field("photonpay_transaction_limit_type", "unlimited")} onChange={(event) => update("photonpay_transaction_limit_type", event.target.value)} className="asset-input"><option value="unlimited">unlimited</option><option value="limited">limited</option></select></label>
            <label>Webhook 容差（秒）<input type="number" min={1} max={86400} value={field("photonpay_webhook_tolerance_seconds", "300")} onChange={(event) => update("photonpay_webhook_tolerance_seconds", event.target.value)} className="asset-input" /></label>
          </div>
          <label className="config-field-spaced">RSA Private Key<input type="password" value={field("photonpay_private_key")} onChange={(event) => update("photonpay_private_key", event.target.value)} className="asset-input" placeholder={bool(settings, "photonpayPrivateKeySavedValue") ? "已配置，输入新值可替换" : "PKCS#8 / PKCS#1 PEM"} autoComplete="new-password" /></label>
          <label className="config-field-spaced">Webhook Public Key<textarea value={field("photonpay_webhook_public_key")} onChange={(event) => update("photonpay_webhook_public_key", event.target.value)} className={areaClass} placeholder={bool(settings, "photonpayWebhookPublicKeySavedValue") ? "已配置，输入新值可替换" : "RSA public key PEM"} autoComplete="off" /></label>
          </div>
        </details>
        <details hidden className="config-provider-section config-provider-dogpay" data-testid="dogpay-config" open={openProviderConfig === "DOGPAY"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "DOGPAY"} aria-controls="provider-config-dogpay" onClick={(event) => { event.preventDefault(); toggleProviderConfig("DOGPAY"); }}>
            <div>
              <h3>DogPay 发卡配置</h3>
              <p>连接 DogPay Card Issuing API。请使用 Sandbox 地址，并填写已创建的 Cardholder ID。</p>
            </div>
            <span className="config-provider-code">DOGPAY</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-dogpay" className="config-provider-content">
          <div className="config-field-grid">
            <label>DogPay Base URL<input value={field("dogpay_base_url", "https://sandbox-api-v2.dogpay.com")} onChange={(event) => update("dogpay_base_url", event.target.value)} className="asset-input" /></label>
            <label>App ID<input type="password" value={field("dogpay_appid")} onChange={(event) => update("dogpay_appid", event.target.value)} className="asset-input" placeholder={bool(settings, "dogpayAppIDSavedValue") ? "已配置，输入新值可替换" : "App ID"} autoComplete="new-password" /></label>
            <label>App Secret<input type="password" value={field("dogpay_secret")} onChange={(event) => update("dogpay_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "dogpaySecretSavedValue") ? "已配置，输入新值可替换" : "App Secret"} autoComplete="new-password" /></label>
            <label>Channel ID<input value={field("dogpay_channel_id")} onChange={(event) => update("dogpay_channel_id", event.target.value)} className="asset-input" placeholder="Channel ID" /></label>
            <label>Entity ID（可选）<input value={field("dogpay_entity_id")} onChange={(event) => update("dogpay_entity_id", event.target.value)} className="asset-input" /></label>
            <label>Cardholder ID<input value={field("dogpay_cardholder_id")} onChange={(event) => update("dogpay_cardholder_id", event.target.value)} className="asset-input" placeholder={bool(settings, "dogpayCardholderIDSavedValue") ? "已配置，输入新值可替换" : "Cardholder ID"} /></label>
            <label>卡类型<input value={field("dogpay_card_type", "virtual")} onChange={(event) => update("dogpay_card_type", event.target.value)} className="asset-input" /></label>
            <label>Budget ID（可选）<input value={field("dogpay_budget_id")} onChange={(event) => update("dogpay_budget_id", event.target.value)} className="asset-input" /></label>
            <label>累计消费限额（可选）<input type="number" min={0} step="0.01" value={field("dogpay_velocity_amount_limit", "0")} onChange={(event) => update("dogpay_velocity_amount_limit", event.target.value)} className="asset-input" placeholder="0 表示不限制" /></label>
            <label>Webhook 容差（秒）<input type="number" min={1} max={86400} value={field("dogpay_webhook_tolerance_seconds", "300")} onChange={(event) => update("dogpay_webhook_tolerance_seconds", event.target.value)} className="asset-input" /></label>
          </div>
          <label className="config-field-spaced">RSA Private Key<input type="password" value={field("dogpay_private_key")} onChange={(event) => update("dogpay_private_key", event.target.value)} className="asset-input" placeholder={bool(settings, "dogpayPrivateKeySavedValue") ? "已配置，输入新值可替换" : "PKCS#8 / PKCS#1 PEM"} autoComplete="new-password" /></label>
          <label className="config-field-spaced">Webhook Secret（可选，默认使用 App ID）<input type="password" value={field("dogpay_webhook_secret")} onChange={(event) => update("dogpay_webhook_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "dogpayWebhookSecretSavedValue") ? "已配置，输入新值可替换" : "Webhook Secret"} autoComplete="new-password" /></label>
          </div>
        </details>
        <details className="config-provider-section config-provider-kimoox" data-testid="kimoox-config" open={openProviderConfig === "KIMOOX"}>
          <summary className="config-provider-section-heading config-provider-summary" aria-expanded={openProviderConfig === "KIMOOX"} aria-controls="provider-config-kimoox" onClick={(event) => { event.preventDefault(); toggleProviderConfig("KIMOOX"); }}>
            <div>
              <h3>Kimoox 发卡配置</h3>
              <p>连接 Kimoox VCC Open API。当前文档公开接口统一使用 HMAC-SHA256 签名，建议先填写测试环境对应的 API 凭据和卡 BIN 号。</p>
            </div>
            <span className="config-provider-code">KIMOOX</span>
            <ChevronDown className="config-provider-chevron" aria-hidden="true" />
          </summary>
          <div id="provider-config-kimoox" className="config-provider-content">
          <div className="config-field-grid">
            <label>Kimoox Base URL<input value={field("kimoox_base_url", "https://card.kimoox.com")} onChange={(event) => update("kimoox_base_url", event.target.value)} className="asset-input" /></label>
            <label>API Key<input type="password" value={field("kimoox_api_key")} onChange={(event) => update("kimoox_api_key", event.target.value)} className="asset-input" placeholder={bool(settings, "kimooxAPIKeySavedValue") ? "已配置，输入新值可替换" : "API Key"} autoComplete="new-password" /></label>
            <label>API Secret<input type="password" value={field("kimoox_api_secret")} onChange={(event) => update("kimoox_api_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "kimooxAPISecretSavedValue") ? "已配置，输入新值可替换" : "API Secret"} autoComplete="new-password" /></label>
            <label className="config-field-span-2">Card BIN（可多选）
              {kimooxBins.length ? <div className="config-multiselect" data-testid="kimoox-bin-options">
                {kimooxBins.map((bin) => {
                  const id = text(bin, "id", text(bin, "binId"));
                  const binNumber = text(bin, "bin", id);
                  const checked = selectedKimooxBINs().includes(binNumber) || selectedKimooxBINs().includes(id);
                  return <label key={id} className="config-multiselect-option"><input type="checkbox" checked={checked} onChange={(event) => updateKimooxBINs(event.target.checked ? [...selectedKimooxBINs().filter((value) => value !== id), binNumber] : selectedKimooxBINs().filter((value) => value !== binNumber && value !== id))} /><span>{binNumber}{text(bin, "cardType", "") ? ` · ${text(bin, "cardType")}` : ""}</span></label>;
                })}
              </div> : null}
              <div className="config-actions-row config-actions-row-inline"><Button type="button" variant="outline" onClick={() => void loadKimooxBINs()} disabled={kimooxBinsBusy}><RefreshCw className={cn("h-4 w-4", kimooxBinsBusy && "animate-spin")} />{kimooxBinsBusy ? "读取中…" : "读取可用 BIN"}</Button></div>
              <input value={field("kimoox_card_bin_ids")} onChange={(event) => update("kimoox_card_bin_ids", event.target.value)} className="asset-input" placeholder="例如 40024200,40041606" />
              <p className="config-help">填写卡 BIN 号即可，例如 40024200,40041606。开卡时会自动换成 Kimoox 的 BIN ID。也可点读取后勾选。</p>
            </label>
            <label>卡类型<select value={field("kimoox_card_type", "PREPAID")} onChange={(event) => update("kimoox_card_type", event.target.value)} className="asset-input"><option value="PREPAID">PREPAID 储值卡</option><option value="BUDGET">BUDGET 预算卡</option></select><p className="config-help">PREPAID 需要首充金额；BUDGET 需要同时填写 Card Group ID 和 Budget ID。</p></label>
            <label>PREPAID 首充金额<select value={field("kimoox_prepaid_amount_mode", "PLAN_PLUS_5")} onChange={(event) => update("kimoox_prepaid_amount_mode", event.target.value)} className="asset-input"><option value="PLAN_PLUS_5">套餐美元标价 + 5（Plus 25 / Pro 5x 105 / Pro 20x 205）</option><option value="FIXED">固定金额</option></select><p className="config-help">充值任务自动开卡使用此规则，店内 CNY 售价不参与。手动创建虚拟卡必须填写首充金额。</p></label>
            {field("kimoox_prepaid_amount_mode", "PLAN_PLUS_5") === "FIXED" ? <label>固定首充金额（USD）<input type="number" min={0.01} max={100000} step="0.01" value={field("kimoox_prepaid_recharge_amount")} onChange={(event) => update("kimoox_prepaid_recharge_amount", event.target.value)} className="asset-input" /></label> : null}
            <label>Cardholder ID（可选）<input value={field("kimoox_cardholder_id")} onChange={(event) => update("kimoox_cardholder_id", event.target.value)} className="asset-input" placeholder="数字 ID；也可使用 Holder ID" /></label>
            <label>Holder ID（可选）<input value={field("kimoox_holder_id")} onChange={(event) => update("kimoox_holder_id", event.target.value)} className="asset-input" /></label>
            <label>Card Group ID（BUDGET 必填）<input disabled={field("kimoox_card_type", "PREPAID") !== "BUDGET"} value={field("kimoox_card_group_id")} onChange={(event) => update("kimoox_card_group_id", event.target.value)} className="asset-input" /></label>
            <label>Budget ID（BUDGET 必填）<input disabled={field("kimoox_card_type", "PREPAID") !== "BUDGET"} value={field("kimoox_budget_id")} onChange={(event) => update("kimoox_budget_id", event.target.value)} className="asset-input" /></label>
            <label>Webhook 容差（秒）<input type="number" min={1} max={86400} value={field("kimoox_webhook_tolerance_seconds", "300")} onChange={(event) => update("kimoox_webhook_tolerance_seconds", event.target.value)} className="asset-input" /></label>
            <label>开卡轮询次数<input type="number" min={1} max={300} value={field("kimoox_apply_poll_attempts", "30")} onChange={(event) => update("kimoox_apply_poll_attempts", event.target.value)} className="asset-input" /></label>
            <label>开卡轮询间隔（秒）<input type="number" min={0} max={300} value={field("kimoox_apply_poll_interval_seconds", "2")} onChange={(event) => update("kimoox_apply_poll_interval_seconds", event.target.value)} className="asset-input" /></label>
          </div>
          <label className="config-field-spaced">Webhook Secret<input type="password" value={field("kimoox_webhook_secret")} onChange={(event) => update("kimoox_webhook_secret", event.target.value)} className="asset-input" placeholder={bool(settings, "kimooxWebhookSecretSavedValue") ? "已配置，输入新值可替换" : "Webhook Secret"} autoComplete="new-password" /></label>
          <p className="config-description config-field-note">Webhook Secret 用于验证 Kimoox 回调并解密回调中的敏感卡信息；保存后不会回显。没有它时，Provider 仍可读取公开卡元数据，但不能安全取得完整卡号、有效期和 CVV。</p>
          </div>
        </details>
        </div>
        <div className="config-actions-row">
          <Button onClick={() => void saveSettingsSection(providerConfigKeys, "卡池与 Provider 配置已保存")} disabled={busy}><Save className="h-4 w-4" />保存卡池与 Provider 配置</Button>
        </div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="Telegram 通知" className="config-telegram">
        <p className="config-description config-description-top">开通成功、失败或卡池耗尽时可推送到管理员私聊和/或群组。消息包含邮箱与 CDK。</p>
        <label className="config-field-spaced">Bot Token<input type="password" value={editableConfigValue(telegram, "bot_token")} onChange={(event) => setTelegram({ ...telegram, bot_token: event.target.value })} className="asset-input" placeholder={bool(telegram, "bot_token_saved") ? "已保存（" + text(telegram, "bot_token_preview") + "）留空不修改" : "123456789:ABCdefGHI..."} autoComplete="off" /></label>
        <label className="config-field-spaced">管理员 Chat ID<input value={editableConfigValue(telegram, "admin_chat_id")} onChange={(event) => setTelegram({ ...telegram, admin_chat_id: event.target.value })} className="asset-input" placeholder="例如 123456789" /></label>
        <label className="config-field-spaced">群组 Chat ID<input value={editableConfigValue(telegram, "group_chat_id")} onChange={(event) => setTelegram({ ...telegram, group_chat_id: event.target.value })} className="asset-input" placeholder="例如 -1001234567890" /></label>
        <div className="config-toggle-list">
          {[["notify_admin", "通知管理员"], ["notify_group", "通知群组"], ["on_success", "开通成功时通知"], ["on_failure", "开通失败时通知"], ["on_card_pool_empty", "卡池耗尽时通知"]].map(([key, label]) => <ConfigToggle key={key} label={label} checked={bool(telegram, key)} onChange={(checked) => setTelegram({ ...telegram, [key]: checked })} />)}
        </div>
        <div className="config-actions-row"><Button variant="outline" onClick={() => void runTest(testTelegram, "Telegram 测试消息已发送")}><Send className="h-4 w-4" />发送测试消息</Button><Button onClick={() => void saveTG()}><Save className="h-4 w-4" />保存 Telegram 配置</Button></div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="邮件通知" className="config-email">
        <p className="config-description config-description-top">配置 SMTP 后，购买成功会把 CDK 发送到订单邮箱，兑换成功后会发送开通结果。发送由 Go 后端完成，SMTP 密码不会回显。</p>
        <ConfigToggle label="启用邮件通知" checked={bool(settings, "emailEnabled")} onChange={(checked) => update("emailEnabled", checked ? "true" : "false")} />
        <div className="config-field-grid">
          <label>SMTP 主机<input value={field("emailSMTPHost")} onChange={(event) => update("emailSMTPHost", event.target.value)} className="asset-input" placeholder="smtp.example.com" /></label>
          <label>SMTP 端口<input type="number" min={1} max={65535} value={field("emailSMTPPort", "587")} onChange={(event) => update("emailSMTPPort", event.target.value)} className="asset-input" /></label>
          <label>SMTP 用户名<input value={field("emailSMTPUsername")} onChange={(event) => update("emailSMTPUsername", event.target.value)} className="asset-input" placeholder="账号或邮箱" autoComplete="off" /></label>
          <label>SMTP 密码<input type="password" value={field("emailSMTPPassword")} onChange={(event) => update("emailSMTPPassword", event.target.value)} className="asset-input" placeholder={bool(settings, "emailSMTPPasswordSavedValue") ? "已配置，留空保持不变" : "SMTP 密码"} autoComplete="new-password" /></label>
          <label>发件人邮箱<input value={field("emailSMTPFrom")} onChange={(event) => update("emailSMTPFrom", event.target.value)} className="asset-input" placeholder="no-reply@example.com" /></label>
          <label>发件人名称<input value={field("emailSMTPFromName")} onChange={(event) => update("emailSMTPFromName", event.target.value)} className="asset-input" placeholder="KC GPT自动充值系统" /></label>
          <label>站点名称<input value={field("emailSiteName", "KC GPT自动充值系统")} onChange={(event) => update("emailSiteName", event.target.value)} className="asset-input" /></label>
          <label>SMTP 超时（秒）<input type="number" min={1} max={300} value={field("emailSMTPTimeoutSeconds", "10")} onChange={(event) => update("emailSMTPTimeoutSeconds", event.target.value)} className="asset-input" /></label>
        </div>
        <div className="config-toggle-list">
          <ConfigToggle label="购买成功发送 CDK" checked={bool(settings, "emailNotifyPurchase")} onChange={(checked) => update("emailNotifyPurchase", checked ? "true" : "false")} />
          <ConfigToggle label="兑换成功发送通知" checked={bool(settings, "emailNotifyRedeem")} onChange={(checked) => update("emailNotifyRedeem", checked ? "true" : "false")} />
          <ConfigToggle label="启用 STARTTLS" checked={bool(settings, "emailSMTPUseTLS")} onChange={(checked) => update("emailSMTPUseTLS", checked ? "true" : "false")} description="587 端口通常需要开启；关闭后使用明文 SMTP 连接。" />
        </div>
        <label className="config-field-spaced">测试收件人邮箱<input type="email" value={emailTestRecipient} onChange={(event) => setEmailTestRecipient(event.target.value)} className="asset-input" placeholder="填写接收测试邮件的地址" autoComplete="email" /></label>
        <div className="config-actions-row"><Button variant="outline" onClick={() => void testEmail()}><Mail className="h-4 w-4" />发送测试邮件</Button><Button onClick={() => void saveSettingsSection(emailConfigKeys, "邮件配置已保存")} disabled={busy}><Save className="h-4 w-4" />保存邮件配置</Button></div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="hCaptcha 自动求解" className="config-hcaptcha">
        <p className="config-description config-description-top">支付时遇到 Stripe「One more step」人机验证：优先使用打码平台（createTask/getTaskResult）兜底 passive checkbox；图片题可继续尝试内置视觉求解器（VLM + CLIP/OpenCV）。</p>
        <div className="config-toggle-list config-toggle-list-tight">
          <ConfigToggle label="启用自动求解" checked={bool(hcaptcha, "enabled")} onChange={(checked) => setHcaptcha({ ...hcaptcha, enabled: checked })} />
          <ConfigToggle label="禁用 VLM（仅用 CLIP/OpenCV）" checked={bool(hcaptcha, "no_vlm")} onChange={(checked) => setHcaptcha({ ...hcaptcha, no_vlm: checked })} description="无 API Key 或想省费用时可开启，成功率较低" />
        </div>
        <label className="config-field-spaced">打码平台 API Key（推荐）<input type="password" value={editableConfigValue(hcaptcha, "captcha_platform_api_key")} onChange={(event) => setHcaptcha({ ...hcaptcha, captcha_platform_api_key: event.target.value })} className="asset-input" placeholder={bool(hcaptcha, "captcha_platform_api_key_saved") ? "已保存（" + text(hcaptcha, "captcha_platform_api_key_preview") + "）留空不修改" : "clientKey（也可写在 .env 的 HCAPTCHA_CAPTCHA_PLATFORM_API_KEY）"} autoComplete="off" /><p className="config-help">与 <a href="https://anti-captcha.com/zh/apidoc/methods/createTask" target="_blank" rel="noreferrer">Anti-Captcha API</a> 相同协议（createTask / getTaskResult / getBalance）。程序请求 <code>https://api.anti-captcha.com</code>，不是官网首页。填错 Capsolver 地址时会根据 Key 自动纠正。</p></label>
        <div className="config-field-grid"><div className="form-group"><label>打码平台 API URL</label><input value={text(hcaptcha, "captcha_platform_api_url")} onChange={(event) => setHcaptcha({ ...hcaptcha, captcha_platform_api_url: event.target.value })} className="asset-input" placeholder="https://api.anti-captcha.com" /></div><div className="form-group"><label>打码平台超时（秒）</label><input type="number" value={text(hcaptcha, "captcha_platform_timeout", "180")} onChange={(event) => setHcaptcha({ ...hcaptcha, captcha_platform_timeout: event.target.value })} className="asset-input" /></div></div>
        <label className="config-field-spaced">VLM API Key（可选）<input type="password" value={editableConfigValue(hcaptcha, "vlm_api_key")} onChange={(event) => setHcaptcha({ ...hcaptcha, vlm_api_key: event.target.value })} className="asset-input" placeholder={bool(hcaptcha, "vlm_api_key_saved") ? "已保存（" + text(hcaptcha, "vlm_api_key_preview") + "）留空不修改" : "sk-...（也可写在 .env 的 HCAPTCHA_VLM_API_KEY）"} autoComplete="off" /></label>
        <label className="config-field-spaced">VLM Base URL<input value={text(hcaptcha, "vlm_base_url", "https://api.openai.com/v1")} onChange={(event) => setHcaptcha({ ...hcaptcha, vlm_base_url: event.target.value })} className="asset-input" /></label>
        <label className="config-field-spaced">VLM 模型<input value={text(hcaptcha, "vlm_model", "gpt-5.5")} onChange={(event) => setHcaptcha({ ...hcaptcha, vlm_model: event.target.value })} className="asset-input" /></label>
        <div className="config-field-grid config-field-grid-three"><div className="form-group"><label>VLM 超时（秒）</label><input type="number" value={text(hcaptcha, "vlm_timeout", "45")} onChange={(event) => setHcaptcha({ ...hcaptcha, vlm_timeout: event.target.value })} className="asset-input" /></div><div className="form-group"><label>求解总超时（秒）</label><input type="number" value={text(hcaptcha, "solver_timeout", "240")} onChange={(event) => setHcaptcha({ ...hcaptcha, solver_timeout: event.target.value })} className="asset-input" /></div><div className="form-group"><label>CDP 调试端口</label><input type="number" value={text(hcaptcha, "cdp_port", "9222")} onChange={(event) => setHcaptcha({ ...hcaptcha, cdp_port: event.target.value })} className="asset-input" /></div></div>
        <p className="config-description config-field-note">建议配置打码平台 API Key（推荐）。也可将 Key 写入 .env 的 HCAPTCHA_CAPTCHA_PLATFORM_API_KEY。</p>
        <div className="config-actions-row config-actions-wrap"><Button variant="outline" onClick={() => void refreshDiagnostics()} disabled={diagnosticsBusy}><RefreshCw className="h-4 w-4" />刷新验证日志</Button><Button variant="outline" onClick={() => void runTest(() => testHcaptcha("test", hcaptcha as JsonMap), "Solver 测试完成")}><Activity className="h-4 w-4" />检测 Solver 环境</Button><Button variant="outline" onClick={() => void runTest(() => testHcaptcha("test-vlm", hcaptcha as JsonMap), "VLM 测试完成")}><Bot className="h-4 w-4" />测试 VLM 连通性</Button><Button variant="outline" onClick={() => void runTest(() => testHcaptcha("test-captcha-platform", hcaptcha as JsonMap), "打码平台测试完成")}><ShieldCheck className="h-4 w-4" />测试打码平台</Button><Button onClick={() => void saveCaptcha()}><Save className="h-4 w-4" />保存 hCaptcha 配置</Button></div>
        <div className="config-log-panel"><label>验证 / Solver 日志</label><pre>{captchaLogError || (Array.isArray(captchaLogs.lines) ? captchaLogs.lines.join("\n") : rows(captchaLogs.runtime).map((item) => text(item, "text")).join("\n") || "暂无 hCaptcha 日志")}</pre></div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="修改后台密码" className="config-password">
        <label className="config-field-spaced">原密码<input type="password" value={adminPassword.current} onChange={(event) => setAdminPassword({ ...adminPassword, current: event.target.value })} className="asset-input" placeholder="请输入原密码" autoComplete="current-password" /></label>
        <label className="config-field-spaced">新密码<input type="password" value={adminPassword.next} onChange={(event) => setAdminPassword({ ...adminPassword, next: event.target.value })} className="asset-input" placeholder="请输入新密码，至少 6 位" autoComplete="new-password" /></label>
        <Button className="config-button-spaced" onClick={() => void updatePassword()}><KeyRound className="h-4 w-4" />修改密码</Button>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="安全设置" className="config-security">
        <p className="config-description config-description-top">登录支持 Google Authenticator 或 Telegram 验证码。</p>
        <div className="config-security-box">
          <div className="form-group config-security-field"><label>登录入口路径</label><input value={pathForm.login} onChange={(event) => setPathForm({ ...pathForm, login: event.target.value })} className="asset-input" /><p className="config-help">完整登录地址：{typeof window !== "undefined" ? window.location.origin : ""}/{pathForm.login}</p></div>
          <div className="form-group config-security-field"><label>管理后台路径</label><input value={pathForm.panel} onChange={(event) => setPathForm({ ...pathForm, panel: event.target.value })} className="asset-input" /><p className="config-help">完整后台地址：{typeof window !== "undefined" ? window.location.origin : ""}/{pathForm.panel}</p></div>
          <p className="config-help config-security-help">仅支持小写字母、数字与连字符（2–32 字符）。保存后请收藏新地址，默认 <code>/admin-login</code>、<code>/admin</code> 将失效。</p>
          <Button variant="outline" onClick={() => void savePaths()}><Globe className="h-4 w-4" />保存入口路径</Button>
        </div>
        <div className="config-security-divider">
          <div className="form-group config-security-2fa"><label>登录二次验证方式</label><select value={text(securityStatus, "login2faMode", "either")} onChange={(event) => setSecurityStatus({ ...securityStatus, login2faMode: event.target.value })} className="asset-input"><option value="either">登录时可切换（Google Authenticator / Telegram）</option><option value="totp">仅 Google Authenticator</option><option value="telegram">仅 Telegram 验证码</option></select><Button className="config-inline-button" variant="outline" onClick={() => void save2FAModeValue()}><Save className="h-4 w-4" />保存验证方式</Button></div>
          <p className="config-security-status">当前账号 {text(securityStatus, "email", "admin@example.com")} · 2FA: {bool(securityStatus, "totpEnabled") ? "已启用" : "未启用"} · 已配置: {bool(securityStatus, "totpEnabled") ? "已配置" : "未配置"} · 策略: {text(securityStatus, "login2faModeLabel", "登录时可切换")}</p>
          <div className="config-security-actions"><Button variant="outline" onClick={() => void setup()}><Smartphone className="h-4 w-4" />绑定 Google Authenticator</Button>{" "}<Button variant="danger" className="btn-delete" style={{ marginLeft: "8px" }} onClick={() => void disable()}><ShieldOff className="h-4 w-4" />关闭 2FA</Button></div>
          {text(totp, "secret", "") ? <div className="config-totp-box"><img src={text(totp, "qrCodeUrl", text(totp, "qr_code", ""))} alt="Google Authenticator QR" /><div className="config-totp-secret"><div>请用 Google Authenticator 扫描上方二维码</div><div>手动密钥：<code>{text(totp, "secret", "")}</code></div></div><input type="text" inputMode="numeric" maxLength={6} value={text(totp, "code", "")} onChange={(event) => setTotp({ ...totp, code: event.target.value })} className="asset-input" placeholder="输入 Authenticator 6 位码确认" /><Button className="config-inline-button" onClick={() => void confirm()}>确认启用</Button></div> : null}
        </div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="默认支付地区（兼容旧商品）">
        <p className="config-description config-description-top">仅用于旧商品未配置支付地区时的默认值，以及支付链接调试的初始值。已配置商品地区时，以商品自身地区为准；账单地址请在左侧「免税地址」页面管理。</p>
        <div className="config-region-row">
          <SearchableRegionSelect
            id="global_payment_region_selector"
            label="默认支付地区"
            value={region}
            options={regionOptions}
            onChange={setRegion}
            labelClassName="config-region-select-label"
          />
          <Button onClick={() => void saveRegionValue()}><Save className="h-4 w-4" />保存地区</Button>
          <span className="status-badge status-success">当前: {text(regionInfo, "label", region)} / {text(regionInfo, "currency", "")}</span>
        </div>
      </LegacyConfigPanel>
      <LegacyConfigPanel title="平台售卡与 Stripe" className="config-platform">
        <p className="config-description config-description-top">平台购买调试模式仅影响站内 CDK 售卡；Worker 支付流程仍按原版执行。</p>
        <ConfigToggle label="平台购买调试模式（直接发 CDK）" checked={bool(settings, "storeDebugMode")} onChange={(checked) => update("storeDebugMode", checked ? "true" : "false")} />
        <div className="config-field-grid"><label>Stripe Secret Key<input type="password" value={field("stripeSecretKey")} onChange={(event) => update("stripeSecretKey", event.target.value)} placeholder={bool(settings, "stripeSecretKeySavedValue") ? "已配置，输入新值可替换" : "sk_test_..."} className="asset-input" autoComplete="new-password" /></label><label>Stripe Webhook Secret<input type="password" value={field("stripeWebhookSecret")} onChange={(event) => update("stripeWebhookSecret", event.target.value)} placeholder={bool(settings, "stripeWebhookSecretSavedValue") ? "已配置，输入新值可替换" : "whsec_..."} className="asset-input" autoComplete="new-password" /></label></div>
        <div className="config-field-grid"><label>支付成功回跳 URL<input value={field("stripeSuccessURL")} onChange={(event) => update("stripeSuccessURL", event.target.value)} className="asset-input" /></label><label>取消支付回跳 URL<input value={field("stripeCancelURL")} onChange={(event) => update("stripeCancelURL", event.target.value)} className="asset-input" /></label></div>
        <Button className="config-button-spaced" onClick={() => void saveSettingsSection(storePaymentConfigKeys, "售卡支付配置已保存")} disabled={busy}><Save className="h-4 w-4" />保存售卡支付配置</Button>
      </LegacyConfigPanel>
    </div>
  );
}
function ProxyPanel({
  proxyRows,
  setProxyRows,
  proxyMeta,
  setProxyMeta,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = async () => {
    try {
      const result = asRow(await getProxies());
      setProxyRows(rows(result.proxies));
      setProxyMeta(asRow(result.summary));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const add = async () => {
    setBusy(true);
    try {
      const result = asRow(await addProxies({ proxies: input }));
      setNotice(text(result, "message", "代理已保存"));
      setInput("");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };
  const toggle = async (row: Row) => {
    try {
      await toggleProxy(text(row, "id"));
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const check = async (row: Row) => {
    try {
      const result = asRow(await testProxy(text(row, "id")));
      setNotice(
        bool(result, "ok")
          ? "代理检测成功"
          : text(result, "error", "代理不可用"),
      );
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const checkAll = async () => {
    setBusy(true);
    try {
      const result = asRow(await testAllProxies());
      await refresh();
      setNotice(text(result, "message", "代理活跃检测已完成"));
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };
  const remove = async (row: Row) => {
    if (!(await confirm("确定删除此代理吗？", "删除代理"))) return;
    try {
      await deleteProxy(text(row, "id"));
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  return (
    <div>
      <Panel
        title="添加代理"
        className="proxy-panel proxy-input-panel"
      >
        <p className="proxy-description">
          粘贴一条或多条代理 URL（一行一条），点保存后立即写入代理池并在下方列表显示。
        </p>
        <textarea
          rows={6}
          value={input}
          onChange={(event) => setInput(event.target.value)}
          className="proxy-area asset-input"
          placeholder={'一行一条，例如：\nhttp://user:pass@host:port\nsocks5://user:pass@host:port\nhttp://USER-session-{session}:PASS@proxy.example.com:1000'}
        />
        <div className="proxy-actions">
          <Button className="proxy-action-button" onClick={() => void add()} disabled={busy}>
            <Plus className="h-4 w-4" />
            保存到代理池
          </Button>
          <Button className="proxy-action-button" variant="outline" onClick={() => setInput("")}>
            清空输入
          </Button>
        </div>
      </Panel>
      <Panel
        title="已保存代理"
        className="proxy-panel proxy-list-panel"
        actions={
          <>
            <Button className="proxy-action-button" variant="outline" onClick={() => void refresh()}>
              <RefreshCw className="h-4 w-4" />刷新
            </Button>
            <Button className="proxy-action-button" variant="outline" onClick={() => void checkAll()} disabled={busy}>
              <Zap className="h-4 w-4" />批量检测活跃
            </Button>
          </>
        }
      >
        <DataTable
          data={proxyRows}
          empty="暂无代理，请在上方粘贴 URL 后点击「保存到代理池」"
          minWidth={760}
          tableClassName="proxy-table"
          columns={[
            {
              key: "active",
              label: "启用",
              width: 72,
              headerClassName: "text-center",
              cellClassName: "text-center",
              render: (row) => (
                <label className="toggle-control justify-center">
                  <input
                    type="checkbox"
                    checked={bool(row, "is_active")}
                    onChange={() => void toggle(row)}
                    className="toggle-input"
                  />
                  <span className="toggle-switch" />
                </label>
              ),
            },
            {
              key: "check",
              label: "活跃检测",
              width: 96,
              headerClassName: "text-center",
              cellClassName: "text-center",
              render: (row) => (
                <Badge label={text(row, "check_label", "未检测")} tone={backendTone(row, "check_tone")} />
              ),
            },
            {
              key: "ip",
              label: "出口 IP",
              width: 140,
              render: (row) => <code>{text(row, "ip_text", "—")}</code>,
            },
            {
              key: "latency",
              label: "延迟",
              width: 88,
              headerClassName: "text-center",
              cellClassName: "text-center",
              render: (row) => <span className="text-xs">{text(row, "latency_text", "—")}</span>,
            },
            {
              key: "protocol",
              label: "协议",
              width: 88,
              headerClassName: "text-center",
              cellClassName: "text-center",
              render: (row) => <code>{text(row, "protocol", "-") || "-"}</code>,
            },
            {
              key: "proxy_url_masked",
              label: "代理地址",
              render: (row) => (
                <code className="break-all text-xs">
                  {text(row, "proxy_url_masked", text(row, "proxy_url"))}
                </code>
              ),
            },
            {
              key: "actions",
              label: "操作",
              width: 180,
              headerClassName: "text-center",
              cellClassName: "text-center",
              render: (row) => (
                <div className="table-action-group">
                  <Button
                    size="sm"
                    variant="outline"
                    className="proxy-row-check"
                    onClick={() => void check(row)}
                  >
                    检测
                  </Button>
                  <button
                    type="button"
                    title="删除代理"
                    onClick={() => void remove(row)}
                    className="btn-delete proxy-row-delete"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </div>
              ),
            },
          ]}
        />
        <p className="proxy-summary">共 {num(proxyMeta, "total", proxyRows.length)} 条，启用 {num(proxyMeta, "active")} 条</p>
      </Panel>
    </div>
  );
}

function BrowserPoolPanel({
  browserPool,
  setBrowserPool,
  setNotice,
  setError,
}: ContentProps) {
  const pool = child(browserPool, "pool");
  const view = child(browserPool, "view");
  const modeEnabled = bool(browserPool, "enabled");
  const status = text(view, "statusLabel", "未就绪");
  const [size, setSize] = useState(
    text(pool, "configuredSize", text(pool, "size", "2")),
  );
  const refresh = useCallback(async () => {
    try {
      setBrowserPool(asRow(await getBrowserPool()));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }, [setBrowserPool, setError]);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(timer);
  }, [refresh]);
  const reload = async () => {
    try {
      setBrowserPool(asRow(await reloadBrowserPool(Number(size))));
      setNotice("浏览器池已请求重载");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const toggle = async () => {
    try {
      setBrowserPool(asRow(await setBrowserPoolMode(!modeEnabled)));
      setNotice(`浏览器池已${modeEnabled ? "关闭" : "开启"}`);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const slots = rows(pool.slots);
  const waitingTasks = rows(rowValue(view, "queue"));
  const system = child(browserPool, "system");
  const cpu = child(system, "cpu");
  const foreground = child(browserPool, "foreground");
  const hostTotal = num(view, "hostTotalGb");
  const hostFree = num(view, "hostFreeGb");
  const hostUsed = num(view, "hostUsedGb");
  const profileSize = text(view, "profileSizeText", "0 B");
  const configuredSize = num(view, "configuredSize", num(pool, "size"));
  const maxPoolSize = num(view, "maxPoolSize", 24);
  return (
    <div className="browser-pool-page">
      <Panel title="日常槽位配置说明（备忘）" className="browser-pool-notes-panel">
        <div className="browser-pool-notes">
          <p>浏览器池 = 常驻 Chromium + 本地 Profile 缓存。任务通过 CDP 接入，<strong>每单仍独立 Context</strong>（不串 Session）。每槽约 <strong>400–550 MB</strong> 内存。</p>
          <p className="browser-pool-proxy-note"><strong>代理：</strong>池内槽位<strong>不绑定</strong>代理。每单任务启动时，从「代理池」随机抽一条启用代理，写入该单的 <code>Context</code>（见运行日志 <code>proxy=yes</code>）。这样不同任务可用不同 IP，避免多账号共用一个住宅 IP。代理在左侧「代理池」管理，与浏览器池分开配置。</p>
        <table className="browser-pool-notes-table">
          <thead><tr><th>场景</th><th>建议槽位</th><th>说明</th></tr></thead>
          <tbody>
            <tr><td>日常单量</td><td><code>4–8</code></td><td>稳妥，与系统配置里「前台并发」对齐即可</td></tr>
            <tr><td>高峰 / 128G 独服</td><td><code>8–16</code></td><td>内存充裕时可加；注意 CPU 与其它项目占用的 ~28G</td></tr>
            <tr><td>上限</td><td><code>≤24</code></td><td>由 <code>BROWSER_POOL_MAX_SIZE</code> 控制，不建议无脑拉满</td></tr>
          </tbody>
        </table>
          <p className="browser-pool-persistence"><strong>持久化：</strong>在服务器 <code>.env</code> 设置 <code>BROWSER_POOL_SIZE=8</code>、<code>MAX_CONCURRENT_ACTIVATIONS=8</code> 后 <code>docker compose up -d --force-recreate app</code>。本页「应用并重载」仅改运行时，重启容器仍以 .env 为准。</p>
        </div>
      </Panel>
      <div className="stat-grid browser-pool-stats">
        <div className="stat-card"><span className="stat-label">池状态</span><strong className={cn("stat-value browser-pool-stat-status", text(view, "statusClass", "is-not-ready"))}>{status}</strong></div>
        <div className="stat-card"><span className="stat-label">槽位</span><strong className="stat-value">{num(pool, "size")} / {configuredSize}</strong></div>
        <div className="stat-card"><span className="stat-label">空闲 / 忙碌</span><strong className="stat-value browser-pool-stat-usage">{num(pool, "idle")} / {num(pool, "busy")}</strong></div>
        <div className="stat-card"><span className="stat-label">排队任务</span><strong className="stat-value browser-pool-stat-waiting">{num(pool, "waiting")}</strong></div>
        <div className="stat-card"><span className="stat-label">累计借用</span><strong className="stat-value">{num(pool, "totalUses", num(pool, "uses"))}</strong></div>
        <div className="stat-card"><span className="stat-label">预估池内存</span><strong className="stat-value browser-pool-stat-memory">{text(view, "estimatedProcessText", "—")}</strong></div>
      </div>
      <Panel title="主机内存与容量建议" actions={<Button variant="outline" onClick={() => void reload()}><RefreshCw className="h-4 w-4" />立即刷新</Button>} className="browser-pool-memory-panel">
        <div className="browser-pool-memory-hint">
          {Number.isFinite(hostTotal) && Number.isFinite(hostFree) ? <>
            <div>主机内存：已用 <strong>{hostUsed.toFixed(1)} GB</strong> / 共 {hostTotal} GB（可用约 <strong className="browser-pool-free-memory">{hostFree.toFixed(1)} GB</strong>）</div>
          </> : <div>主机内存：—</div>}
          <div>CPU：{text(cpu, "text", "—")}</div>
          <div>池 Profile 磁盘：{profileSize} · CDP 基址端口 {text(pool, "basePort", "19222")}</div>
          <div>前台任务：{text(foreground, "activeForegroundJobs", "0")} 占用 · 槽位上限 {maxPoolSize}</div>
          <div className="browser-pool-sizing-hint">💡 {text(view, "sizingHint", "—")}</div>
        </div>
      </Panel>
      <Panel title="运行模式" className="browser-pool-mode-panel">
        <div className="browser-pool-mode-row">
          <label>
            <input
              type="checkbox"
              checked={modeEnabled}
              onChange={() => void toggle()}
            />
            启用浏览器池（预热 Chromium，任务 CDP 接入）
          </label>
          <span className="browser-pool-mode-hint">当前：{text(view, "modeLabel", "—")}</span>
        </div>
        <p className="browser-pool-mode-description"><strong>关闭</strong>：每任务独立冷启动 Chromium（与 Session 登录路径分离，调试更稳）。<br /><strong>开启</strong>：复用预热槽位，省去约 5–10s 启动时间。切换立即生效，写入 PostgreSQL 持久化。</p>
      </Panel>
      <Panel title="池配置" className="browser-pool-config-panel">
        <div className="browser-pool-config-row">
          <label>
            槽位数量（1–<span>{maxPoolSize}</span>）
            <input
              type="number"
              min={1}
              max={maxPoolSize}
              value={size}
              onChange={(event) => setSize(event.target.value)}
              className="asset-input browser-pool-size-input"
            />
          </label>
          <Button onClick={() => void reload()}>
            <RefreshCw className="h-4 w-4" />
            应用并重载池
          </Button>
        </div>
        <p className="browser-pool-config-description">
          重载会关闭所有空闲槽位并重新预热；有任务占用时需等待结束。修改后写入运行时配置（重启容器仍以 <code>BROWSER_POOL_SIZE</code> 环境变量为准，可在 .env 持久化）。
        </p>
      </Panel>
      <Panel title="槽位可视化" className="browser-pool-slots-panel">
        <div className="browser-pool-slots-grid">
          {slots.map((slot) => {
            const openUrls = Array.isArray(rowValue(slot, "openUrls")) ? rowValue(slot, "openUrls") as string[] : [];
            return (
              <div
                key={text(slot, "slotId")}
                className={cn("browser-pool-slot", text(slot, "statusClass", "is-idle"))}
              >
                <div className="browser-pool-slot-header">
                  <strong>Slot #{text(slot, "slotId")}</strong>
                  <span className="browser-pool-slot-status">{text(slot, "statusLabel", "-")}</span>
                </div>
                <div className="browser-pool-slot-details">
                  <div>CDP: <code>{text(slot, "cdpUrl", "")}</code></div>
                  <div>端口: {text(slot, "port")} · 累计 {num(slot, "uses")} 次</div>
                  <div>Profile: {text(slot, "profileSizeText", "0 B")}</div>
                  <div>{text(slot, "pageSummary", "页面数: 0")}</div>
                </div>
                <div className="browser-pool-slot-urls">
                  {openUrls.length ? openUrls.map((url, index) => <div key={`${String(url)}-${index}`}>{String(url)}</div>) : <div className="is-empty">无打开页面</div>}
                </div>
              </div>
            );
          })}
          {slots.length === 0 ? (
            <div className="browser-pool-empty">暂无槽位（池未初始化或已禁用）</div>
          ) : null}
        </div>
      </Panel>
      <Panel title="等待队列" className="browser-pool-queue-panel">
        <div className="browser-pool-queue">{waitingTasks.length ? waitingTasks.map((task) => <div key={text(task, "jobKey", text(task, "job_key"))}>{text(task, "jobKey", text(task, "job_key", "排队任务"))}</div>) : "无排队任务"}</div>
      </Panel>
    </div>
  );
}

function AddressPanel({
  addressRows,
  setAddressRows,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const empty = {
    line1: "",
    city: "",
    state: "",
    postal_code: "",
    country: "US",
  };
  const [form, setForm] = useState<Row>(empty);
  const [formErrors, setFormErrors] = useState<Record<string, string>>({});
  const [editID, setEditID] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  const refresh = async () => {
    try {
      const result = asRow(await getAddresses("US"));
      setAddressRows(rows(result.addresses));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const save = async () => {
    try {
      const values = {
        line1: text(form, "line1", ""),
        city: text(form, "city", ""),
        state: text(form, "state", ""),
        postal_code: text(form, "postal_code", ""),
        country: text(form, "country", "US"),
      };
      setFormErrors({});
      if (editID) await updateAddress(editID, values);
      else await createAddress({ region: "US", ...values } as JsonMap);
      setForm(empty);
      setEditID("");
      setFormOpen(false);
      setNotice(editID ? "地址模板已更新" : "地址模板已添加");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const remove = async (id: string) => {
    if (!(await confirm("确定删除该地址模板？删除后不可恢复。", "删除地址模板"))) return;
    try {
      await deleteAddress(id);
      setNotice("地址模板已删除");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const beginCreate = () => {
    setEditID("");
    setForm(empty);
    setFormErrors({});
    setFormOpen(true);
  };
  const beginEdit = (row: Row) => {
    if (!bool(row, "can_edit")) return;
    setEditID(text(row, "id"));
    setForm({ ...empty, ...row });
    setFormErrors({});
    setFormOpen(true);
  };
  const cancelForm = () => {
    setEditID("");
    setForm(empty);
    setFormErrors({});
    setFormOpen(false);
  };
  return (
    <div>
      <Panel
        title="美国免税地址池"
        className="tax-address-panel"
        actions={
          <>
            <Button
              onClick={async () => {
                if (!(await confirm(
                  "将随机生成 10 条美国免税州地址（OR / DE / MT / NH / AK）并加入 US 地址池，继续？",
                  "批量生成美国免税地址",
                ))) return;
                try {
                  const result = asRow(await generateAddresses(10));
                  setNotice(`已生成 ${num(result, "count", 10)} 条美国免税地址`);
                  await refresh();
                } catch (reason) {
                  setError(errorMessage(reason));
                }
              }}
            >
              <Shuffle className="h-4 w-4" />
              批量生成（10 条）
            </Button>
            <Button
              variant="outline"
              title="删除所有从未支付成功绑定过的地址"
              onClick={async () => {
                if (!(await confirm(
                  "将清空所有未绑定地址（从未支付成功使用过的），已绑定的会保留。继续？",
                  "清空未绑定地址",
                ))) return;
                try {
                  const result = asRow(await clearAddresses("US"));
                  setNotice(text(result, "message", "未绑定地址清理完成"));
                  await refresh();
                } catch (reason) {
                  setError(errorMessage(reason));
                }
              }}
            >
              <LegacyEraserIcon />
              清空未绑定
            </Button>
            <Button variant="outline" onClick={beginCreate}>
              <Plus className="h-4 w-4" />
              手动新增
            </Button>
          </>
        }
      >
        <p className="tax-address-info text-[13px] leading-6 text-slate-500">
          支付结账时优先从地址池随机选取；池为空则系统自动即时生成。持卡人姓名由前台自动化随机生成，无需在此配置。
        </p>
        <div className={cn("import-box tax-address-form", formOpen && "active")}>
          <div className="import-box-header">
            <div className="import-box-title">
              {editID ? "编辑地址模板" : "新增地址模板"}
            </div>
            <button type="button" className="btn-delete" onClick={cancelForm} title="关闭">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="mt-3 grid gap-3 md:grid-cols-2">
            {[
              ["line1", "街道地址 (line1)", "182 Highland Ave"],
              ["city", "城市 (city)", "Salem"],
              ["state", "州 (state，完整英文名)", "Oregon"],
              ["postal_code", "邮编 (postal_code)", "97301"],
              ["country", "国家代码 (country)", "US"],
            ].map(([key, label, placeholder]) => (
              <label key={key} className="tax-address-form-field">
                {label} <span className="tax-required-mark">*</span>
                <input
                  value={text(form, key, "")}
                  maxLength={key === "line1" ? 200 : key === "city" || key === "state" ? 100 : key === "postal_code" ? 20 : 2}
                  placeholder={placeholder}
                  onChange={(event) => setForm({ ...form, [key]: event.target.value })}
                  style={key === "country" ? { textTransform: "uppercase" } : undefined}
                  className="asset-input tax-address-input"
                />
                {formErrors[key] ? <span className="tax-address-field-error">{formErrors[key]}</span> : null}
              </label>
            ))}
          </div>
          <div className="import-actions mt-4">
            <Button variant="outline" onClick={cancelForm}>取消</Button>
            <Button onClick={() => void save()}>
              <Check className="h-4 w-4" />
              {editID ? "更新" : "保存"}
            </Button>
          </div>
        </div>
        <div className="address-list-block">
        <DataTable
          tableClassName="tax-address-table"
          data={addressRows}
          empty="地址池为空，请点击「批量生成」或「手动新增」。"
          columns={[
            {
              key: "line1",
              label: "街道地址",
              render: (row) => text(row, "line1"),
            },
            { key: "city", label: "城市" },
            { key: "state", label: "州" },
            { key: "postal_code", label: "邮编" },
            { key: "country", label: "国家" },
            {
              key: "status",
              label: "状态",
              render: (row) => (
                <Badge label={text(row, "status_label", "-")} tone={backendStatusTone(row)} />
              ),
            },
            {
              key: "actions",
              label: "操作",
              render: (row) => (
                <div className="flex justify-center gap-2">
                  <button
                    type="button"
                    className={cn("btn-delete", bool(row, "can_edit") && "address-edit-action")}
                    title={bool(row, "can_edit") ? "编辑" : "已绑定不可编辑"}
                    disabled={!bool(row, "can_edit")}
                    onClick={() => beginEdit(row)}
                  >
                    <Pencil className="h-4 w-4" />
                  </button>
                  <button
                    type="button"
                    className="btn-delete"
                    title={bool(row, "can_delete") ? "删除" : "已绑定不可删"}
                    disabled={!bool(row, "can_delete")}
                    onClick={() => void remove(text(row, "id"))}
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </div>
              ),
            },
          ]}
        />
        </div>
      </Panel>
    </div>
  );
}

function formatCheckoutRuntimeLog(row: Row) {
  const timestamp = new Date(num(row, "ts", 0));
  const time = Number.isNaN(timestamp.getTime())
    ? "--:--:--"
    : timestamp.toTimeString().slice(0, 8);
  const job = text(row, "job_key", text(row, "jobKey", ""));
  const shortJob = job ? job.slice(-8) : "—";
  const source = text(row, "source", text(row, "level", "")).trim();
  const sourceLabel = ({
    "fork/register_openai.js": "注册",
    "fork/oauth_login.js": "协议",
    "fork/index.js": "结账",
    protocol: "协议",
    "legacy/stdout": "浏览器",
    "legacy/stderr": "浏览器",
    product: "流程",
    task: "任务",
    server: "服务",
    system: "系统",
  } as Record<string, string>)[source] || source.replace(/^fork\//, "").replace(/\.js$/, "").slice(0, 8);
  return `${time}  ${shortJob}  ${sourceLabel || "—"}  ${text(row, "text", text(row, "line", text(row, "message", "")))}`;
}

function CheckoutDebugScreenshots({ paths }: { paths: string[] }) {
  const [loaded, setLoaded] = useState<Array<{ path: string; url?: string; error?: string }>>([]);
  const pathKey = paths.join("\n");
  useEffect(() => {
    if (!pathKey) {
      setLoaded([]);
      return;
    }
    let disposed = false;
    const urls: string[] = [];
    const load = async () => {
      const result = await Promise.all(pathKey.split("\n").map(async (path) => {
        try {
          const url = URL.createObjectURL(await downloadAdminMedia("screenshots", path));
          urls.push(url);
          return { path, url };
        } catch (reason) {
          return { path, error: errorMessage(reason) };
        }
      }));
      if (!disposed) setLoaded(result);
    };
    void load();
    return () => {
      disposed = true;
      urls.forEach((url) => URL.revokeObjectURL(url));
    };
  }, [pathKey]);

  if (!paths.length) return null;
  return (
    <div className="checkout-screenshot-wrap">
      <div className="checkout-screenshot-title">失败截图 ({paths.length})</div>
      {loaded.length ? loaded.map((item) => (
        <div key={item.path} className="checkout-screenshot-item">
          {item.error ? <p className="checkout-screenshot-error">{item.error}</p> : <img src={item.url} alt={item.path} />}
        </div>
      )) : <div className="checkout-screenshot-loading">加载截图中...</div>}
    </div>
  );
}

function CheckoutPanel({ checkoutPlans, setNotice, setError, navigate }: ContentProps) {
  const [plan, setPlan] = useState("");
  const [planName, setPlanName] = useState("");
  const [region, setRegion] = useState("");
  const [session, setSession] = useState("");
  const workerMode = "browser";
  const [jobKey, setJobKey] = useState("");
  const [status, setStatus] = useState<Row>({});
  const [runtimeRows, setRuntimeRows] = useState<Row[]>([]);
  const [autoScroll, setAutoScroll] = useState(true);
  const [busy, setBusy] = useState(false);
  const logAfterRef = useRef(0);
  const logWrapRef = useRef<HTMLDivElement>(null);
  const [logReload, setLogReload] = useState(0);
  const planOptions = rows(rowValue(checkoutPlans, "planOptions"));
  const regionOptions = rows(rowValue(checkoutPlans, "regionOptions"));
  const selectedPlan = planOptions.find((option) => text(option, "value", text(option, "code")) === plan) || planOptions[0];
  const selectedRegion = regionOptions.find((option) => text(option, "value", text(option, "code")) === region) || regionOptions[0];
  const currentStatus = text(status, "status", "running");
  const checkoutURL = text(status, "checkout_url", "");
  const activeWorker = text(status, "mode", workerMode);
  const protocolRun = activeWorker === "protocol";
  const paused = text(status, "errorCode", text(status, "error_code")) === "payment_paused_before_submit";
  const statusText = paused
    ? "✅ 已到达付款前最后一步，未扣款"
    : currentStatus === "success" || currentStatus === "succeeded"
    ? (protocolRun ? "✅ 协议支付完成" : "✅ 支付链接已生成")
    : currentStatus === "running" || currentStatus === "queued"
      ? (protocolRun ? "⏳ 协议 Worker 调试中..." : "⏳ 浏览器调试进行中...")
      : currentStatus === "manual"
        ? `⚠️ ${text(status, "message", "需人工处理")}`
        : `❌ ${text(status, "message", "调试失败")}`;
  const statusColor = paused || currentStatus === "success" || currentStatus === "succeeded"
    ? "var(--success, #22c55e)"
    : currentStatus === "running" || currentStatus === "queued"
      ? "var(--text-dim)"
      : "var(--error, #ef4444)";
  useEffect(() => {
    if (!plan && selectedPlan) setPlan(text(selectedPlan, "value", text(selectedPlan, "code")));
    if (!region) {
      const configuredRegion = text(checkoutPlans, "region", "") || text(selectedRegion, "value", text(selectedRegion, "code", ""));
      if (configuredRegion) setRegion(configuredRegion);
    }
  }, [checkoutPlans, plan, region, selectedPlan, selectedRegion]);
  const start = async () => {
    setBusy(true);
    try {
      const result = asRow(
        await generateCheckout({ plan_type: plan, plan_name: planName, country: region, session, mode: "browser" }),
      );
      setJobKey(text(result, "jobKey"));
      setStatus(result);
      setRuntimeRows([]);
      logAfterRef.current = 0;
      setNotice(text(result, "message", "Checkout 调试任务已启动"));
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    if (!jobKey) return;
    let disposed = false;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const result = asRow(await getCheckoutStatus(jobKey));
        if (disposed) return;
        setStatus((previous) => ({ ...previous, ...result }));
        if (["success", "succeeded", "failed", "manual"].includes(text(result, "status", "running")) && timer !== undefined) {
          window.clearInterval(timer);
          timer = undefined;
        }
      } catch {
        // Retry on the next poll so a transient API failure does not hide the task.
      }
    };
    timer = window.setInterval(() => void poll(), 2000);
    void poll();
    return () => {
      disposed = true;
      if (timer !== undefined) window.clearInterval(timer);
    };
  }, [jobKey]);
  useEffect(() => {
    if (!jobKey) return;
    let disposed = false;
    let inFlight = false;
    const filterForJob = (value: unknown) => rows(value).filter((entry) =>
      text(entry, "job_key", text(entry, "jobKey", "")) === jobKey,
    );
    const loadTail = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const result = asRow(await getRuntimeLogs(2000, { tail: true }));
        if (disposed) return;
        setRuntimeRows(filterForJob(rowValue(result, "entries") ?? rowValue(result, "logs")));
        logAfterRef.current = num(result, "nextAfter", 0);
      } catch {
        // Retry on the next incremental request.
      } finally {
        inFlight = false;
      }
    };
    const loadIncremental = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const result = asRow(await getRuntimeLogs(500, { after: logAfterRef.current }));
        if (disposed) return;
        const entries = filterForJob(rowValue(result, "entries") ?? rowValue(result, "logs"));
        if (entries.length) setRuntimeRows((previous) => [...previous, ...entries].slice(-2000));
        logAfterRef.current = num(result, "nextAfter", logAfterRef.current);
      } catch {
        // Retry on the next poll.
      } finally {
        inFlight = false;
      }
    };
    void loadTail();
    const timer = window.setInterval(() => void loadIncremental(), 1500);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [jobKey, logReload]);
  useEffect(() => {
    if (selectedPlan) setPlanName(text(selectedPlan, "planName", ""));
  }, [selectedPlan]);
  useEffect(() => {
    if (!autoScroll) return;
    return scrollLogToBottom(logWrapRef.current);
  }, [autoScroll, runtimeRows]);
  return (
    <div className="checkout-debug-page">
      <Panel title="生成参数" header={false} className="checkout-debug-params-panel">
        <h2 className="panel-title">生成参数</h2>
        <div className="checkout-debug-form-grid">
          <div className="form-group">
            <label htmlFor="checkout_plan_type">套餐类型</label>
            <select id="checkout_plan_type" value={plan} onChange={(event) => setPlan(event.target.value)} className="asset-input">
              {planOptions.map((option) => {
                const value = text(option, "value", text(option, "code"));
                return <option key={value} value={value}>{text(option, "label")} — {text(option, "planName")}</option>;
              })}
            </select>
          </div>
          <div className="form-group">
            <label htmlFor="checkout_plan_name">plan_name（自动跟随套餐，可手动改）</label>
            <input id="checkout_plan_name" value={planName} onChange={(event) => setPlanName(event.target.value)} className="asset-input" placeholder="chatgptplusplan" autoComplete="off" />
          </div>
          <SearchableRegionSelect
            id="checkout_region_selector"
            label="调试支付地区"
            value={region}
            options={regionOptions}
            onChange={setRegion}
          />
        </div>
        <p className="checkout-debug-region-hint">将使用: {text(selectedRegion, "countryLabel", text(selectedRegion, "label", region))} / {text(selectedRegion, "currency", text(checkoutPlans, "currency", ""))}</p>
        <div className="form-group checkout-debug-session-group">
          <label htmlFor="checkout_session_input">Session JSON</label>
          <textarea id="checkout_session_input" rows={8} value={session} onChange={(event) => setSession(event.target.value)} className="checkout-session-textarea asset-input" placeholder="粘贴完整 Session JSON（从 chatgpt.com/api/auth/session 复制，尽量带上 cookies[] / __Secure-next-auth.session-token）" />
        </div>
        <div className="checkout-debug-actions">
          <Button className="checkout-debug-start-button" onClick={() => void start()} disabled={busy}>
            <Play className="h-3.5 w-3.5" />
            启动浏览器调试
          </Button>
          <Button variant="outline" className="checkout-debug-clear-button" onClick={() => { setSession(""); setJobKey(""); setStatus({}); setRuntimeRows([]); logAfterRef.current = 0; }}>
            清空
          </Button>
        </div>
      </Panel>
      {jobKey ? (
        <section className="panel checkout-debug-result-panel">
          <h2 className="panel-title">任务状态</h2>
          <div className="checkout-result-status" style={{ color: statusColor }}>{statusText}</div>
          <div className="checkout-result-email">{text(status, "email", "") ? `账号: ${text(status, "email", "")}` : ""}</div>
          <div className="checkout-result-url-wrap">
            {checkoutURL ? <a href={checkoutURL} target="_blank" rel="noreferrer">{checkoutURL}</a> : currentStatus === "running" || currentStatus === "queued" ? <span>等待 Checkout URL...</span> : null}
          </div>
          <CheckoutDebugScreenshots paths={mediaPathList(rowValue(status, "screenshots"))} />
        </section>
      ) : null}
      <Panel title="运行日志" header={false} className="checkout-debug-runtime-panel">
        <div className="runtime-log-toolbar">
          <h2 className="panel-title" style={{ margin: 0 }}>运行日志</h2>
          <label className="runtime-log-toggle">
            <input type="checkbox" checked={autoScroll} onChange={(event) => setAutoScroll(event.target.checked)} />
            <span>自动滚动</span>
          </label>
          <Button className="checkout-debug-refresh-button" onClick={() => { logAfterRef.current = 0; setRuntimeRows([]); setLogReload((value) => value + 1); }}>
            <RefreshCw className="h-4 w-4" />刷新
          </Button>
          <Button className="checkout-debug-open-runtime-button" variant="outline" onClick={() => navigate("runtime_logs")}>打开完整运行日志</Button>
        </div>
        <p className="runtime-log-hint">仅显示当前调试任务的 Playwright 子进程输出；失败时会自动保存截图。</p>
        <div className="runtime-log-pre-wrap" ref={logWrapRef}>
          <pre className="runtime-log-pre">{runtimeRows.map(formatCheckoutRuntimeLog).join("\n")}</pre>
        </div>
      </Panel>
    </div>
  );
}

function CardsPanel({
  cardRows,
  setCardRows,
  cardMeta,
  setCardMeta,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const [input, setInput] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [poolRows, setPoolRows] = useState<Row[]>([]);
  const [providerRows, setProviderRows] = useState<Row[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [virtualDeleteRow, setVirtualDeleteRow] = useState<Row | null>(null);
  const [deletingCard, setDeletingCard] = useState(false);
  const [activityRow, setActivityRow] = useState<Row | null>(null);
  const [activity, setActivity] = useState<Row | null>(null);
  const [activityBusy, setActivityBusy] = useState(false);
  const [createForm, setCreateForm] = useState({
    poolId: "",
    provider: "",
    usageType: "ONE_TIME",
    amount: "",
    currency: "USD",
    cardholderName: "KC GPT",
  });
  const refresh = async () => {
    try {
      const [cardPayload, poolPayload] = await Promise.all([getCards(), getCardPools()]);
      const result = asRow(cardPayload);
      const poolResult = asRow(poolPayload);
      setCardRows(rows(result.cards));
      setCardMeta(asRow(result.stats));
      const nextPools = rows(poolResult.pools);
      const nextProviderRows = rows(poolResult.providers).filter((provider) => visibleCardProviders.includes(text(provider, "provider").toUpperCase() as typeof visibleCardProviders[number]));
      setPoolRows(nextPools);
      setProviderRows(nextProviderRows);
      setCreateForm((current) => {
        const selectedPoolId = current.poolId || text(nextPools[0], "id", "");
        const availableProviders = creatableProviderOptions(nextProviderRows);
        return {
          ...current,
          poolId: selectedPoolId,
          provider: availableProviders.some((provider) => text(provider, "provider").toUpperCase() === current.provider.toUpperCase())
            ? current.provider
            : text(availableProviders[0], "provider", ""),
        };
      });
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  useEffect(() => {
    void refresh();
    // CardsPanel owns the Provider/card-pool controls, so it must hydrate its
    // pool options even when the parent only loaded the legacy card rows.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const importPool = async () => {
    try {
      const result = asRow(await importCardPool({ text: input }));
      setNotice(
        `已导入 ${num(result, "imported", num(result, "created"))} 张卡片`,
      );
      setInput("");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const createCard = async () => {
    const poolId = createForm.poolId.trim();
    const provider = createForm.provider.trim().toUpperCase();
    if (!poolId || !provider) {
      setError("请选择卡池和已启用的 Provider");
      return;
    }
    const amount = Number(createForm.amount);
    if (!createForm.amount.trim() || !Number.isFinite(amount) || amount <= 0) {
      setError("请填写大于 0 的首充金额");
      return;
    }
    setCreating(true);
    try {
      await createProviderCard({
        poolId,
        provider,
        usageType: createForm.usageType,
        amount,
        currency: createForm.currency.trim().toUpperCase(),
        cardholderName: createForm.cardholderName.trim(),
        idempotencyKey: typeof crypto?.randomUUID === "function" ? crypto.randomUUID() : `manual-${Date.now()}`,
      });
      setNotice(`已通过 ${provider} 创建虚拟卡并加入卡池`);
      setCreateOpen(false);
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setCreating(false);
    }
  };
  const creatableProviders = creatableProviderOptions(providerRows);
  const isVirtualProviderCard = (row: Row) => {
    const provider = text(row, "provider", "LOCAL_TEXT").toUpperCase();
    const providerCardId = text(row, "provider_card_id", text(row, "providerCardId"));
    return provider !== "" && provider !== "LOCAL_TEXT" && Boolean(providerCardId);
  };
  const executeDelete = async (row: Row, cancelProvider: boolean) => {
    setDeletingCard(true);
    try {
      await deleteCard(text(row, "id"), { cancelProvider });
      setVirtualDeleteRow(null);
      setNotice(cancelProvider ? "已远程销卡，卡片已标记为已销毁" : "已删除本地记录，未调用远程销卡");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setDeletingCard(false);
    }
  };
  const openActivity = async (row: Row) => {
    const id = text(row, "id", "");
    if (!id || id === "-") return;
    setActivityRow(row);
    setActivityBusy(true);
    setActivity(null);
    try {
      setActivity(asRow(await getCardActivity(id)));
    } catch (reason) {
      setError(errorMessage(reason));
      setActivityRow(null);
    } finally {
      setActivityBusy(false);
    }
  };
  const closeActivity = () => {
    setActivityRow(null);
    setActivity(null);
  };
  const remove = async (row: Row) => {
    if (isVirtualProviderCard(row)) {
      setVirtualDeleteRow(row);
      return;
    }
    if (!(await confirm("确定从卡池删除此银行卡吗？", "删除银行卡"))) return;
    await executeDelete(row, false);
  };
  const activityCard = child(activity ?? undefined, "card");
  return (
    <div className="cards-page">
      <div className="stat-grid cards-stat-grid">
        {[["卡片总数", num(cardMeta, "total", cardRows.length), "#7e22ce"], ["可用", num(cardMeta, "active"), "#047857"], ["冷却中", num(cardMeta, "cooldown"), "#b45309"], ["已销毁/待重试", num(cardMeta, "exhausted"), "#b91c1c"]].map(([label, value, color]) => (
          <div className="stat-card" key={String(label)}><div className="stat-header"><span className="stat-label">{label}</span></div><div className="stat-value" style={{ color: String(color) }}>{value}</div></div>
        ))}
      </div>
      <Panel title="银行卡列表" actions={<div className="flex gap-2"><Button variant="outline" onClick={() => setCreateOpen((value) => !value)}><Plus className="h-4 w-4" />创建虚拟卡</Button><Button variant="outline" onClick={() => setImportOpen((value) => !value)}><Upload className="h-4 w-4" />批量导入</Button></div>}>
        <div className={cn("import-box", createOpen && "active")}>
          <div className="import-box-header"><div className="import-box-title">通过 Provider 创建虚拟卡</div></div>
          <p className="import-box-tip">创建成功后只保存卡片元数据；完整卡号、有效期和 CVC 只会在真实支付前由后端临时读取。</p>
          <div className="config-field-grid">
            <label>卡池<select value={createForm.poolId} onChange={(event) => { const poolId = event.target.value; setCreateForm((current) => ({ ...current, poolId, provider: creatableProviders.some((provider) => text(provider, "provider").toUpperCase() === current.provider.toUpperCase()) ? current.provider : text(creatableProviders[0], "provider", "") })); }} className="asset-input"><option value="">请选择卡池</option>{poolRows.map((pool) => <option key={text(pool, "id")} value={text(pool, "id")}>{text(pool, "name", text(pool, "id"))}</option>)}</select></label>
            <label>Provider<select value={createForm.provider} onChange={(event) => setCreateForm({ ...createForm, provider: event.target.value })} className="asset-input"><option value="">请选择 Provider</option>{creatableProviders.map((provider) => <option key={text(provider, "provider")} value={text(provider, "provider")}>{text(provider, "provider")}</option>)}</select><p className="config-help">与系统配置里已启用、且支持发卡的 Provider 一致。LOCAL_TEXT 请用批量导入，不走这里开卡。</p></label>
            <label>用途<select value="ONE_TIME" disabled className="asset-input"><option value="ONE_TIME">ONE_TIME 一次性</option></select><p className="config-help">充值卡统一一次性使用；任务完成或失败后都会销毁，不能再次分配。</p></label>
            <label>首充金额（USD）<input type="number" min={0.01} step="0.01" value={createForm.amount} onChange={(event) => setCreateForm({ ...createForm, amount: event.target.value })} className="asset-input" placeholder="必填" /></label>
            <label>币种<input value={createForm.currency} onChange={(event) => setCreateForm({ ...createForm, currency: event.target.value })} className="asset-input" maxLength={3} /></label>
            <label>持卡人<input value={createForm.cardholderName} onChange={(event) => setCreateForm({ ...createForm, cardholderName: event.target.value })} className="asset-input" /></label>
          </div>
          <div className="import-actions mt-3"><Button onClick={() => void createCard()} disabled={creating || creatableProviders.length === 0}><Plus className="h-4 w-4" />{creating ? "创建中…" : "创建并加入卡池"}</Button>{creatableProviders.length === 0 ? <span className="config-help">没有已启用且支持发卡的 Provider，请先到系统配置启用并保存。</span> : null}</div>
        </div>
        <div className={cn("import-box", importOpen && "active")}>
          <div className="import-box-header"><div className="import-box-title">批量导入银行卡</div></div>
          <p className="import-box-tip">每行一条，格式：<code>卡号|有效期|CVC|持卡人</code>（持卡人可选）。有效期支持 <code>MM/YY</code> 或 <code>MMYY</code>。</p>
          <textarea rows={6} value={input} onChange={(event) => setInput(event.target.value)} className="import-textarea" placeholder="5349336392059744|04/31|738|John Doe\n5349336383247282|04/31|937" />
          <div className="import-actions mt-3"><Button variant="outline" onClick={() => void importPool()}><CheckCheck className="h-4 w-4" />解析并导入</Button></div>
        </div>
        <DataTable
          data={cardRows}
          empty="暂无卡片，请使用批量导入添加"
          minWidth={1860}
          tableClassName="cards-table"
          onRowClick={(row) => void openActivity(row)}
          columns={[
            { key: "provider", label: "Provider", render: (row) => text(row, "provider", "LOCAL_TEXT") },
            { key: "pool_id", label: "卡池", render: (row) => text(row, "pool_id", "pool_legacy") },
            {
              key: "card_number",
              label: "卡号",
              render: (row) => <code>{text(row, "card_number", "•••• " + text(row, "last4"))}</code>,
            },
            { key: "card_expiry", label: "有效期", render: (row) => <code>{text(row, "card_expiry")}</code> },
            { key: "card_cvc", label: "CVC", render: (row) => <code>{text(row, "card_cvc")}</code> },
            { key: "card_holder", label: "导入持卡人", render: (row) => text(row, "card_holder", text(row, "holder", "-")) },
            { key: "payment_holder_name", label: "支付姓名", render: (row) => text(row, "payment_holder_name", "-") },
            { key: "payment_address_line1", label: "绑定地址", render: (row) => text(row, "payment_address_line1", "-") },
            {
              key: "status",
              label: "状态",
              render: (row) => (
                <Badge
                  label={text(row, "status_label", text(row, "status"))}
                  tone={backendStatusTone(row)}
                />
              ),
            },
            {
              key: "usage_count",
              label: "次数",
              render: (row) => num(row, "usage_count", num(row, "usageCount")),
            },
            { key: "last_failure_message", label: "最近失败", render: (row) => text(row, "last_failure_message", text(row, "last_failure_code", "-")) },
            { key: "last_used_at", label: "最近使用", render: (row) => text(row, "last_used_at_text", text(row, "last_used_at", "")) },
            {
              key: "actions",
              label: "操作",
              render: (row) => (
                <div className="card-row-actions" onClick={(event) => event.stopPropagation()}>
                  <button type="button" title="交易与事件" onClick={() => void openActivity(row)} className="btn-detail">
                    <Activity className="h-4 w-4" />
                  </button>
                  <button
                    type="button"
                    title="删除"
                    onClick={() => void remove(row)}
                    className="btn-delete"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </div>
              ),
            },
          ]}
        />
      </Panel>
      {virtualDeleteRow ? (
        <div
          className="admin-confirm-overlay is-open"
          role="presentation"
          aria-hidden="false"
          onClick={() => {
            if (!deletingCard) setVirtualDeleteRow(null);
          }}
        >
          <div
            className="admin-confirm-dialog admin-confirm-dialog-wide"
            role="dialog"
            aria-modal="true"
            aria-labelledby="virtual-card-delete-title"
            onClick={(event) => event.stopPropagation()}
          >
            <div id="virtual-card-delete-title" className="admin-confirm-title">
              删除虚拟卡
            </div>
            <p className="admin-confirm-text">
              {`此卡由 ${text(virtualDeleteRow, "provider")} 开出（${text(virtualDeleteRow, "card_number", "•••• " + text(virtualDeleteRow, "last4"))}）。
远程销卡会调用 Provider API，可能产生费用。请选择删除方式：
• 仅删除本地记录：卡池不再使用这张卡，远程卡仍保留，不收费
• 同时调用 API 销卡：会向发卡方销卡，可能产生费用`}
            </p>
            <div className="admin-confirm-actions">
              <Button variant="outline" disabled={deletingCard} onClick={() => setVirtualDeleteRow(null)}>
                取消
              </Button>
              <Button variant="outline" disabled={deletingCard} onClick={() => void executeDelete(virtualDeleteRow, false)}>
                仅删除本地记录
              </Button>
              <Button variant="danger" disabled={deletingCard} onClick={() => void executeDelete(virtualDeleteRow, true)}>
                同时调用 API 销卡
              </Button>
            </div>
          </div>
        </div>
      ) : null}
      {activityRow ? (
        <div
          className="admin-confirm-overlay is-open"
          role="presentation"
          onClick={closeActivity}
        >
          <div
            className="admin-confirm-dialog card-activity-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="card-activity-title"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="card-activity-header">
              <div>
                <div id="card-activity-title" className="admin-confirm-title">卡片交易与事件</div>
                <p className="card-activity-subtitle">
                  {text(activityRow, "provider", "LOCAL_TEXT")} · {text(activityRow, "card_number", "•••• " + text(activityRow, "last4"))}
                  {text(activityCard, "providerCardId", "") ? ` · ${text(activityCard, "providerCardId")}` : ""}
                </p>
              </div>
              <Button variant="outline" onClick={closeActivity}>关闭</Button>
            </div>
            {activityBusy ? <p className="config-help">正在读取回调记录…</p> : null}
            {!activityBusy && activity ? (
              <>
                {bool(child(activity, "card"), "local") ? (
                  <p className="config-help">本地导入卡不会收到发卡方 Webhook。虚拟卡在配置回调并发生开卡或支付后，记录会出现在这里。</p>
                ) : null}
                <h4 className="card-activity-section-title">交易</h4>
                <DataTable
                  data={rows(rowValue(activity, "transactions"))}
                  empty={bool(child(activity, "card"), "local") ? "本地卡没有发卡方交易推送" : "暂无交易回调"}
                  minWidth={0}
                  tableClassName="card-activity-table"
                  columns={[
                    { key: "occurredAtText", label: "时间", render: (row) => text(row, "occurredAtText", "") },
                    { key: "statusLabel", label: "状态", render: (row) => text(row, "statusLabel", text(row, "status")) },
                    { key: "amount", label: "金额", render: (row) => `${text(row, "amount", "0")} ${text(row, "currency", "")}`.trim() },
                    { key: "merchantName", label: "商户", render: (row) => text(row, "merchantName", "-") },
                    { key: "failureCode", label: "失败原因", render: (row) => text(row, "failureCode", "-") },
                  ]}
                />
                <h4 className="card-activity-section-title">事件</h4>
                <DataTable
                  data={rows(rowValue(activity, "events"))}
                  empty={bool(child(activity, "card"), "local") ? "本地卡没有发卡方事件推送" : "暂无事件回调"}
                  minWidth={0}
                  tableClassName="card-activity-table"
                  columns={[
                    { key: "occurredAtText", label: "时间", render: (row) => text(row, "occurredAtText", text(row, "processedAtText", "")) },
                    { key: "eventTypeLabel", label: "事件", render: (row) => text(row, "eventTypeLabel", text(row, "eventType")) },
                    { key: "status", label: "处理", render: (row) => text(row, "status") === "ignored" ? "已忽略" : text(row, "status") === "processed" ? "已处理" : text(row, "status") },
                  ]}
                />
              </>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}

type StoreProductForm = {
  code: string;
  name: string;
  description: string;
  providerPlanName: string;
  country: string;
  currency: string;
  price: string;
  saleLimit: string;
  sortOrder: string;
  published: boolean;
};

function emptyStoreProductForm(options: Row = {}): StoreProductForm {
  const defaults = child(options, "defaults");
  const providerPlans = rows(rowValue(options, "providerPlans"));
  const countries = rows(rowValue(options, "countries"));
  const providerPlanName = text(defaults, "providerPlanName", text(providerPlans[0], "value", text(providerPlans[0], "code", "")));
  const country = text(defaults, "country", text(countries[0], "value", text(countries[0], "code", "")));
  return {
    code: "",
    name: "",
    description: "",
    providerPlanName,
    country,
    currency: text(options, "currency", ""),
    price: String(num(defaults, "price", 0)),
    saleLimit: String(num(defaults, "saleLimit", 100)),
    sortOrder: String(num(defaults, "sortOrder", 0)),
    published: bool(defaults, "published"),
  };
}

function StoreProductsPanel({ storeProductRows, setStoreProductRows, storeProductOptions, setNotice, setError }: ContentProps) {
  const [form, setForm] = useState<StoreProductForm>(() => emptyStoreProductForm(storeProductOptions));
  const [editingCode, setEditingCode] = useState("");
  const providerPlans = rows(rowValue(storeProductOptions, "providerPlans"));
  const countries = rows(rowValue(storeProductOptions, "countries"));
  const currency = text(storeProductOptions, "currency", form.currency);
  useEffect(() => {
    if (editingCode || form.code || form.name) return;
    setForm(emptyStoreProductForm(storeProductOptions));
  }, [editingCode, form.code, form.name, storeProductOptions]);
  const update = (key: string, value: string | boolean) => setForm((current) => ({ ...current, [key]: value }));
  const refresh = async () => {
    try {
      const result = await getStoreProducts();
      setStoreProductRows(rows(result.products));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const resetForm = () => {
    setEditingCode("");
    setForm(emptyStoreProductForm(storeProductOptions));
  };
  const edit = (row: Row) => {
    const code = text(row, "code");
    if (!code) {
      setError("商品编码为空，无法编辑");
      return;
    }
    const providerPlanName = text(row, "providerPlanName", "plus");
    const country = text(row, "country", "US");
    setEditingCode(code);
    setForm({
      code,
      name: text(row, "name"),
      description: text(row, "description"),
      providerPlanName,
      country,
      currency,
      price: String(num(row, "price", 20)),
      saleLimit: String(num(row, "saleLimit", 100)),
      sortOrder: String(num(row, "sortOrder", 10)),
      published: bool(row, "published"),
    });
  };
  const save = async () => {
    try {
      const code = editingCode || form.code;
      await saveStoreProduct({ ...form, code, price: Number(form.price), saleLimit: Number(form.saleLimit), sortOrder: Number(form.sortOrder) });
      setNotice(`商品 ${code} 已${editingCode ? "更新" : "保存"}`);
      await refresh();
      resetForm();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const toggle = async (row: Row) => {
    try {
      const code = text(row, "code");
      const result = await toggleStoreProductPublished(code);
      setNotice(text(result, "message", "商品状态已更新"));
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  return <div className="space-y-6">
    <Panel
      title={editingCode ? "编辑售卡商品" : "发布售卡商品"}
      description="商品发布后，前台才会显示；订单完成平台收款后由系统即时生成唯一自助 CDK。可售数量达到上限后会自动暂停购买并显示“已售罄，补货中”，补货时调高数量即可恢复。"
      actions={<Button variant="outline" size="sm" onClick={() => void refresh()}><RefreshCw className="h-4 w-4" />刷新</Button>}
    >
      <div className="grid gap-4 md:grid-cols-3">
        <label className="text-xs font-semibold text-slate-600">商品编码<input value={form.code} disabled={Boolean(editingCode)} onChange={(event) => update("code", event.target.value)} placeholder="plus" className={inputClass} /></label>
        <label className="text-xs font-semibold text-slate-600">商品名称<input value={form.name} onChange={(event) => update("name", event.target.value)} placeholder="ChatGPT Plus" className={inputClass} /></label>
        <label className="text-xs font-semibold text-slate-600">关联原版套餐<select value={form.providerPlanName} onChange={(event) => update("providerPlanName", event.target.value)} className={inputClass}>{providerPlans.map((item) => <option key={text(item, "value", text(item, "code"))} value={text(item, "value", text(item, "code"))}>{text(item, "label")}</option>)}</select></label>
        <SearchableRegionSelect
          id="store_product_region"
          label="商品支付地区"
          value={form.country}
          options={countries}
          onChange={(value) => update("country", value)}
          labelClassName="text-xs font-semibold text-slate-600"
        />
        <label className="text-xs font-semibold text-slate-600">价格<input type="number" min="0.01" step="0.01" value={form.price} onChange={(event) => update("price", event.target.value)} className={inputClass} /></label>
        <label className="text-xs font-semibold text-slate-600">可售数量（发布时必须大于 0）<input type="number" min={form.published ? 1 : 0} step="1" value={form.saleLimit} onChange={(event) => update("saleLimit", event.target.value)} className={inputClass} /></label>
        <label className="text-xs font-semibold text-slate-600">平台售卡币种<select value={currency} disabled className={inputClass}><option value={currency}>{currency}</option></select></label>
        <label className="text-xs font-semibold text-slate-600">排序<input type="number" value={form.sortOrder} onChange={(event) => update("sortOrder", event.target.value)} className={inputClass} /></label>
      </div>
      <label className="mt-4 block text-xs font-semibold text-slate-600">商品说明<textarea value={form.description} onChange={(event) => update("description", event.target.value)} rows={3} className={areaClass} /></label>
      <label className="store-product-publish-control"><input type="checkbox" checked={form.published} onChange={(event) => update("published", event.target.checked)} /><span>立即发布到前台</span></label>
      <div className="store-product-actions">
        <Button onClick={() => void save()}><Save className="h-4 w-4" />{editingCode ? "更新商品" : "保存商品"}</Button>
        {editingCode ? <Button variant="outline" onClick={resetForm}>取消编辑</Button> : null}
      </div>
    </Panel>
    <Panel title="已配置商品">
      <DataTable data={storeProductRows} empty="暂无商品" columns={[
        { key: "code", label: "编码", render: (row) => <code>{text(row, "code")}</code> },
        { key: "name", label: "商品", render: (row) => <span className="font-semibold text-slate-900">{text(row, "name")}</span> },
        { key: "price", label: "价格", render: (row) => text(row, "priceText", text(row, "price")) },
        { key: "inventory", label: "可售数量", render: (row) => <div className="flex flex-col gap-1"><span className="font-medium text-slate-800">{num(row, "saleLimit", 0) > 0 ? `${num(row, "remainingQuantity", 0)} / ${num(row, "saleLimit", 0)}` : "未配置"}</span><span className="text-xs text-slate-500">已售 {num(row, "soldCount", 0)}</span></div> },
        { key: "deliveryMode", label: "交付方式", render: () => <span className="text-xs text-slate-500">支付后即时生成 CDK</span> },
        { key: "published", label: "状态", render: (row) => <div className="flex flex-wrap items-center gap-2"><Badge label={text(row, "published_label", "-")} tone={backendTone(row, "published_tone")} />{bool(row, "soldOut") ? <Badge label="已售罄，补货中" tone="danger" /> : null}</div> },
        { key: "actions", label: "操作", render: (row) => <div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => edit(row)}><Pencil className="h-3.5 w-3.5" />编辑</Button><Button variant="outline" size="sm" onClick={() => void toggle(row)}>{text(row, "published_action_label", "切换发布状态")}</Button></div> },
      ]} />
    </Panel>
  </div>;
}

function CdkFilterDropdown({
  filterKey,
  label,
  value,
  options,
  open,
  setOpen,
  onChange,
}: {
  filterKey: string;
  label: string;
  value: string;
  options: Array<{ value: string; label: string }>;
  open: boolean;
  setOpen: (value: boolean) => void;
  onChange: (value: string) => void;
}) {
  const selected = options.find((option) => option.value === value) || options[0];
  return (
    <div className={cn("filter-dropdown", open && "open")} data-filter={filterKey} onClick={(event) => event.stopPropagation()}>
      <button type="button" className="filter-trigger" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span>{selected?.label || label}</span>
        <ChevronDown />
      </button>
      {open ? (
        <div className="filter-menu">
          {options.map((option) => (
            <button
              type="button"
              key={option.value}
              className={cn("filter-option", option.value === value && "active")}
              onClick={() => {
                onChange(option.value);
                setOpen(false);
              }}
            >
              {option.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function SearchableRegionSelect({
  id,
  label,
  value,
  options,
  onChange,
  labelClassName = "text-xs font-semibold text-slate-600",
}: {
  id: string;
  label: string;
  value: string;
  options: Row[];
  onChange: (value: string) => void;
  labelClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const selected = options.find((option) => text(option, "value", text(option, "code")) === value);
  const normalizedQuery = query.trim().toLowerCase();
  const filtered = options.filter((option) => {
    if (!normalizedQuery) return true;
    return [text(option, "label", ""), text(option, "countryLabel", ""), text(option, "code", text(option, "value", "")), text(option, "currency", "")]
      .join(" ")
      .toLowerCase()
      .includes(normalizedQuery);
  });
  useEffect(() => {
    if (!open) return;
    const close = (event: globalThis.MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);
  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);
  return (
    <div ref={rootRef} className="searchable-region-select" data-testid={`${id}-searchable-select`}>
      <div className={cn("searchable-region-select-label", labelClassName)}>
        <span>{label}</span>
        <button
          id={id}
          type="button"
          className="searchable-region-select-trigger"
          onClick={() => setOpen((current) => !current)}
          aria-expanded={open}
          aria-haspopup="listbox"
        >
          <span className={cn("searchable-region-select-value", !selected && "placeholder")}>{selected ? text(selected, "label") : "请选择支付地区"}</span>
          <ChevronDown className={cn("searchable-region-select-chevron", open && "open")} />
        </button>
      </div>
      {open ? (
        <div className="searchable-region-select-menu" role="listbox">
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => { if (event.key === "Escape") setOpen(false); }}
            className="searchable-region-select-search"
            placeholder="搜索国家、地区代码或币种"
            aria-label={`${label}搜索`}
          />
          <div className="searchable-region-select-options">
            {filtered.length ? filtered.map((option) => {
              const optionValue = text(option, "value", text(option, "code"));
              return (
                <button
                  type="button"
                  key={optionValue}
                  role="option"
                  aria-selected={optionValue === value}
                  className={cn("searchable-region-select-option", optionValue === value && "selected")}
                  onClick={() => { onChange(optionValue); setOpen(false); }}
                >
                  <span className="searchable-region-select-option-main">{text(option, "label", optionValue)}</span>
                  <span className="searchable-region-select-option-meta">{optionValue} · {text(option, "currency", "")}</span>
                </button>
              );
            }) : <div className="px-2 py-3 text-sm text-slate-500">没有匹配的支付地区</div>}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function CDKPanel({ cdkRows, setCdkRows, cdkMeta, setCdkMeta, setNotice, setError, confirm }: ContentProps) {
  const [plan, setPlan] = useState("plus");
  const [count, setCount] = useState("1");
  const [importText, setImportText] = useState("");
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [planFilter, setPlanFilter] = useState("all");
  const [page, setPage] = useState(1);
  const [toolsOpen, setToolsOpen] = useState(false);
  const [openFilter, setOpenFilter] = useState<"status" | "plan" | null>(null);
  const pageSize = 12;

  const loadPage = useCallback(async (requestedPage: number, announce = false) => {
    try {
      const result = asRow(await getCDKs({
        search,
        status: statusFilter,
        planType: planFilter,
        page: requestedPage,
        pageSize,
      }));
      setCdkRows(rows(result.cdks));
      setCdkMeta(result);
      if (announce) setNotice("CDK 列表已刷新");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }, [pageSize, planFilter, search, setCdkMeta, setCdkRows, setError, setNotice, statusFilter]);

  const refresh = async (announce = false) => loadPage(page, announce);

  useEffect(() => {
    void loadPage(page);
  }, [loadPage, page]);

  const generate = async () => {
    try {
      const result = asRow(await generateCDKs({ count: Number(count), plan_type: plan }));
      const generatedCodes = Array.isArray(result.cdks) ? result.cdks.map(String) : [];
      const generated = generatedCodes.join("\n");
      setNotice(generated ? `已生成 ${generatedCodes.length} 个 CDK：${generated}` : text(result, "message", "CDK 已生成"));
      setPage(1);
      await loadPage(1);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const importRows = async () => {
    try {
      const result = asRow(await importCDKs({ text: importText, plan_type: plan }));
      setNotice(text(result, "message", "CDK 已导入"));
      setImportText("");
      setPage(1);
      await loadPage(1);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const [selected, setSelected] = useState<string[]>([]);
  const total = num(cdkMeta, "total", cdkRows.length);
  const totalPages = num(cdkMeta, "totalPages", 1);
  const visibleRows = cdkRows;

  const codeOf = (row: Row) => text(row, "code", text(row, "cdk_code"));
  const copyAndShip = async (codes: string[]) => {
    if (!codes.length) {
      setError("请选择要复制的 CDK");
      return;
    }
    try {
      await copyText(codes.join("\n"));
      await shipCDKs(codes);
      setNotice(`已复制 ${codes.length} 个 CDK，并标记出库`);
      await loadPage(page);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const togglePage = () => {
    const codes = visibleRows.map(codeOf).filter(Boolean);
    const select = codes.some((code) => !selected.includes(code));
    setSelected((current) => select ? Array.from(new Set([...current, ...codes])) : current.filter((code) => !codes.includes(code)));
  };
  const selectUnused = async () => {
    try {
      const result = asRow(await getCDKs({
        search,
        status: statusFilter,
        planType: planFilter,
        page,
        pageSize,
        selectUnused: true,
      }));
      const codes = Array.isArray(result.selectableCodes) ? result.selectableCodes.map(String) : [];
      setSelected((current) => Array.from(new Set([...current, ...codes])));
      setNotice(codes.length ? `已选中本页 ${codes.length} 个未使用 CDK` : "本页没有未使用的 CDK");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const batchDelete = async () => {
    if (!selected.length) {
      setError("请选择要删除的 CDK");
      return;
    }
    if (!(await confirm(`确定删除选中的 ${selected.length} 个 CDK ?`, "删除 CDK"))) return;
    try {
      await deleteCDKs(selected);
      setSelected([]);
      await loadPage(page);
      setNotice("选中的 CDK 已删除");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const filters = child(cdkMeta, "filters");
  const statusOptions = rows(rowValue(filters, "status")) as Array<{ value: string; label: string }>;
  const planOptions = rows(rowValue(filters, "plan")) as Array<{ value: string; label: string }>;
  const generationPlanOptions = rows(rowValue(filters, "generation")) as Array<{ value: string; label: string }>;
  return (
    <div className="cdk-page" onClick={() => setOpenFilter(null)}>
      <Panel
        title="激活码列表"
        actions={
          <div className="cdk-header-actions">
            <Button className="cdk-refresh-button" variant="primary" onClick={() => void refresh(true)}>
              <RefreshCw className="h-4 w-4" />刷新
            </Button>
            <Button className="cdk-import-button" variant="outline" onClick={() => setToolsOpen((value) => !value)}>
              <Upload className="h-4 w-4" />批量导入
            </Button>
            <input value={search} onChange={(event) => { setSearch(event.target.value); setPage(1); }} className="asset-input cdk-search-input" placeholder="搜索激活码 / 卡密" />
            <select value={plan} onChange={(event) => setPlan(event.target.value)} className="asset-input cdk-plan-input" aria-label="生成套餐">
              {generationPlanOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
            <input type="number" min={1} max={100} value={count} onChange={(event) => setCount(event.target.value)} className="asset-input cdk-count-input" aria-label="生成数量" />
            <Button className="cdk-generate-button" onClick={() => void generate()}>
              <Sparkles className="h-4 w-4" />生成
            </Button>
          </div>
        }
      >
        {toolsOpen ? (
          <div className="import-box active mb-4">
            <div className="import-box-header"><div className="import-box-title">批量导入激活码</div></div>
            <div className="import-box-tip">支持每行一条，或使用逗号、空格分隔。系统会自动去重并忽略空行。</div>
            <textarea rows={4} value={importText} onChange={(event) => setImportText(event.target.value)} className="import-textarea" placeholder={"KC-ABCDEFGHJK23456\nKC-MNPQRSTUVW56789"} />
            <div className="import-actions"><Button variant="outline" onClick={() => void importRows()}><CheckCheck className="h-4 w-4" />解析并导入</Button></div>
          </div>
        ) : null}
        <div className="batch-actions cdk-batch-actions">
          <CdkFilterDropdown filterKey="cdk" label="全部状态" value={statusFilter} options={statusOptions} open={openFilter === "status"} setOpen={(open) => setOpenFilter(open ? "status" : null)} onChange={(value) => { setStatusFilter(value); setPage(1); }} />
          <CdkFilterDropdown filterKey="cdk_plan_type_filter" label="全部套餐" value={planFilter} options={planOptions} open={openFilter === "plan"} setOpen={(open) => setOpenFilter(open ? "plan" : null)} onChange={(value) => { setPlanFilter(value); setPage(1); }} />
          <Button size="sm" variant="outline" onClick={togglePage}><CheckSquare className="h-4 w-4" />本页全选/取消</Button>
          <Button size="sm" variant="outline" onClick={() => void selectUnused()}><SquareCheck className="h-4 w-4" />选中本页未使用</Button>
          <Button size="sm" variant="outline" onClick={() => void copyAndShip(selected)}><Copy className="h-4 w-4" />复制</Button>
          <Button size="sm" variant="outline" onClick={() => void batchDelete()}><Trash2 className="h-4 w-4" />删除</Button>
        </div>
        <DataTable data={visibleRows} empty="暂无 CDK" minWidth={0} tableClassName="cdk-table" columns={[
          { key: "select", label: "选择", render: (row) => <input type="checkbox" checked={selected.includes(codeOf(row))} onChange={(event) => setSelected((current) => event.target.checked ? Array.from(new Set([...current, codeOf(row)])) : current.filter((code) => code !== codeOf(row)))} className="h-4 w-4 accent-cyan-500" /> },
          { key: "code", label: "激活码内容", render: (row) => <button type="button" className="cdk-copy" title="复制并标记出库" onClick={() => void copyAndShip([codeOf(row)])}><code>{codeOf(row)}</code><Copy /></button> },
          { key: "plan_type", label: "套餐", render: (row) => <span className={cn("status-badge cdk-plan-badge", text(row, "plan_class", "cdk-plan-plus"))}>{text(row, "plan_label", "Plus")}</span> },
          { key: "session_preview", label: "Session 记录", render: (row) => <code>{text(row, "session_preview")}</code> },
          { key: "shipped", label: "是否出库", render: (row) => <span className={cn("readonly-switch", text(row, "shipped_class", ""))} title={text(row, "shipped_label", "未出库")} /> },
          { key: "status", label: "状态", render: (row) => <Badge label={text(row, "status_label", text(row, "status", ""))} tone={backendStatusTone(row)} /> },
          { key: "used_at", label: "使用时间", render: (row) => text(row, "used_at_text", text(row, "used_at", "")) },
          { key: "actions", label: "操作", render: (row) => <button type="button" title="删除 CDK" onClick={async () => { if (!(await confirm(`确定删除 CDK: ${codeOf(row)} ?`, "删除 CDK"))) return; try { await deleteCDK(codeOf(row)); await refresh(); setNotice("CDK 已删除"); } catch (reason) { setError(errorMessage(reason)); } }} className="btn-delete"><Trash2 className="h-4 w-4" /></button> },
        ]} />
        <div className="table-footer"><span className="pagination-meta">显示 {total ? (page - 1) * pageSize + 1 : 0}-{Math.min(page * pageSize, total)}，共 {total} 条</span><div className="pagination"><button className="pagination-nav" disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>上一页</button><span>{page} / {totalPages}</span><button className="pagination-nav" disabled={page >= totalPages} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>下一页</button></div></div>
      </Panel>
    </div>
  );
}

function SessionsPanel({
  sessionRows,
  setSessionRows,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const [cdk, setCDK] = useState("");
  const [session, setSession] = useState("");
  const [selected, setSelected] = useState<Row | null>(null);
  const [launchBusy, setLaunchBusy] = useState(false);
  const [launchResult, setLaunchResult] = useState("");
  const [renewalStatuses, setRenewalStatuses] = useState<Record<string, Row>>({});

  const refresh = async (announce = false) => {
    try {
      setRenewalStatuses({});
      setSessionRows(rows(asRow(await getSessions()).sessions));
      if (announce) setNotice("Session 列表已刷新");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const start = async () => {
    setLaunchBusy(true);
    setLaunchResult("正在提交...");
    try {
      const result = asRow(
        await triggerActivation({ cdk, session }),
      );
      const jobKey = text(result, "jobKey", text(result, "job_key", ""));
      const message = jobKey ? `任务已启动：${jobKey}` : text(result, "message", "任务已启动");
      setNotice(`${message}，请到「运行日志」查看进度`);
      setLaunchResult(message);
      setCDK("");
      await refresh();
    } catch (reason) {
      setLaunchResult("");
      setError(errorMessage(reason));
    } finally {
      setLaunchBusy(false);
    }
  };
  const view = async (row: Row) => {
    try {
      setSelected(
        asRow(await getSession(text(row, "job_key", text(row, "jobKey")))),
      );
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };

  useEffect(() => {
    let active = true;
    setRenewalStatuses({});
    void batchRenewalStatus([])
      .then((result) => {
        if (!active) return;
        setRenewalStatuses(asRow(asRow(result).data) as Record<string, Row>);
      })
      .catch(() => {
        if (!active) return;
        setRenewalStatuses({});
      });
    return () => {
      active = false;
    };
  }, [sessionRows]);

  const renderAutoRenewCell = (row: Row) => {
    if (!bool(row, "renewal_visible")) {
      return <span className="session-renewal-muted">—</span>;
    }
    const jobKey = text(row, "job_key", text(row, "jobKey", ""));
    const info = renewalStatuses[jobKey];
    if (!info) return <span className="session-renewal-muted session-renewal-small">查询中…</span>;
    return <Badge label={text(info, "renewalStatusLabel", "—")} tone={backendTone(info, "renewalStatusTone", "neutral")} />;
  };

  const selectedPayload = sessionPayload(selected || undefined);
  const selectedSession = child(selected || undefined, "session");
  const selectedJobKey = text(selectedSession, "job_key", text(selected || undefined, "job_key", text(selected || undefined, "jobKey", "")));
  const selectedTime = text(selectedSession, "time", text(selectedSession, "created_at", text(selected || undefined, "time", text(selected || undefined, "created_at", ""))));
  const copySession = async (row: Row) => {
    try {
      const result = asRow(await getSession(text(row, "job_key", text(row, "jobKey", ""))));
      const value = sessionPayload(result);
      if (!value) throw new Error("该记录没有完整 Session 可复制");
      await copyText(value);
      setNotice("Session 已复制到剪贴板");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const exportSession = async (row: Row) => {
    try {
      const jobKey = text(row, "job_key", text(row, "jobKey", ""));
      if (!jobKey) throw new Error("Session 任务标识为空");
      downloadBlob(await downloadSession(jobKey), `session_${jobKey}.json`);
      setNotice("Session 已导出");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const deleteSession = async (row: Row) => {
    const jobKey = text(row, "job_key", text(row, "jobKey", ""));
    if (!(await confirm("确定删除这条任务记录吗？", "删除任务记录"))) return;
    try {
      await deleteTaskLog(jobKey);
      setNotice("任务记录已删除");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const changeRenewal = async (row: Row, action: "cancel" | "enable") => {
    const jobKey = text(row, "job_key", text(row, "jobKey", ""));
    if (!(await confirm(action === "cancel" ? "确认要关闭该 Session 对应账号的自动续费吗？" : "确认要开启该 Session 对应账号的自动续费吗？", action === "cancel" ? "取消自动续费" : "开启自动续费"))) return;
    try {
      if (!jobKey) throw new Error("Session 任务标识为空");
      const response = asRow(await changeSessionRenewal(jobKey, action));
      const data = asRow(response.data);
      setRenewalStatuses((current) => ({
        ...current,
        [jobKey]: data,
      }));
      setNotice(`${text(response, "message", action === "cancel" ? "自动续费已关闭" : "自动续费已开启")}${text(data, "email", "") ? `（${text(data, "email", "")}）` : ""}`);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const copySelectedSession = async () => {
    if (!selectedPayload) {
      setError("没有可复制的 Session 内容");
      return;
    }
    try {
      await copyText(selectedPayload);
      setNotice("Session 已复制到剪贴板");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const exportSelectedSession = async () => {
    if (!selectedJobKey) {
      setError("没有可导出的 Session 记录");
      return;
    }
    try {
      downloadBlob(await downloadSession(selectedJobKey), `session_${selectedJobKey}.json`);
      setNotice("Session 已导出");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };

  return (
    <div className="sessions-page">
      <section className="panel session-launch-panel">
        <div className="panel-header">
          <h2 className="panel-title">手动启动自助开通</h2>
        </div>
        <div className="session-launch-body">
          <p className="session-launch-description">
            粘贴从 <code>chatgpt.com/api/auth/session</code> 复制的 JSON，或只贴 AccessToken。启动后请到「运行日志」查看实时输出。
          </p>
          <div className="session-launch-form">
            <input
              aria-label="输入未使用的自助 CDK"
              value={cdk}
              onChange={(event) => setCDK(event.target.value)}
              className="asset-input"
              placeholder="输入未使用的自助 CDK"
            />
            <textarea
              aria-label="粘贴 Session JSON 或 AccessToken"
              rows={5}
              value={session}
              onChange={(event) => setSession(event.target.value)}
              className="import-textarea"
              placeholder="粘贴 Session JSON 或 AccessToken"
            />
            <div className="session-launch-actions">
              <Button className="session-start-button" variant="outline" disabled={launchBusy} onClick={() => void start()}>
                <Play className="h-4 w-4" />
                {launchBusy ? "提交中..." : "启动自动化开通"}
              </Button>
              <span className="session-launch-result">{launchResult}</span>
            </div>
          </div>
        </div>
      </section>
      <section className="panel session-history-panel">
        <div className="panel-header">
          <h2 className="panel-title">Session 记录列表</h2>
          <Button className="session-refresh-button" onClick={() => void refresh(true)}>
            <RefreshCw className="h-4 w-4" />刷新
          </Button>
        </div>
        <DataTable
          data={sessionRows}
          empty="暂无 Session 记录"
          minWidth={0}
          tableClassName="sessions-table"
          columns={[
            {
              key: "time",
              label: "执行时间",
              render: (row) =>
                text(row, "time", text(row, "created_at", "")),
            },
            { key: "cdk_code", label: "激活码" },
            {
              key: "token_preview",
              label: "Session",
              render: (row) => {
                const preview = text(row, "token_preview");
                const canView = rowValue(row, "has_session") !== false;
                return canView ? (
                  <button type="button" className="session-preview-link" title="点击查看完整 Session" onClick={() => void view(row)}>
                    {preview}
                  </button>
                ) : <code>{preview}</code>;
              },
            },
            { key: "card_last4", label: "卡尾号", render: (row) => text(row, "card_last4", "-") },
            { key: "message", label: "任务信息", render: (row) => text(row, "message", "-") },
            {
              key: "status",
              label: "状态",
              render: (row) => (
                <Badge
                  label={text(row, "status_label", text(row, "status"))}
                  tone={backendStatusTone(row)}
                />
              ),
            },
            { key: "auto_renew", label: "自动续费", render: renderAutoRenewCell },
            {
              key: "actions",
              label: "操作",
              render: (row) => {
                const jobKey = text(row, "job_key", text(row, "jobKey", ""));
                const info = renewalStatuses[jobKey];
                const canCancel = info ? bool(info, "canCancel") : false;
                const canEnable = info ? bool(info, "canEnable") : false;
                return (
                  <div className="table-action-group">
                    <Button size="sm" className="session-row-action session-copy-button" onClick={() => void copySession(row)}>复制</Button>
                    <Button size="sm" variant="outline" className="session-row-action session-export-button" onClick={() => void exportSession(row)}>导出</Button>
                    {canCancel ? <Button size="sm" variant="danger" className="session-row-action session-renew-cancel" onClick={() => void changeRenewal(row, "cancel")}>取消续费</Button> : null}
                    {canEnable ? <Button size="sm" variant="outline" className="session-row-action session-renew-enable" onClick={() => void changeRenewal(row, "enable")}>开启续费</Button> : null}
                    <button type="button" className="btn-delete session-delete-button" title="删除此任务记录" onClick={() => void deleteSession(row)}>
                      <Trash2 />
                    </button>
                  </div>
                );
              },
            },
          ]}
        />
      </section>
      {selected ? (
        <div className="screenshot-modal-overlay open" role="presentation" onClick={() => setSelected(null)}>
          <div className="session-details-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}>
            <div className="screenshot-modal-header">
              <h3>Session 详情</h3>
              <div className="session-modal-actions">
                <Button size="sm" className="session-modal-copy" onClick={() => void copySelectedSession()}><Clipboard className="h-4 w-4" />复制</Button>
                <Button size="sm" variant="outline" onClick={exportSelectedSession}><Download className="h-4 w-4" />导出</Button>
                <Button size="sm" variant="outline" onClick={() => setSelected(null)}>关闭</Button>
              </div>
            </div>
            <p className="session-modal-meta">任务: {selectedJobKey} · CDK: {text(selectedSession, "cdk_code", text(selected || undefined, "cdk_code", "-"))} · {selectedTime}</p>
            <pre className="session-modal-pre">{selectedPayload || "该记录未保存完整 Session（仅旧任务有摘要）。请重新提交一次开通以保存完整内容。"}</pre>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function RenewalPanel({ setNotice, setError }: ContentProps) {
  const [session, setSession] = useState("");
  const [result, setResult] = useState<Row>({});
  const run = async (action: (body: JsonMap) => Promise<JsonMap>) => {
    try {
      const value = asRow(await action({ session }));
      setResult(value);
      setNotice(text(value, "message", "请求已完成"));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  return (
    <div className="renewal-page">
      <section className="panel renewal-panel">
        <h2 className="panel-title">Session</h2>
        <p className="renewal-description">从 <code>chatgpt.com/api/auth/session</code> 复制完整 JSON，或只粘贴 AccessToken。</p>
        <textarea
          rows={8}
          value={session}
          onChange={(event) => setSession(event.target.value)}
          className="import-textarea renewal-session-textarea"
          placeholder="粘贴 Session JSON 或 AccessToken"
        />
        <div className="renewal-actions">
          <Button variant="danger" className="renewal-action" onClick={() => void run(cancelRenewal)}>
            <CircleX />
            取消自动续费
          </Button>
          <Button variant="outline" className="renewal-action btn-success" onClick={() => void run(enableRenewal)}>
            <RefreshCw />
            开启自动续费
          </Button>
          <Button variant="outline" className="renewal-action" onClick={() => setSession("")}>
            清空
          </Button>
        </div>
      </section>
      {Object.keys(result).length ? (
        <Panel title="操作结果">
          <div className="grid gap-3 text-sm text-slate-700">
            <p>{text(child(result, "result_view"), "message", text(result, "message", "操作完成"))}</p>
            <p>账号：{text(child(result, "result_view"), "email", "-")}</p>
            <p>订阅渠道：{text(child(result, "result_view"), "subscription_label", "-")}</p>
            <p>自动续费：<Badge label={text(child(result, "result_view"), "status_label", "-")} tone={backendTone(child(result, "result_view"), "status_tone")} /></p>
            <p>到期时间：{text(child(result, "result_view"), "expires_at", "-")} · 剩余：{text(child(result, "result_view"), "remaining_days", "-")}</p>
          </div>
        </Panel>
      ) : null}
    </div>
  );
}

function TaskPanel({
  taskRows,
  setTaskRows,
  taskMeta,
  setTaskMeta,
  setNotice,
  setError,
  confirm,
  automation,
}: ContentProps & { automation: boolean }) {
  const [paused, setPaused] = useState(false);
  const [media, setMedia] = useState<{
    screenshots: string[];
    videos: string[];
    message?: string;
  } | null>(null);
  const [page, setPage] = useState(1);
  const pageSize = 12;
  const refresh = useCallback(async (showToast = false) => {
    try {
      const result = asRow(await getTaskLogs({ page, pageSize }));
      setTaskRows(rows(result.logs || result.tasks));
      setTaskMeta(result);
      if (showToast) setNotice("任务列表已刷新");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }, [page, pageSize, setError, setNotice, setTaskMeta, setTaskRows]);
  const total = num(taskMeta, "total", taskRows.length);
  const totalPages = num(taskMeta, "totalPages", 1);
  const visibleRows = taskRows;
  useEffect(() => {
    if (paused) return;
    const timer = window.setInterval(() => void refresh(), 3000);
    return () => window.clearInterval(timer);
  }, [paused, refresh]);
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);
  const taskKey = (row: Row) =>
    text(row, "job_key", text(row, "jobKey", text(row, "id", "")));
  const automationData = (row: Row) => child(row, "automation");
  const automationPhase = (row: Row) => text(automationData(row), "phase_label", "-");
  const automationStages = (row: Row) => {
    const stages = rows(rowValue(automationData(row), "stages"));
    if (!stages.length) return <span className="automation-stage-empty">-</span>;
    return (
      <>
        {stages.map((stage, index) => {
          return (
            <span
              key={`${text(stage, "key", "stage")}-${index}`}
              className={cn("automation-stage", text(stage, "state", "pending"))}
            >
              {text(stage, "label", "-")}
            </span>
          );
        })}
      </>
    );
  };
  const taskInfo = (row: Row) => {
    const details = rows(rowValue(row, "task_info_lines"));
    return details.length ? (
      <div className="task-info-cell">{details.map((line, index) => <span key={`${text(line, "kind", "line")}-${index}`} className={text(line, "class_name", "")}>{text(line, "text", "-")}</span>)}</div>
    ) : (
      <span>-</span>
    );
  };
  const paginationItems = () => {
    if (totalPages <= 7) {
      return Array.from({ length: totalPages }, (_, index) => index + 1);
    }
    const pages = new Set([1, totalPages, page - 1, page, page + 1]);
    if (page <= 3) {
      pages.add(2);
      pages.add(3);
      pages.add(4);
    }
    if (page >= totalPages - 2) {
      pages.add(totalPages - 1);
      pages.add(totalPages - 2);
      pages.add(totalPages - 3);
    }
    const sorted = [...pages]
      .filter((item) => item >= 1 && item <= totalPages)
      .sort((left, right) => left - right);
    const result: Array<number | "ellipsis"> = [];
    sorted.forEach((item, index) => {
      if (index > 0 && item - sorted[index - 1] > 1) result.push("ellipsis");
      result.push(item);
    });
    return result;
  };
  const mediaButtons = (row: Row) => {
    const screenshotPaths = mediaPathList(rowValue(row, "screenshots"));
    const videoPaths = mediaPathList(rowValue(row, "videos"));
    const message = text(row, "message", "");
    return (
      <div className="task-media-cell">
        {screenshotPaths.length ? (
          <button
            type="button"
            className="btn btn-primary task-media-button task-screenshot-button"
            onClick={() => setMedia({ screenshots: screenshotPaths, videos: [], message })}
          >
            截图 ({screenshotPaths.length})
          </button>
        ) : null}
        {videoPaths.length ? (
          <button
            type="button"
            className="btn btn-secondary task-media-button task-video-button"
            onClick={() => setMedia({ screenshots: [], videos: videoPaths, message })}
          >
            ▶ 录像
          </button>
        ) : null}
        {!screenshotPaths.length && !videoPaths.length ? (
          <span className="task-media-empty">任务结束后可刷新查看</span>
        ) : null}
      </div>
    );
  };
  return (
    <div>
      <section className="panel task-content-panel">
        <div className="runtime-log-toolbar log-page-toolbar">
          {!automation ? <Button size="sm" variant="outline" onClick={() => setPaused((value) => !value)}>{paused ? <Play className="h-4 w-4" /> : <Pause className="h-4 w-4" />}{paused ? "恢复自动刷新" : "停止自动刷新"}</Button> : null}
          <Button size="sm" onClick={() => void refresh(true)}><RefreshCw className="h-4 w-4" />{automation ? "刷新任务" : "立即刷新"}</Button>
          <span className="log-page-toolbar-hint">{automation ? "绿色阶段=已完成，红色=失败，灰色=未到达。Checkout 列可一眼确认支付页是否打开。" : "默认每 3 秒自动拉取概览与下方表格；停止后仅在你点击「立即刷新」时更新。可在表格中删除单条任务记录。前台自助开通由子进程执行，无法在此强制杀进程，可开维护模式拒绝新单。"}</span>
        </div>
        <DataTable
          data={visibleRows}
          empty={automation ? "暂无自动化任务记录" : "暂无任务记录"}
          minWidth={0}
          tableClassName="task-table"
          columns={automation ? [
            { key: "created_at", label: "执行时间", width: "160px", render: (row) => text(row, "time", text(row, "created_at", "")) },
            { key: "job_key", label: "任务 ID", width: "130px", render: (row) => <code title={taskKey(row)}>{text(row, "job_key_short", "-")}</code> },
            { key: "cdk_code", label: "激活码", width: "130px", render: (row) => <code>{text(row, "cdk", text(row, "cdk_code"))}</code> },
            { key: "checkout", label: "Checkout", width: "110px", render: (row) => { const automation = automationData(row); const CheckoutIcon = text(automation, "checkout_icon", "close") === "check" ? CheckCircle2 : CircleX; return <span className={cn("automation-checkout-badge", text(automation, "checkout_class", "no"))}><CheckoutIcon /> {text(automation, "checkout_label", "未打开")}</span>; } },
            { key: "automation_stage", label: "自动化阶段", render: automationStages },
            { key: "current_stage", label: "当前阶段", width: "120px", render: (row) => automationPhase(row) },
            { key: "status", label: "状态", width: "100px", render: (row) => <LegacyTaskStatusBadge row={row} /> },
            { key: "media", label: "截图/录像", width: "110px", render: mediaButtons },
          ] : [
            { key: "created_at", label: "执行时间", render: (row) => text(row, "time", text(row, "created_at", "")) },
            { key: "cdk_code", label: "激活码", render: (row) => <code>{text(row, "cdk", text(row, "cdk_code"))}</code> },
            { key: "token_preview", label: "Token 摘要", render: (row) => <code>{text(row, "token_preview")}</code> },
            { key: "message", label: "任务信息", render: taskInfo },
            { key: "progress", label: "进度", render: (row) => { const progress = num(row, "progress"); return <div className="task-progress-cell"><div className="task-progress-value">{progress}%</div><div className="progress-mini-track"><div className={cn("progress-mini-bar", text(row, "status", "running"))} style={{ width: `${progress}%` }} /></div></div>; } },
            { key: "status", label: "状态", render: (row) => <LegacyTaskStatusBadge row={row} /> },
            { key: "media", label: "截图/录像", render: mediaButtons },
            {
              key: "actions",
              label: "操作",
              render: (row) => (
                <div className="task-action-cell">
                  <button type="button" title="删除任务记录" onClick={async () => { if (!(await confirm(`确定删除任务记录「${taskKey(row)}」？仅删除数据库中的本条记录，不会强制终止正在运行的子进程。`, "删除任务"))) return; try { await deleteTaskLog(taskKey(row)); setNotice("任务记录已删除"); await refresh(); } catch (reason) { setError(errorMessage(reason)); } }} className="btn-delete"><Trash2 /></button>
                </div>
              ),
            },
          ]}
        />
        <div className="table-footer">
          <span className="pagination-meta">共 {total} 条，当前显示 {total ? (page - 1) * pageSize + 1 : 0}-{Math.min(page * pageSize, total)}</span>
          <div className="pagination">
            <button className="pagination-nav" disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>上一页</button>
            {paginationItems().map((item, index) => item === "ellipsis" ? <span key={`ellipsis-${index}`} className="pagination-ellipsis">...</span> : <button key={item} className={item === page ? "active" : ""} onClick={() => setPage(item)}>{item}</button>)}
            <button className="pagination-nav" disabled={page >= totalPages} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>下一页</button>
          </div>
        </div>
      </section>
      {media ? <TaskMediaModal media={media} close={() => setMedia(null)} /> : null}
    </div>
  );
}

function TaskMediaModal({
  media,
  close,
}: {
  media: { screenshots: string[]; videos: string[]; message?: string };
  close: () => void;
}) {
  const [loaded, setLoaded] = useState<Array<{ kind: "screenshot" | "video"; path: string; url?: string; error?: string }>>([]);
  useEffect(() => {
    let disposed = false;
    const urls: string[] = [];
    setLoaded([]);
    const load = async () => {
      const items = [
        ...media.screenshots.map((path) => ({ kind: "screenshot" as const, path })),
        ...media.videos.map((path) => ({ kind: "video" as const, path })),
      ];
      const result = await Promise.all(items.map(async (item) => {
        try {
          const url = URL.createObjectURL(await downloadAdminMedia(item.kind === "video" ? "video" : "screenshots", item.path));
          urls.push(url);
          return { ...item, url };
        } catch (reason) {
          return { ...item, error: errorMessage(reason) };
        }
      }));
      if (!disposed) setLoaded(result);
    };
    void load();
    return () => {
      disposed = true;
      urls.forEach((url) => URL.revokeObjectURL(url));
    };
  }, [media]);
  const markMediaError = (path: string, reason: string) => {
    setLoaded((items) => items.map((item) => item.path === path ? { ...item, error: reason, url: undefined } : item));
  };
  return (
    <div className="screenshot-modal-overlay open" role="presentation" onClick={close}>
      <div className="screenshot-modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}>
        <div className="screenshot-modal-header"><h3>截图 / 录像</h3><button type="button" className="btn btn-success task-modal-close" onClick={close}>关闭</button></div>
        <div className="screenshot-modal-body">
          <p className="task-media-description">{media.videos.length ? "自动化全程录像（可拖动进度条查看卡在哪一步）" : media.message || "自动化连续失败，请根据截图人工处理 Stripe 页面"}</p>
          {loaded.length ? loaded.map((item) => <div key={`${item.kind}-${item.path}`} className="task-media-item"><p className="task-media-path">{item.path}</p>{item.error ? <p className="task-media-error">{item.error}</p> : item.kind === "video" ? <video src={item.url} controls autoPlay muted className="task-media-video" onError={() => markMediaError(item.path, "录像加载失败，请检查文件格式或媒体接口") } /> : <img src={item.url} alt={item.path} className="task-media-image" onError={() => markMediaError(item.path, "截图加载失败，请检查文件是否存在") } />}</div>) : <p className="task-media-loading">加载媒体中...</p>}
        </div>
      </div>
    </div>
  );
}

function BillingPanel({
  billingRows,
  setBillingRows,
  billingMeta,
  setBillingMeta,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const [filters, setFilters] = useState({
    start_date: "",
    end_date: "",
    card_last4: "",
    plan_type: "",
    status: "",
  });
  const [page, setPage] = useState(1);
  const [summary, setSummary] = useState<Row>({});
  const [summaryCard, setSummaryCard] = useState("");
  const billingCardKey = (row: Row) => text(row, "card_last4", "");
  const billingFilterOptions = child(billingMeta, "filters");
  const planOptions = rows(rowValue(billingFilterOptions, "plan_type"));
  const statusOptions = rows(rowValue(billingFilterOptions, "status"));
  useEffect(() => {
    if (!Object.keys(summary).length) return;
    document.querySelector<HTMLElement>(".billing-summary-panel")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }, [summary]);
  const query = (requestedPage = page) => {
    const params = new URLSearchParams(Object.entries(filters).filter(([, value]) => value));
    params.set("page", String(requestedPage));
    params.set("page_size", "20");
    return params;
  };
  const refresh = async () => {
    try {
      const result = asRow(await getBilling(query().toString()));
      setBillingRows(rows(result.records || result.billing));
      setBillingMeta(result);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const loadPage = async (requestedPage: number) => {
    setPage(requestedPage);
    try {
      const result = asRow(await getBilling(query(requestedPage).toString()));
      setBillingRows(rows(result.records || result.billing));
      setBillingMeta(result);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const showSummary = async (last4: string) => {
    if (!last4) return;
    try {
      setSummaryCard(last4);
      setSummary(asRow(await getBillingSummary(last4)));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const exportRows = async () => {
    try {
      const exportParams = new URLSearchParams(Object.entries(filters).filter(([, value]) => value));
      downloadBlob(
        await downloadBilling(exportParams.toString()),
        `billing_export_${new Date().toISOString().slice(0, 10)}.csv`,
      );
      setNotice("CSV 导出成功");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  const total = num(billingMeta, "total", billingRows.length);
  const totalPages = Math.max(1, num(billingMeta, "totalPages", 1));
  const pageItems = totalPages <= 7
    ? Array.from({ length: totalPages }, (_, index) => index + 1)
    : [1, ...(page > 3 ? ["..."] : []), ...Array.from(new Set([page - 1, page, page + 1])).filter((value) => value > 1 && value < totalPages), ...(page < totalPages - 2 ? ["..."] : []), totalPages];
  return (
    <div className="billing-page">
      <Panel title="筛选条件" actions={<div className="billing-toolbar"><Button onClick={() => void loadPage(1)}><Search className="h-6 w-6" />查询</Button><Button variant="outline" className="btn-success" onClick={() => void exportRows()}><Download className="h-6 w-6" />导出 CSV</Button><Button variant="danger" className="btn-delete" onClick={async () => { if (!(await confirm("确定清除所有失败状态的账单记录吗？此操作不可恢复。", "清除失败账单"))) return; try { const result = asRow(await deleteFailedBilling()); setNotice(`已清除 ${num(result, "deleted")} 条失败账单`); await loadPage(1); } catch (reason) { setError(errorMessage(reason)); } }}><Trash2 className="h-6 w-6" />清除失败记录</Button></div>}>
        <div className="billing-filter-grid">
          {[
            ["start_date", "开始日期"],
            ["end_date", "结束日期"],
            ["card_last4", "卡片后四位"],
          ].map(([key, label]) => (
            <div key={key} className="billing-filter-group">
              <label className="billing-filter-field">{label}</label>
              <input
                type={key.includes("date") ? "date" : "text"}
                value={filters[key as keyof typeof filters]}
                onChange={(event) =>
                  setFilters({ ...filters, [key]: event.target.value })
                }
                className="billing-filter-input asset-input"
                placeholder={key === "card_last4" ? "例如 1234" : undefined}
              />
            </div>
          ))}
          <div className="billing-filter-group">
            <label className="billing-filter-field">套餐类型</label>
            <select
              value={filters.plan_type}
              onChange={(event) =>
                setFilters({ ...filters, plan_type: event.target.value })
              }
              className="billing-filter-input asset-input"
            >
              {planOptions.map((option) => <option key={text(option, "value")} value={text(option, "value")}>{text(option, "label")}</option>)}
            </select>
          </div>
          <div className="billing-filter-group">
            <label className="billing-filter-field">支付状态</label>
            <select
              value={filters.status}
              onChange={(event) =>
                setFilters({ ...filters, status: event.target.value })
              }
              className="billing-filter-input asset-input"
            >
              {statusOptions.map((option) => <option key={text(option, "value")} value={text(option, "value")}>{text(option, "label")}</option>)}
            </select>
          </div>
        </div>
      </Panel>
      <Panel
        title="账单列表"
        actions={<span className="billing-total-hint">共 {total} 条记录</span>}
      >
        <DataTable
          data={billingRows}
          empty="暂无账单记录"
          minWidth={0}
          tableClassName="billing-table"
          columns={[
            {
              key: "payment_time",
              label: "支付时间",
              render: (row) => text(row, "payment_time_text", text(row, "payment_time", "")),
            },
            { key: "card_last4", label: "卡号", render: (row) => <button type="button" className="billing-card-link" onClick={() => void showSummary(billingCardKey(row))}>{text(row, "card_number", text(row, "card_last4"))}</button> },
            {
              key: "amount",
              label: "金额",
              render: (row) => text(row, "amount_text", text(row, "amount", "")),
            },
            { key: "currency", label: "币种" },
            { key: "plan_type", label: "套餐类型", render: (row) => text(row, "plan_label", text(row, "plan_type", "")) },
            { key: "cdk_code", label: "CDK", render: (row) => <span className="billing-cdk">{text(row, "cdk_code")}</span> },
            { key: "email", label: "邮箱", render: (row) => <span className="billing-email">{text(row, "email")}</span> },
            {
              key: "status",
              label: "状态",
              render: (row) => <Badge label={text(row, "status_label", text(row, "status"))} tone={backendStatusTone(row)} />,
            },
            {
              key: "actions",
              label: "操作",
              render: (row) => (
                <button
                  type="button"
                  title="删除账单"
                  onClick={async () => {
                    if (!(await confirm("确定删除这条账单记录吗？", "删除账单"))) return;
                    try {
                      await deleteBilling(text(row, "id"));
                      setNotice("账单记录已删除");
                      await refresh();
                    } catch (reason) {
                      setError(errorMessage(reason));
                    }
                  }}
                  className="btn-delete"
                >
                  <Trash2 className="h-6 w-6" />
                </button>
              ),
            },
          ]}
        />
        <div className="table-footer">
          <span className="pagination-meta">显示 {total ? (page - 1) * 20 + 1 : 0}-{Math.min(page * 20, total)}，共 {total} 条</span>
          <div className="pagination"><button className="pagination-nav" disabled={page <= 1} onClick={() => void loadPage(page - 1)}>上一页</button>{pageItems.map((item, index) => item === "..." ? <span key={`ellipsis-${index}`} className="pagination-ellipsis">...</span> : <button key={item} className={item === page ? "active" : ""} onClick={() => void loadPage(item as number)}>{item}</button>)}<button className="pagination-nav" disabled={page >= totalPages} onClick={() => void loadPage(page + 1)}>下一页</button></div>
        </div>
      </Panel>
      {Object.keys(summary).length ? <Panel className="billing-summary-panel" title={summaryCard ? `卡片 **** ${summaryCard} 消费汇总` : "卡片消费汇总"} actions={<Button variant="outline" className="btn-success" onClick={() => { setSummary({}); setSummaryCard(""); }}><X className="h-6 w-6" />关闭</Button>}><div className="stat-grid"><InfoItem label="累计消费金额" value={text(summary, "cumulative_amount_text", text(summary, "cumulative_amount", "0.00"))} /><InfoItem label="成功支付次数" value={num(summary, "success_count")} /><InfoItem label="失败支付次数" value={num(summary, "failed_count")} /></div></Panel> : null}
    </div>
  );
}

function RuntimePanel({
  runtimeRows,
  setRuntimeRows,
  setNotice,
  setError,
  confirm,
}: ContentProps) {
  const [autoScroll, setAutoScroll] = useState(true);
  const [paused, setPaused] = useState(false);
  const afterRef = useRef(0);
  const runtimeRowsRef = useRef(runtimeRows);
  const wrapRef = useRef<HTMLDivElement>(null);
  const refresh = useCallback(async () => {
    try {
      const result = asRow(await getRuntimeLogs(1500, { tail: true }));
      const nextRows = rows(result.entries || result.logs);
      runtimeRowsRef.current = nextRows;
      setRuntimeRows(nextRows);
      afterRef.current = Number(result.nextAfter || 0);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }, [setError, setRuntimeRows]);
  const poll = useCallback(async () => {
    if (paused) return;
    try {
      const result = asRow(await getRuntimeLogs(1000, { after: afterRef.current }));
      const entries = rows(result.entries || result.logs);
      if (entries.length) {
        const nextRows = [...runtimeRowsRef.current, ...entries].slice(-1500);
        runtimeRowsRef.current = nextRows;
        setRuntimeRows(nextRows);
      }
      if (result.nextAfter !== undefined) afterRef.current = Number(result.nextAfter);
    } catch {
      // The next poll retries after transient API/Worker outages.
    }
  }, [paused, setRuntimeRows]);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void poll(), 2000);
    return () => window.clearInterval(timer);
  }, [poll, refresh]);
  useEffect(() => {
    if (!autoScroll) return;
    return scrollLogToBottom(wrapRef.current);
  }, [autoScroll, runtimeRows]);
  return (
    <section className="panel runtime-content-panel">
      <div className="runtime-log-toolbar">
        <label className="runtime-log-toggle"><input type="checkbox" checked={autoScroll} onChange={(event) => setAutoScroll(event.target.checked)} /><span>自动滚动到底部</span></label>
        <label className="runtime-log-toggle"><input type="checkbox" checked={paused} onChange={(event) => setPaused(event.target.checked)} /><span>暂停拉取</span></label>
        <Button size="sm" onClick={() => void refresh()}><RefreshCw className="h-4 w-4" />立即刷新</Button>
        <Button size="sm" variant="danger" onClick={async () => { if (!(await confirm("确定清空当前内存中的运行日志？（不影响任务管理数据库表）", "清空运行日志"))) return; try { await clearRuntimeLogs(); setNotice("运行日志已清空"); await refresh(); } catch (reason) { setError(errorMessage(reason)); } }}><Trash2 className="h-4 w-4" />清空日志</Button>
      </div>
        <p className="runtime-log-hint">与「任务管理」中的数据库记录不同：此处为实时流水，便于排查 Playwright / 子进程输出；筛选 Job 可在文本内搜索。</p>
      <div ref={wrapRef} className="runtime-log-pre-wrap">
      <pre className="runtime-log-pre">
        {runtimeRows.length
          ? runtimeRows
              .map(
                (row) =>
                  text(row, "line", text(row, "text", text(row, "message", ""))),
              )
              .join("\n")
          : "暂无运行日志"}
      </pre>
      </div>
    </section>
  );
}

function LoginLogPanel({ loginRows, setLoginRows, setError }: ContentProps) {
  const refresh = async () => {
    try {
      setLoginRows(rows(asRow(await getLoginLogs(200)).logs));
    } catch (reason) {
      setError(errorMessage(reason));
    }
  };
  return (
    <Panel
      title="最近登录记录"
      className="admin-login-panel"
      actions={<Button variant="outline" onClick={() => void refresh()}><RefreshCw className="h-6 w-6" />刷新</Button>}
    >
      <DataTable
        data={loginRows}
        empty="暂无登录记录"
        minWidth={0}
        tableClassName="admin-login-table"
        tableContainerClassName="admin-login-table-container"
        columns={[
          {
            key: "created_at",
            label: "时间",
            render: (row) => text(row, "created_at_text", text(row, "created_at", "")),
          },
          {
            key: "event",
            label: "事件",
            render: (row) => text(row, "event_label", text(row, "event", "")),
          },
          { key: "admin_email", label: "账号", render: (row) => text(row, "admin_email", "") },
          { key: "ip", label: "IP", render: (row) => text(row, "ip", "") },
          {
            key: "fingerprint",
            label: "指纹",
            render: (row) => {
              return <span title={text(row, "fingerprint", "")}>{text(row, "fingerprint_preview", "")}</span>;
            },
          },
          {
            key: "user_agent",
            label: "浏览器",
            render: (row) => {
              return <span title={text(row, "user_agent", "")}>{text(row, "user_agent_preview", "")}</span>;
            },
          },
          { key: "detail", label: "详情", render: (row) => text(row, "detail", "") },
        ]}
      />
    </Panel>
  );
}
