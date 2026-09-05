/**
 * 构建前清空产物目录。
 *
 * 背景：本机 E: 盘上 Node 的 fs.rmSync({ recursive: true }) 会让进程直接硬崩溃
 * （0xC0000409 STATUS_STACK_BUFFER_OVERRUN，Git Bash 下表现为 EXIT=127，无任何报错）。
 * 同样的调用在 C: 盘正常，与路径含中文无关——换成纯 ASCII 的 E:\_test 一样崩。
 *
 * vite 的 emptyOutDir 内部正是走 fs.rmSync，所以只要 build.outDir
 * （../backend/internal/web/dist）在构建开始时已存在，整个 vite build 就会
 * 停在 "modules transformed" 之后、"rendering chunks" 之前静默死掉；
 * 目录不存在时构建完全正常。
 *
 * 逐层 readdir + unlink + rmdir 走的是另一条系统调用路径，在 E: 盘可用，
 * 因此这里自己删，删完 emptyOutDir 就成了空操作。
 */
import { existsSync, readdirSync, rmdirSync, unlinkSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'

function removeRecursive(target) {
  for (const entry of readdirSync(target, { withFileTypes: true })) {
    const child = join(target, entry.name)
    if (entry.isDirectory()) {
      removeRecursive(child)
    } else {
      unlinkSync(child)
    }
  }
  rmdirSync(target)
}

const outDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../backend/internal/web/dist')

if (existsSync(outDir)) {
  removeRecursive(outDir)
  console.log(`[clean-out-dir] removed ${outDir}`)
} else {
  console.log(`[clean-out-dir] nothing to remove at ${outDir}`)
}
