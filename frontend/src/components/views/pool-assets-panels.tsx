"use client";

import {
	Download,
	Eye,
	Mail,
	PackageCheck,
	Play,
	RefreshCw,
	RotateCcw,
	Save,
	Smartphone,
	Square,
	Trash2,
  Upload,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Children, useEffect, useState, type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  deletePhone,
  deletePoolEmail,
	deleteProduct,
	downloadProduct,
	downloadProducts,
	generateProducts,
	getProductGeneration,
	getPhones,
  getPoolEmails,
  getProducts,
  importPhones,
  importPoolEmails,
	importProducts,
	previewPoolEmail,
	resumeProducts,
	stopProducts,
  updatePhone,
  updateProductStatus,
} from "@/lib/legacy-api";

type Row = Record<string, unknown>;
type ConfirmAction = (message: string, title?: string) => Promise<boolean>;

const inputClass =
  "h-10 w-full rounded-md border border-slate-200 bg-slate-50 px-3 text-sm text-slate-900 outline-none transition focus:border-cyan-400 focus:bg-white";
const areaClass =
  "w-full resize-y rounded-md border border-slate-200 bg-slate-50 p-3 font-mono text-xs leading-5 text-slate-900 outline-none transition focus:border-cyan-400 focus:bg-white";

function value(row: Row, key: string, fallback = "") {
  const item = row[key];
  return item === null || item === undefined ? fallback : String(item);
}

type AssetTone = "neutral" | "success" | "warning" | "danger" | "info";

function serverTone(row: Row, key: string, fallback: AssetTone = "neutral"): AssetTone {
  const tone = value(row, key, fallback);
  return ["neutral", "success", "warning", "danger", "info"].includes(tone)
    ? (tone as AssetTone)
    : fallback;
}

function enabled(row: Row) {
  const item = row.is_active ?? row.active;
  return item === true || item === 1 || item === "1" || item === "true";
}

