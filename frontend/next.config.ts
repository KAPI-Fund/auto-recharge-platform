import type { NextConfig } from "next";

const apiInternalUrl = (process.env.API_INTERNAL_URL || "http://127.0.0.1:8080").replace(/\/$/, "");

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: process.cwd(),
  async rewrites() {
    return [
      {
        source: "/platform-api/:path*",
        destination: `${apiInternalUrl}/api/v1/:path*`,
      },
      {
        source: "/legacy-api/:path*",
        destination: `${apiInternalUrl}/api/:path*`,
      },
      {
        source: "/api/:path*",
        destination: `${apiInternalUrl}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
