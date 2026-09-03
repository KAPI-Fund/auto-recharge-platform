import { ImapFlow } from "imapflow";
import { simpleParser } from "mailparser";

const DEFAULT_IMAP_HOST = "outlook.office365.com";
const DEFAULT_IMAP_PORT = 993;
const DEFAULT_LIMIT = 50;
const MAX_LIMIT = 100;

function numberValue(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function textValue(value) {
  return String(value || "").trim();
}

function messageAddress(value) {
  if (!value) return "";
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    return value
      .map((item) => item?.address || item?.name || "")
      .filter(Boolean)
      .join(", ");
  }
  return value.text || value.address || value.name || "";
}

function stripHtml(value) {
  return String(value || "")
    .replace(/<style[\s\S]*?<\/style>/gi, " ")
    .replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

async function refreshAccessToken({ clientId, refreshToken }) {
  if (!clientId || !refreshToken) return "";
  const body = new URLSearchParams({
    client_id: clientId,
    grant_type: "refresh_token",
    refresh_token: refreshToken,
    scope: "offline_access https://outlook.office.com/IMAP.AccessAsUser.All",
  });
  const response = await fetch(
    "https://login.microsoftonline.com/common/oauth2/v2.0/token",
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body,
    },
  );
  const payload = await response.json().catch(() => ({}));
  if (!response.ok || !payload.access_token) {
    throw new Error(payload.error_description || "邮箱 OAuth 刷新令牌失败");
  }
  return String(payload.access_token);
}

async function resolveAuth(input) {
  const email = textValue(input.email);
  const password = textValue(input.password);
  const clientId = textValue(input.clientId);
  const refreshToken = textValue(input.refreshToken);
  if (refreshToken) {
    const accessToken = await refreshAccessToken({ clientId, refreshToken }).catch(
      () => "",
    );
    if (accessToken) return { user: email, accessToken };
    if (!clientId) return { user: email, accessToken: refreshToken };
  }
  if (!email || !password) {
    throw new Error("邮箱账号缺少密码或 OAuth 凭据");
  }
  return { user: email, pass: password };
}

async function listMailboxNames(client, includeJunk) {
  const names = ["INBOX"];
  if (!includeJunk) return names;
  const boxes = await client.list().catch(() => []);
  for (const box of boxes) {
    const path = textValue(box.path);
    const specialUse = textValue(box.specialUse).toLowerCase();
    if (
      path &&
      (specialUse.includes("junk") ||
        specialUse.includes("spam") ||
        /junk|spam|垃圾邮件/i.test(path))
    ) {
      names.push(path);
    }
  }
  return [...new Set(names)];
}

async function readMailbox(client, mailbox, limit) {
  const messages = [];
  let lock;
  try {
    lock = await client.getMailboxLock(mailbox);
    const uids = await client.search({ all: true }, { uid: true });
    for (const uid of uids.slice(-limit).reverse()) {
      const message = await client.fetchOne(
        uid,
        { uid: true, envelope: true, source: true, internalDate: true },
        { uid: true },
      );
      const parsed = await simpleParser(message.source);
      const html = textValue(parsed.html);
      messages.push({
        mailbox,
        uid,
        date: parsed.date || message.internalDate || message.envelope?.date || null,
        from: messageAddress(parsed.from) || messageAddress(message.envelope?.from),
        to: messageAddress(parsed.to) || messageAddress(message.envelope?.to),
        subject: textValue(parsed.subject) || textValue(message.envelope?.subject),
        text: textValue(parsed.text) || stripHtml(html),
        hasAttachments: Array.isArray(parsed.attachments) && parsed.attachments.length > 0,
      });
    }
  } finally {
    lock?.release();
  }
  return messages;
}

export async function previewMailbox(input = {}) {
  const email = textValue(input.email);
  if (!email || !email.includes("@")) throw new Error("邮箱地址无效");
  const limit = Math.min(MAX_LIMIT, numberValue(input.limit, DEFAULT_LIMIT));
  const includeJunk = input.includeJunk === true || input.includeJunk === "1";
  const host = textValue(input.imapHost) || DEFAULT_IMAP_HOST;
  const port = numberValue(input.imapPort, DEFAULT_IMAP_PORT);
  const auth = await resolveAuth(input);
  const client = new ImapFlow({
    host,
    port,
    secure: true,
    auth,
    logger: false,
    tls: { rejectUnauthorized: input.rejectUnauthorized !== false },
  });
  await client.connect();
  try {
    const names = await listMailboxNames(client, includeJunk);
    const result = [];
    for (const name of names) {
      try {
        result.push(...(await readMailbox(client, name, limit)));
      } catch (error) {
        if (name === "INBOX") throw error;
      }
    }
    result.sort((left, right) => {
      const leftTime = new Date(left.date || 0).getTime();
      const rightTime = new Date(right.date || 0).getTime();
      return rightTime - leftTime;
    });
    return { success: true, email, host, count: result.length, messages: result.slice(0, limit) };
  } finally {
    await client.logout().catch(() => client.close());
  }
}
