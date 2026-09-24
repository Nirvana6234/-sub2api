import { cp, mkdir, readFile, rm } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(scriptDir, "../../..");
const sourceDir = path.resolve(projectRoot, "tools/chat/out");
const targetDir = path.resolve(projectRoot, "backend/internal/web/paw_dist");

if (!targetDir.startsWith(projectRoot + path.sep)) {
  throw new Error(`Refusing to write outside the project root: ${targetDir}`);
}

await readFile(path.join(sourceDir, "index.html"));
await rm(targetDir, { recursive: true, force: true });
await mkdir(targetDir, { recursive: true });
await cp(sourceDir, targetDir, { recursive: true });

console.log(`Staged Paw static export at ${targetDir}`);
