import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const chatDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// `tauri build`（下面）打包时靠 `bundle.resources` 从磁盘拷文件，不是靠某个
// env var 现凑——所以随包的 codex 二进制必须在这一步之前就已经落在
// `src-tauri/vendor/codex/`。取不到就让整个构建在这里失败，而不是等
// bundler 那步报一个"resource not found"、看起来毫不相关的错误。
const vendorResult = spawnSync(
  process.execPath,
  [path.join(chatDir, "scripts", "fetch-codex-vendor.mjs")],
  { cwd: chatDir, stdio: "inherit" },
);
if (vendorResult.error || vendorResult.status !== 0) {
  console.error("准备随包 codex 二进制失败，构建中止。");
  process.exit(vendorResult.status ?? 1);
}

const serviceUrl = process.env.PAW_SERVICE_URL?.trim() ?? "";
if (!serviceUrl) {
  console.error("PAW_SERVICE_URL is required for a Chat desktop build.");
  process.exit(1);
}

let parsedUrl;
try {
  parsedUrl = new URL(serviceUrl);
} catch {
  console.error("PAW_SERVICE_URL must be an absolute http(s) URL.");
  process.exit(1);
}

if (parsedUrl.protocol !== "http:" && parsedUrl.protocol !== "https:") {
  console.error("PAW_SERVICE_URL must use http:// or https://.");
  process.exit(1);
}

// Node >= 20.12 refuses to spawn .cmd/.bat without an explicit shell (CVE-2024-27980),
// so npx must go through the shell on Windows.
const command = process.platform === "win32" ? "npx.cmd" : "npx";
const result = spawnSync(command, ["tauri", "build"], {
  shell: process.platform === "win32",
  env: {
    ...process.env,
    BUILD_APP: "1",
    BUILD_MODE: "export",
    PAW_MOUNT_PATH: "",
    PAW_SERVICE_URL: serviceUrl.replace(/\/+$/, ""),
    PAW_STATIC_EXPORT: "1",
  },
  stdio: "inherit",
});

if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}

process.exit(result.status ?? 1);