function assetPanel(
  title: string,
  description: string,
  icon: LucideIcon,
  children: ReactNode,
) {
  const Icon = icon;
  return (
    <section className="rounded-lg border border-slate-200 bg-white shadow-[0_14px_45px_rgba(16,32,51,0.05)]">
      <div className="flex items-start gap-3 border-b border-slate-100 p-5 md:p-6">
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-slate-950 text-cyan-300">
          <Icon className="h-4 w-4" />
        </span>
        <div>
          <h2 className="text-lg font-semibold text-slate-950">{title}</h2>
          <p className="mt-1 text-sm leading-5 text-slate-500">{description}</p>
        </div>
      </div>
      <div className="p-5 md:p-6">{children}</div>
    </section>
  );
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

export function PoolEmailsPanel({
  rows,
  setRows,
  setNotice,
  setError,
  confirm,
}: {
  rows: Row[];
  setRows: (rows: Row[]) => void;
  setNotice: (message: string) => void;
  setError: (message: string) => void;
  confirm: ConfirmAction;
}) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState<Row | null>(null);

  async function refresh() {
    setRows(((await getPoolEmails()).items as Row[]) || []);
  }

  async function importRows() {
    setBusy(true);
    try {
      const result = await importPoolEmails(input);
      await refresh();
      setInput("");
      setNotice(`邮箱池已导入 ${value(result, "applied", "0")} 条`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "邮箱池导入失败");
    } finally {
      setBusy(false);
    }
  }

  return assetPanel(
    "邮箱池",
    "支持 email----password----client_id----refresh_token、制表符和空格格式；预览会在 Worker 侧通过 IMAP 读取。",
    Mail,
    <div className="space-y-5">
      <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto]">
        <textarea
          value={input}
          onChange={(event) => setInput(event.target.value)}
          rows={5}
          className={areaClass}
          placeholder="email@example.com----password\n或 email@example.com----password----client_id----refresh_token"
          aria-label="邮箱池导入内容"
        />
        <div className="flex gap-2 lg:flex-col">
          <Button onClick={() => void importRows()} disabled={busy}>
            <Upload className="h-4 w-4" />
            {busy ? "导入中" : "导入邮箱"}
          </Button>
          <Button variant="outline" onClick={() => void refresh()} disabled={busy}>
            <RefreshCw className="h-4 w-4" />
            刷新
          </Button>
        </div>
      </div>
      <AssetTable
        headers={["邮箱", "密码", "OAuth", "注册", "占用", "操作"]}
        empty="暂无邮箱"
      >
        {rows.map((row) => (
          <tr key={value(row, "id")} className="border-t border-slate-100">
            <td className="px-3 py-3 font-mono text-xs text-slate-700">{value(row, "email")}</td>
            <td className="px-3 py-3"><Badge label={value(row, "password_label", "-")} tone={serverTone(row, "password_tone", "neutral")} /></td>
            <td className="px-3 py-3"><Badge label={value(row, "oauth_label", "-")} tone={serverTone(row, "oauth_tone", "neutral")} /></td>
            <td className="px-3 py-3 text-xs text-slate-500">{value(row, "registration_label", "-")}</td>
            <td className="px-3 py-3 text-xs text-slate-500">{value(row, "usage_label", "-")}</td>
            <td className="px-3 py-3">
              <div className="flex gap-1">
                <IconButton title="预览邮件" onClick={async () => {
                  try { setPreview((await previewPoolEmail(value(row, "id"))) as Row); }
                  catch (reason) { setError(reason instanceof Error ? reason.message : "邮件预览失败"); }
                }}><Eye className="h-4 w-4" /></IconButton>
                <IconButton title="删除邮箱" onClick={async () => {
                  if (!(await confirm("确定删除此邮箱吗？", "删除邮箱"))) return;
                  try { await deletePoolEmail(value(row, "id")); await refresh(); setNotice("邮箱已删除"); }
                  catch (reason) { setError(reason instanceof Error ? reason.message : "删除失败"); }
                }}><Trash2 className="h-4 w-4" /></IconButton>
              </div>
            </td>
          </tr>
        ))}
      </AssetTable>
      {preview ? <MailboxPreview preview={preview} close={() => setPreview(null)} /> : null}
    </div>,
  );
}

function MailboxPreview({ preview, close }: { preview: Row; close: () => void }) {
  const messages = Array.isArray(preview.messages) ? (preview.messages as Row[]) : [];
  return (
    <div className="rounded-md border border-cyan-100 bg-cyan-50/50 p-4">
      <div className="flex items-center justify-between gap-3">
        <div><p className="text-sm font-semibold text-slate-900">{value(preview, "email")}</p><p className="mt-1 text-xs text-slate-500">{messages.length} 封邮件</p></div>
        <IconButton title="关闭预览" onClick={close}><X className="h-4 w-4" /></IconButton>
      </div>
      <div className="mt-3 space-y-2">
        {messages.length ? messages.map((message, index) => <details key={`${value(message, "uid")}-${index}`} className="rounded-md border border-slate-200 bg-white p-3">
          <summary className="cursor-pointer text-sm font-medium text-slate-800">{value(message, "subject", "无主题")}</summary>
          <div className="mt-2 space-y-1 text-xs text-slate-500"><p>发件人：{value(message, "from", "-")}</p><p>时间：{value(message, "date", "-")}</p><p className="whitespace-pre-wrap break-words text-slate-700">{value(message, "text", "-")}</p></div>
        </details>) : <p className="text-sm text-slate-500">暂无邮件</p>}
      </div>
    </div>
  );
}

