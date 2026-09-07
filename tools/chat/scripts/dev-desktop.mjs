// `npm run app:dev` 的入口——只做一件事：找到随包 codex 二进制，把它塞进
// `COFLY_CODEX_BINARY`，然后照常起 `tauri dev`。
//
// 为什么不是直接在 package.json 里写 `tauri dev`：`COFLY_CODEX_BINARY` 这个
// 环境变量本来就是给"开发和端到端用"（见 src-tauri/src/lib.rs 的
// `resolve_agent_paths` 注释），但每次开发都要手动记得设置、还要记得二进制在
// `src-tauri/vendor/codex/bin/` 底下——这条本该是"敲一条命令就好"，不该是
// 一条需要记住的隐藏步骤。已经显式设置过 `COFLY_CODEX_BINARY` 的话尊重它，
// 方便临时指到别的二进制做验证。
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const chatDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const vendoredBinary = path.join(
  chatDir,
  "src-tauri",
  "vendor",
  "codex",
  "bin",
  process.platform === "win32" ? "codex-app-server.exe" : "codex-app-server",
);

const env = { ...process.env };
if (!env.COFLY_CODEX_BINARY) {
  if (existsSync(vendoredBinary)) {
    env.COFLY_CODEX_BINARY = vendoredBinary;
  } else {
    console.warn(
      `[app:dev] 没找到随包 codex 二进制（${vendoredBinary}）——agent 面起不来，` +
        "Chat 本身仍然能用。先跑一次 `npm run codex:vendor` 取回它。",
    );
  }
}

// Node >= 20.12 之后不允许直接 spawn .cmd/.bat（CVE-2024-27980），
// Windows 上 npx 得走 shell —— 跟 build-tauri.mjs 是同一个坑。
const command = process.platform === "win32" ? "npx.cmd" : "npx";
const result = spawnSync(command, ["tauri", "dev"], {
  shell: process.platform === "win32",
  cwd: chatDir,
  env,
  stdio: "inherit",
});

if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
