import { rm } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(scriptDir, "../..");
const outputDir = path.resolve(projectRoot, "backend/internal/web/dist");

if (!outputDir.startsWith(projectRoot + path.sep)) {
  throw new Error(`Refusing to clean outside the project root: ${outputDir}`);
}

await rm(outputDir, { recursive: true, force: true });
console.log(`Cleaned frontend output directory: ${outputDir}`);
