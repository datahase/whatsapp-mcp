import type { NextConfig } from "next";
const config: NextConfig = {
  output: "export",
  basePath: "/admin",
  images: { unoptimized: true },
  poweredByHeader: false,
};
export default config;