export function PhonePoolPanel({
  rows,
  setRows,
  setNotice,
  setError,
  confirm,
}: {
  rows: Row[];
  setRows: (rows: Row[]) => void;
  setNotice: (message: string) => void;
  setError: (message: string) => void;
  confirm: ConfirmAction;
}) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() { setRows(((await getPhones()).phones as Row[]) || []); }
  async function importRows() {
    setBusy(true);
    try { const result = await importPhones({ text: input }); await refresh(); setInput(""); setNotice(`手机号池已导入 ${value(result, "created", "0")} 条`); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "手机号导入失败"); }
    finally { setBusy(false); }
  }
  return assetPanel(
    "手机号池",
    "每行一个手机号-短信 API Key；保留原版的编辑、启用/停用和使用次数状态。",
    Smartphone,
    <div className="space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row"><textarea value={input} onChange={(event) => setInput(event.target.value)} rows={4} className={areaClass} placeholder="8613800000000-API_KEY" aria-label="手机号池导入内容" /><div className="flex gap-2 sm:flex-col"><Button onClick={() => void importRows()} disabled={busy}><Upload className="h-4 w-4" />导入</Button><Button variant="outline" onClick={() => void refresh()}><RefreshCw className="h-4 w-4" />刷新</Button></div></div>
      <AssetTable headers={["手机号", "API Key", "状态", "使用次数", "操作"]} empty="暂无手机号">
        {rows.map((row) => <PhoneRow key={value(row, "id")} row={row} refresh={refresh} setNotice={setNotice} setError={setError} confirm={confirm} />)}
      </AssetTable>
    </div>,
  );
}

function PhoneRow({ row, refresh, setNotice, setError, confirm }: { row: Row; refresh: () => Promise<void>; setNotice: (message: string) => void; setError: (message: string) => void; confirm: ConfirmAction }) {
  const [phone, setPhone] = useState(value(row, "phone"));
  const [key, setKey] = useState(value(row, "key"));
  const [status, setStatus] = useState(value(row, "status", "正常"));
  const [active, setActive] = useState(enabled(row));
  const [busy, setBusy] = useState(false);
  async function save() {
    setBusy(true);
    try { await updatePhone(value(row, "id"), { phone, key, status, active }); await refresh(); setNotice("手机号已保存"); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "保存失败"); }
    finally { setBusy(false); }
  }
  return <tr className="border-t border-slate-100">
    <td className="px-3 py-2"><input value={phone} onChange={(event) => setPhone(event.target.value)} className={inputClass} /></td>
    <td className="px-3 py-2"><input value={key} onChange={(event) => setKey(event.target.value)} className={inputClass} /></td>
    <td className="px-3 py-2"><select value={status} onChange={(event) => setStatus(event.target.value)} className={inputClass}>{(Array.isArray(row.status_options) ? row.status_options as Row[] : []).map((option) => <option key={value(option, "value")} value={value(option, "value")}>{value(option, "label", value(option, "value"))}</option>)}</select></td>
    <td className="px-3 py-2 text-center text-xs text-slate-500">{value(row, "usage_text", "0")}</td>
    <td className="px-3 py-2"><div className="flex items-center gap-1"><label className="flex h-8 items-center gap-1 px-1 text-xs text-slate-500"><input type="checkbox" checked={active} onChange={(event) => setActive(event.target.checked)} className="accent-cyan-500" />启用</label><IconButton title="保存手机号" disabled={busy} onClick={() => void save()}><Save className="h-4 w-4" /></IconButton><IconButton title="删除手机号" onClick={async () => { if (!(await confirm("确定删除此手机号吗？", "删除手机号"))) return; try { await deletePhone(value(row, "id")); await refresh(); setNotice("手机号已删除"); } catch (reason) { setError(reason instanceof Error ? reason.message : "删除失败"); } }}><Trash2 className="h-4 w-4" /></IconButton></div></td>
  </tr>;
}

