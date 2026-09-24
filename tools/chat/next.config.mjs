/** @type {import('next').NextConfig} */
const staticExport = process.env.PAW_STATIC_EXPORT === "1";
const mountPath = (process.env.PAW_MOUNT_PATH ?? "")
  .trim()
  .replace(/^\/+|\/+$/g, "");
const distDir =
  process.env.NEXT_DIST_DIR ??
  (process.env.NODE_ENV === "development" ? ".next-dev" : ".next");

const nextConfig = {
  reactStrictMode: true,
  distDir,
  output: staticExport ? "export" : undefined,
  basePath: mountPath ? `/${mountPath}` : undefined,
  images: {
    unoptimized: true,
  },
};

export default nextConfig;
