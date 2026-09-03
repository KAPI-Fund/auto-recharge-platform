import { cn } from "@/lib/utils";

export function Badge({ label, tone = "neutral" }: { label: string; tone?: "neutral" | "success" | "warning" | "danger" | "info" }) {
  const legacyTone = tone === "success" ? "status-success" : tone === "danger" ? "status-failed" : tone === "warning" ? "status-warning" : tone === "info" ? "status-running" : "status-neutral";
  return <span className={cn("status-badge inline-flex items-center rounded-full px-2.5 py-1 text-xs font-semibold", legacyTone, {
    "bg-slate-100 text-slate-600": tone === "neutral",
    "bg-emerald-50 text-emerald-700": tone === "success",
    "bg-amber-50 text-amber-700": tone === "warning",
    "bg-red-50 text-red-700": tone === "danger",
    "bg-cyan-50 text-cyan-700": tone === "info",
  })}>{label}</span>;
}