export function ProductPoolPanel({
  rows,
  setRows,
  setNotice,
  setError,
  confirm,
}: {
  rows: Row[];
  setRows: (rows: Row[]) => void;
  setNotice: (message: string) => void;
  setError: (message: string) => void;
  confirm: ConfirmAction;
}) {
  const [input, setInput] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [generationCount, setGenerationCount] = useState("10");
  const [generation, setGeneration] = useState<Row | null>(null);
  const [generationBusy, setGenerationBusy] = useState(false);

  useEffect(() => {
    if (!generation) return;
    if (generation && (generation.terminal === true || generation.terminal === "true")) return;
    const timer = window.setInterval(() => {
      void getProductGeneration(value(generation, "jobKey"))
        .then((result) => setGeneration((result.task || result) as Row))
        .catch(() => undefined);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [generation]);

  async function startGeneration() {
    const count = Number(generationCount);
    setGenerationBusy(true);
    try {
      const result = await generateProducts(count);
      setGeneration((result.task || result) as Row);
      setNotice(value(result, "message", "成品生产任务已启动"));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "成品生产启动失败");
    } finally {
      setGenerationBusy(false);
    }
  }

  async function resumeGeneration() {
    setGenerationBusy(true);
    try {
      const result = await resumeProducts();
      setGeneration((result.task || result) as Row);
      setNotice(value(result, "message", "成品续作任务已启动"));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "成品续作失败");
    } finally {
      setGenerationBusy(false);
    }
  }

  async function stopGeneration() {
    try {
      const result = await stopProducts(value(generation || {}, "jobKey"));
      setNotice(value(result, "message", "已发送停止指令"));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "停止成品生产失败");
    }
  }
  async function refresh() { setRows(((await getProducts()).items as Row[]) || []); }
  async function importRows() {
    setBusy(true);
    try { const result = await importProducts(input); await refresh(); setInput(""); setNotice(`成品号库已导入 ${value(result, "created", "0")} 条`); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "成品导入失败"); }
    finally { setBusy(false); }
  }
  async function exportSelected() {
    if (!selected.length) return setError("请选择成品号");
    try { downloadBlob(await downloadProducts(selected), "products.json"); setSelected([]); await refresh(); setNotice("成品号已出库并下载"); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "成品出库失败"); }
  }
  return assetPanel(
    "成品号库",
    "导入已准备好的账号，管理正常/封禁状态，并按原版事务语义单个或批量出库。",
    PackageCheck,
    <div className="space-y-5">
      <div className="rounded-md border border-cyan-100 bg-cyan-50/60 p-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p className="text-sm font-semibold text-slate-900">原版成品生产</p>
            <p className="mt-1 text-xs leading-5 text-slate-500">使用原版注册、支付和 OAuth 协议提取流程，成功后自动写入成品号库。</p>
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <label className="text-xs font-semibold text-slate-600">数量<input type="number" min={1} max={100} value={generationCount} onChange={(event) => setGenerationCount(event.target.value)} className="mt-1 h-10 w-24 rounded-md border border-slate-200 bg-white px-3 text-sm text-slate-900" /></label>
            <Button onClick={() => void startGeneration()} disabled={generationBusy}><Play className="h-4 w-4" />开始生产</Button>
            <Button variant="outline" onClick={() => void resumeGeneration()} disabled={generationBusy}><RotateCcw className="h-4 w-4" />继续中断任务</Button>
            <Button variant="ghost" onClick={() => void stopGeneration()} disabled={!generation || !(generation.stop_enabled === true || generation.stop_enabled === "true")}><Square className="h-4 w-4" />停止</Button>
          </div>
        </div>
        {generation ? <div className="mt-4 rounded-md border border-cyan-100 bg-white p-3"><div className="flex flex-wrap items-center justify-between gap-2 text-xs"><span className="font-mono text-slate-500">{value(generation, "jobKey")}</span><Badge label={value(generation, "status_label", value(generation, "status"))} tone={serverTone(generation, "status_tone", "info")} /></div><div className="mt-3 flex items-center justify-between text-xs text-slate-500"><span>{value(generation, "message", "等待 Worker")}</span><span>{value(generation, "progress_text", "0%")}</span></div><div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-100"><div className="h-full rounded-full bg-cyan-500 transition-all" style={{ width: `${value(generation, "progress", "0")}%` }} /></div><p className="mt-2 text-xs text-slate-500">成功 {value(generation, "successCount", "0")} · 已完成 {value(generation, "completedCount", "0")} / {value(generation, "targetCount", "0")} · 失败 {value(generation, "failedCount", "0")}</p></div> : null}
      </div>
      <div className="flex flex-col gap-3 lg:flex-row"><textarea value={input} onChange={(event) => setInput(event.target.value)} rows={4} className={areaClass} placeholder="email----password----token----imap_key----file_path" aria-label="成品号导入内容" /><div className="flex gap-2 lg:flex-col"><Button onClick={() => void importRows()} disabled={busy}><Upload className="h-4 w-4" />导入</Button><Button variant="outline" onClick={() => void refresh()}><RefreshCw className="h-4 w-4" />刷新</Button><Button variant="dark" onClick={() => void exportSelected()}><Download className="h-4 w-4" />批量出库</Button></div></div>
      <AssetTable headers={["选择", "邮箱", "IMAP Key", "状态", "已出库", "操作"]} empty="暂无成品号">
        {rows.map((row) => { const id = value(row, "id"); const isSelected = selected.includes(id); return <tr key={id} className="border-t border-slate-100">
          <td className="px-3 py-3"><input type="checkbox" checked={isSelected} onChange={(event) => setSelected(event.target.checked ? [...selected, id] : selected.filter((item) => item !== id))} className="accent-cyan-500" /></td>
          <td className="px-3 py-3 font-mono text-xs text-slate-700">{value(row, "email")}</td>
          <td className="px-3 py-3 font-mono text-xs text-slate-500">{value(row, "imap_key", "-")}</td>
          <td className="px-3 py-3"><button type="button" className="text-left" onClick={async () => { try { await updateProductStatus(id); await refresh(); setNotice("成品状态已更新"); } catch (reason) { setError(reason instanceof Error ? reason.message : "状态更新失败"); } }}><Badge label={value(row, "status_label", value(row, "status", "正常"))} tone={serverTone(row, "status_tone", "neutral")} /></button></td>
          <td className="px-3 py-3 text-xs text-slate-500">{value(row, "shipped_label", "-")}</td>
          <td className="px-3 py-3"><div className="flex gap-1"><IconButton title="单个出库" onClick={async () => { try { downloadBlob(await downloadProduct(id), `${value(row, "email", "product")}.json`); await refresh(); setNotice("成品已出库"); } catch (reason) { setError(reason instanceof Error ? reason.message : "出库失败"); } }}><Download className="h-4 w-4" /></IconButton><IconButton title="删除成品" onClick={async () => { if (!(await confirm("确定删除此成品号吗？", "删除成品号"))) return; try { await deleteProduct(id); await refresh(); setNotice("成品已删除"); } catch (reason) { setError(reason instanceof Error ? reason.message : "删除失败"); } }}><Trash2 className="h-4 w-4" /></IconButton></div></td>
        </tr>; })}
      </AssetTable>
    </div>,
  );
}

function IconButton({ children, title, onClick, disabled = false }: { children: ReactNode; title: string; onClick: () => void; disabled?: boolean }) {
  return <button type="button" title={title} disabled={disabled} onClick={onClick} className="inline-flex h-8 w-8 items-center justify-center rounded-md text-slate-400 transition hover:bg-cyan-50 hover:text-cyan-700 disabled:opacity-50">{children}</button>;
}

function AssetTable({ headers, empty, children }: { headers: string[]; empty: string; children: ReactNode }) {
  const hasRows = Children.count(children) > 0;
  return <div className="overflow-x-auto rounded-md border border-slate-200"><table className="w-full min-w-[760px] text-left text-sm"><thead className="bg-slate-50 text-xs font-semibold text-slate-500"><tr>{headers.map((header) => <th key={header} className="px-3 py-3">{header}</th>)}</tr></thead><tbody>{children}</tbody></table>{!hasRows ? <div className="px-3 py-8 text-center text-sm text-slate-400">{empty}</div> : null}</div>;
}
