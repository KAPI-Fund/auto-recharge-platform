import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const apiInternalUrl = (process.env.API_INTERNAL_URL || "http://127.0.0.1:8080").replace(/\/$/, "");

function cleanPath(value: unknown, fallback: string) {
  const path = String(value || fallback).trim().replace(/^\/+|\/+$/g, "");
  return path || fallback.replace(/^\/+/, "");
}

function notFound() {
  return new NextResponse("Not Found", {
    status: 404,
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
}

export default async function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname.replace(/\/+$/, "") || "/";
  if (pathname.startsWith("/legacy-pages") || pathname.endsWith(".html")) {
    return notFound();
  }
  if (
    pathname === "/" ||
    pathname.startsWith("/_next") ||
    pathname.startsWith("/legacy-api") ||
    pathname.startsWith("/platform-api") ||
    pathname.startsWith("/api") ||
    pathname.includes(".")
  ) {
    return NextResponse.next();
  }
  const isCanonicalAdminPath =
    pathname === "/admin" || pathname.startsWith("/admin/") || pathname === "/admin-login";

  try {
    const response = await fetch(`${apiInternalUrl}/api/public/admin-paths`, { cache: "no-store" });
    if (!response.ok) return NextResponse.next();
    const paths = await response.json();
    const loginPath = `/${cleanPath(paths.loginPath, "admin-login")}`;
    const panelPath = `/${cleanPath(paths.panelPath, "admin")}`;
    if (isCanonicalAdminPath && pathname !== loginPath && pathname !== panelPath && !pathname.startsWith(`${panelPath}/`)) {
      return notFound();
    }
    const destination = pathname === loginPath
      ? "/admin-login"
      : pathname === panelPath || pathname.startsWith(`${panelPath}/`)
        ? pathname.replace(panelPath, "/admin") || "/admin"
        : "";
    if (!destination || destination === pathname) return NextResponse.next();
    return NextResponse.rewrite(new URL(destination, request.url));
  } catch {
    return NextResponse.next();
  }
}

export const config = {
  matcher: ["/:path*"],
};
