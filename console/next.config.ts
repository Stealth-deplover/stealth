import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  reactStrictMode: true,
  // Playwright's local dev server uses 127.0.0.1; allow its HMR/client
  // resources so the critical flow exercises hydrated React behavior.
  allowedDevOrigins: ["127.0.0.1", "localhost"],
};

export default nextConfig;
