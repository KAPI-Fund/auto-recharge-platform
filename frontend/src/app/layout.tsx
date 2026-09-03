import type { Metadata } from "next";
import "./globals.css";
import "../../public/storefront.css";

export const metadata: Metadata = {
  title: {
    default: "KC GPT自动充值系统",
    template: "%s",
  },
  description: "KC GPT自动充值系统",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="zh-CN"><body>{children}</body></html>;
}
