/** @type {import('next').NextConfig} */
const staticExport = process.env.PAW_STATIC_EXPORT === "1";
const mountPath = (process.env.PAW_MOUNT_PATH ?? "")
  .trim()
  .replace(/^\/+|\/+$/g, "");

const nextConfig = {
  reactStrictMode: true,
  output: staticExport ? "export" : undefined,
  basePath: mountPath ? `/${mountPath}` : undefined,
  images: {
    unoptimized: true,
  },
};

export default nextConfig;
