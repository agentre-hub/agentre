// 把 src/ 下的非 TS 资源原样搬进 dist/。
//
// 这个包的构建是一句 `tsc`，而 tsc 只管 TS —— png / svg 这些被 import 的资源它一个
// 都不搬。少了它们，`dist/**/*.js` 里的 `import x from "./assets/…"` 就指向一个不
// 存在的文件：本仓测试读的是 src/，一切正常；一旦发布成 tarball 被宿主消费，宿主
// 的打包器会在 import 分析阶段炸掉，而且报的是宿主的错。
//
// 判据是「不是 .ts/.tsx 就搬」而不是列一串扩展名：新加一种资源（webp、woff2…）
// 不该需要再改这里一次才发得出去。locale 的 .json 靠 resolveJsonModule 已经会被
// tsc 发出来，重复拷贝一次是幂等的，不额外分情况。

import { cpSync, mkdirSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/** srcDir 下所有非 TS 文件，返回相对 srcDir 的路径。 */
export function assetFiles(srcDir) {
  const walk = (dir) => {
    const out = [];
    for (const entry of readdirSync(dir)) {
      const full = path.join(dir, entry);
      if (statSync(full).isDirectory()) {
        out.push(...walk(full));
        continue;
      }
      if (/\.(ts|tsx)$/.test(entry)) continue;
      out.push(path.relative(srcDir, full));
    }
    return out;
  };
  return walk(srcDir).sort();
}

export function copyAssets(srcDir, outDir) {
  const files = assetFiles(srcDir);
  for (const rel of files) {
    const to = path.join(outDir, rel);
    mkdirSync(path.dirname(to), { recursive: true });
    cpSync(path.join(srcDir, rel), to);
  }
  return files;
}

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
// 不打日志：这个包的 eslint 按浏览器环境判文件，`console` 在这里算未定义标识符。
// 拷贝失败会以抛异常 + 非零退出码报出来，构建照样会停，不缺这一行回执。
copyAssets(path.join(packageRoot, "src"), path.join(packageRoot, "dist"));
