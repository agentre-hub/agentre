// 品牌组 logo 的确定性渲染：把 src/engine/assets/brands/ 下的 svg/png 逐个渲成
// 560px 透明 png，交给 stk.sh 加贴纸外框。跑法与参数见同目录 README.md。
//
// 本包的 eslint 把脚本按浏览器环境判（见 scripts/copy-assets.mjs），没有 `process`
// 这个全局，也没有 `console` —— 所以 Node 的东西一律从 globalThis 取，回执走 stdout。
// 图片用 file:// 页面 goto 渲染：setContent 载不了本地的 file:// 图。

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const nodeProcess = globalThis.process;
const scriptDir = path.dirname(fileURLToPath(import.meta.url));

// logo 目录跟着脚本走：<包根>/src/engine/assets/brands，换台机器照样能跑。
const defaultBrandsDir = path.resolve(
  scriptDir,
  "../../src/engine/assets/brands",
);
const names = [
  "claude.svg",
  "anthropic.svg",
  "openai.svg",
  "codex.svg",
  "pi.svg",
  "openclaw.svg",
  "hermes.png",
  "deepseek.svg",
  "glm.svg",
  "gemini.svg",
  "qwen.svg",
  "kimi.svg",
  "minimax.svg",
  "mistral.svg",
  "xai.svg",
  "meta.svg",
];

const usage = `用法：node raster.mjs --playwright <path> [--out <dir>] [--brands <dir>]

把品牌 logo 渲成 560px 透明 png（r-<name>.png），需要能 import 到 @playwright/test。

  --playwright <path>  @playwright/test 的 ESM 入口，通常是
                       <仓库>/e2e/node_modules/@playwright/test/index.mjs
                       也可用环境变量 PLAYWRIGHT_MODULE 给
  --out <dir>          r-<name>.png 的输出目录
                       默认当前目录，也可用环境变量 RASTER_OUT_DIR
  --brands <dir>       logo 目录，默认本包的 src/engine/assets/brands
  -h, --help           显示这段说明

相对路径按当前工作目录解析。例（在输出目录里跑，png 落在当前目录）：
  node <仓库>/frontend/packages/agentre-ui/scripts/preset-avatars/raster.mjs \\
      --playwright <仓库>/e2e/node_modules/@playwright/test/index.mjs`;

function fail(message) {
  nodeProcess.stderr.write(`${message}\n\n${usage}\n`);
  nodeProcess.exit(1);
}

function parseArgs(args) {
  const options = {
    playwright: nodeProcess.env.PLAYWRIGHT_MODULE ?? "",
    brands: defaultBrandsDir,
    out: nodeProcess.env.RASTER_OUT_DIR ?? ".",
  };
  const flags = {
    "--playwright": "playwright",
    "--out": "out",
    "--brands": "brands",
  };
  for (let i = 0; i < args.length; i += 1) {
    const flag = args[i];
    if (flag === "-h" || flag === "--help") {
      nodeProcess.stdout.write(`${usage}\n`);
      nodeProcess.exit(0);
    }
    const key = flags[flag];
    if (!key) fail(`未知参数：${flag}`);
    const value = args[i + 1];
    if (!value || value.startsWith("--")) fail(`参数 ${flag} 缺少取值`);
    options[key] = value;
    i += 1;
  }
  return options;
}

const options = parseArgs(nodeProcess.argv.slice(2));
if (!options.playwright) {
  fail("缺少 --playwright（@playwright/test 的 ESM 入口路径）");
}

const brandsDir = path.resolve(options.brands);
const outDir = path.resolve(options.out);
mkdirSync(outDir, { recursive: true });

const { chromium } = await import(
  pathToFileURL(path.resolve(options.playwright)).href
);

const browser = await chromium.launch();
let scratch;
try {
  scratch = mkdtempSync(path.join(outDir, ".raster-"));
  const pagePath = path.join(scratch, "page.html");
  const pageUrl = pathToFileURL(pagePath).href;
  const page = await browser.newPage({ viewport: { width: 600, height: 600 } });
  for (const name of names) {
    const logoUrl = pathToFileURL(path.join(brandsDir, name)).href;
    writeFileSync(
      pagePath,
      `<html><body style="margin:0;background:transparent"><img id="i" src="${logoUrl}" style="width:560px;height:560px;object-fit:contain;margin:20px"></body></html>`,
    );
    await page.goto(pageUrl);
    await page.locator("#i").evaluate((image) => image.decode());
    await page.locator("#i").screenshot({
      path: path.join(outDir, `r-${name.replace(/\.(svg|png)$/, "")}.png`),
      omitBackground: true,
    });
  }
} finally {
  try {
    await browser.close();
  } finally {
    if (scratch) rmSync(scratch, { recursive: true, force: true });
  }
}
nodeProcess.stdout.write(`ok: ${names.length} 张 → ${outDir}\n`);
