// 把随包发的 codex-app-server 取到本地 `src-tauri/vendor/codex/`。
//
// 这份二进制**不进 git**（229MB，见 src-tauri/.gitignore 里 `/vendor/**/*.exe`
// 那条），所以任何一台新机器——本地新克隆，或者 CI 的干净 runner——第一次
// 跑桌面端之前都得先补出这个目录。真值来源是
// `src-tauri/crates/codex-host/tests/fixtures/bundled-codex.json`：来源 URL、
// 上游 tag、每个文件的 sha256——这份清单是上次手工下载校验时写的，这里只是把
// 那个手工过程变成可以重放的脚本，不改判断依据。
//
// 幂等：本地文件齐、sha256 对得上就直接跳过，不重新下载。
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const chatDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const tauriDir = path.join(chatDir, "src-tauri");
const manifestPath = path.join(
  tauriDir,
  "crates/codex-host/tests/fixtures/bundled-codex.json",
);
const vendorDir = path.join(tauriDir, "vendor", "codex");

const manifest = JSON.parse(readFileSync(manifestPath, "utf-8"));

// `bundled-codex.json` 为了记清楚"上游发布过什么"，连我们不随包发的文件
// （`bin/codex-code-mode-host.exe`，`CodeModeHostTransport::Local` 不 spawn 它，
// 见开发计划 A10）也留了 sha256 做身份存档。取回来之后要把它删掉——不是漏取，
// 是刻意不留，省 69MB。这里只校验我们真正要用的那几个文件。
const unusedFiles = new Set(["bin/codex-code-mode-host.exe"]);
const neededFiles = Object.keys(manifest.files).filter((f) => !unusedFiles.has(f));

function sha256(filePath) {
  return createHash("sha256").update(readFileSync(filePath)).digest("hex");
}

function verifyAll() {
  return neededFiles.every((rel) => {
    const full = path.join(vendorDir, ...rel.split("/"));
    return existsSync(full) && sha256(full) === manifest.files[rel].sha256;
  });
}

if (verifyAll()) {
  console.log(`[fetch-codex] vendor/codex 已是 ${manifest.tag}，校验通过，跳过下载。`);
  process.exit(0);
}

console.log(`[fetch-codex] 下载 ${manifest.source}`);
rmSync(vendorDir, { recursive: true, force: true });
mkdirSync(vendorDir, { recursive: true });

const archive = path.join(tauriDir, "vendor", "codex-app-server-package.tar.gz");
mkdirSync(path.dirname(archive), { recursive: true });

// curl 与 tar 在 Windows（10 1803+/GitHub Actions windows-latest）和常见 Linux/
// macOS 发行版里都是自带命令，不额外引入依赖。curl 遵守标准 HTTP(S)_PROXY
// 环境变量，本地要走代理的话在外层 shell 设好就行，脚本不用管。
let result = spawnSync("curl", ["-fL", "--retry", "3", "-o", archive, manifest.source], {
  stdio: "inherit",
});
if (result.error || result.status !== 0) {
  console.error(
    "[fetch-codex] 下载失败——检查网络能不能到 GitHub Releases，" +
      "或者本地是否需要先配好代理。",
  );
  process.exit(1);
}

result = spawnSync("tar", ["-xzf", archive, "-C", vendorDir], { stdio: "inherit" });
rmSync(archive, { force: true });
if (result.error || result.status !== 0) {
  console.error("[fetch-codex] 解包失败。");
  process.exit(1);
}

for (const rel of unusedFiles) {
  rmSync(path.join(vendorDir, ...rel.split("/")), { force: true });
}

if (!verifyAll()) {
  console.error(
    "[fetch-codex] 解包后的文件跟 bundled-codex.json 记的 sha256 对不上——" +
      "要么上游那个 tag 的发布内容变了，要么这份清单本该重新生成。先别用，" +
      "查清楚再继续（别改脚本悄悄放过这个检查）。",
  );
  process.exit(1);
}

console.log(`[fetch-codex] vendor/codex 就绪：${manifest.tag}`);
