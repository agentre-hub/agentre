import {
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { describe, expect, it } from "vitest";

/**
 * 发布产物必须带上它自己 import 的资源。
 *
 * 这个包的构建就是一句 `tsc`，而 **tsc 只处理 TS，不搬非 TS 文件**。于是
 * `dist/engine/ai-brand-logo.js` 里那句 `import agentreLogo from
 * "./assets/images/logo-mark.png"` 指向一个不存在的文件 —— 包在本仓测试里一切正常
 * （测试读的是 `src/`），一发布成 tarball，任何宿主一加载引擎面板就在
 * `vite:import-analysis` 上炸掉，而且报的是宿主的错，排查要绕一圈才回到这里。
 *
 * 两条守卫分别守两种失败：漏文件（拷贝清单太窄）、漏接线（脚本没进 build/prepare）。
 */
const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

/** src/ 下所有非 TS 文件的相对路径 —— 也就是 tsc 一个都不会搬的那些。 */
function assetsUnder(dir: string, base = dir): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...assetsUnder(full, base));
      continue;
    }
    if (/\.(ts|tsx)$/.test(entry)) continue;
    out.push(path.relative(base, full));
  }
  return out.sort();
}

describe("发布产物带得上自己的资源", () => {
  it("拷贝脚本报出的清单，覆盖 src 下每一个非 TS 文件", async () => {
    // 运行时拼路径：写成静态说明符的话，脚本还不存在时 vite 会在 transform 阶段就
    // 报错，整个文件一条都跑不起来，另外两条守卫也就看不到自己的红。
    // 必须经 pathToFileURL —— 这个包的测试环境是 happy-dom，`import.meta.url` 在
    // 那里是 http:，直接拿它做基准 ESM loader 会拒绝。
    const scriptUrl = pathToFileURL(
      path.join(packageRoot, "scripts", "copy-assets.mjs"),
    ).href;
    const { assetFiles } = (await import(/* @vite-ignore */ scriptUrl)) as {
      assetFiles: (srcDir: string) => string[];
    };
    const srcDir = path.join(packageRoot, "src");

    expect(assetFiles(srcDir).sort()).toEqual(assetsUnder(srcDir));
  });

  it("build 与 prepare 都接上了拷贝脚本（只写脚本不接线等于没修）", () => {
    const pkg = JSON.parse(
      readFileSync(path.join(packageRoot, "package.json"), "utf8"),
    ) as { scripts: Record<string, string> };

    expect(pkg.scripts.build).toContain("copy-assets");
    expect(pkg.scripts.prepare).toContain("copy-assets");
  });

  it("拷贝真的把文件写到了输出目录（含嵌套目录）", async () => {
    // 验脚本本身而不是 `pnpm run build`：后者要跑整套 tsc，实测 17s、会顶穿默认
    // 超时，而它多验到的只有「&& 有没有断」—— 那一条上面那条守卫已经盯着了。
    const scriptUrl = pathToFileURL(
      path.join(packageRoot, "scripts", "copy-assets.mjs"),
    ).href;
    const { copyAssets } = (await import(/* @vite-ignore */ scriptUrl)) as {
      copyAssets: (srcDir: string, outDir: string) => string[];
    };

    const srcDir = path.join(packageRoot, "src");
    const outDir = mkdtempSync(path.join(tmpdir(), "agentre-ui-assets-"));
    try {
      copyAssets(srcDir, outDir);
      const missing = assetsUnder(srcDir).filter((rel) => {
        try {
          return !statSync(path.join(outDir, rel)).isFile();
        } catch {
          return true;
        }
      });
      expect(missing).toEqual([]);
    } finally {
      rmSync(outDir, { recursive: true, force: true });
    }
  });
});
