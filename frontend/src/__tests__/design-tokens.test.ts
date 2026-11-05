import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// 对话流排版依赖这几个 token。它们是 token 层与所有 transcript 组件之间的契约,
// 改名/删除会让 max-w-measure、text-prose 等工具类静默失效(Tailwind 不会报错,
// 只是不生成这个类),所以在这里锁死。
//
// token 已抽到共享包(agentre-server 与桌面端共用同一份),因此这里直接读包内的
// tokens.css——读 globals.css 只会读到一行 @import,锁不住任何东西。
const TOKENS_CSS = path.resolve(
  __dirname,
  "../../packages/agentre-ui/styles/tokens.css",
);

const REQUIRED_TOKENS: [string, string][] = [
  ["--text-3xs", "0.625rem"],
  ["--text-3xs--line-height", "0.875rem"],
  ["--text-prose", "0.9375rem"],
  ["--text-prose--line-height", "1.7"],
  ["--text-aux", "0.8125rem"],
  ["--text-aux--line-height", "1.65"],
  ["--text-meta", "0.75rem"],
  ["--text-meta--line-height", "1.25rem"],
  ["--container-measure", "none"],
];

// 热力色阶 heat-0…heat-4：本轮从 agentre-server 提升为共享 token（spec
// 2026-09-01「色阶的归属变更」/ 决策 10）。两个消费者——server 的活跃热力图与
// composer 思考力度弹层的强度圆点——从此读同一份值，所以深浅两套值与 --color-heat-*
// 别名都锁在这里：值漂了 server 的热力图观感就变了，别名丢了 `bg-heat-3` 只是
// 静默生成不出规则（class 照样在 DOM 里，格子/圆点变成没有底色的空格）。
const HEAT_SCALE_LIGHT = [
  "#eaeaee",
  "#cfe0f0",
  "#9cc0e0",
  "#6294c4",
  "#3b6896",
];
const HEAT_SCALE_DARK = ["#23262c", "#21384f", "#315b83", "#4a80b3", "#6fa4d4"];

function blockOf(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  expect(start).toBeGreaterThanOrEqual(0);
  return css.slice(start, css.indexOf("\n}", start));
}

describe("design tokens", () => {
  it("共享包暴露对话流排版 token", () => {
    const css = fs.readFileSync(TOKENS_CSS, "utf8");
    const missing = REQUIRED_TOKENS.filter(
      ([name, value]) => !new RegExp(`${name}:\\s*${value}\\s*;`).test(css),
    ).map(([name]) => name);
    expect(missing).toEqual([]);
  });

  it("共享包持有热力色阶的深浅两套值", () => {
    const css = fs.readFileSync(TOKENS_CSS, "utf8");
    const read = (selector: string) => {
      const block = blockOf(css, selector);
      return HEAT_SCALE_LIGHT.map(
        (_, index) =>
          new RegExp(`--heat-${index}:\\s*(#[0-9a-f]{6})\\s*;`).exec(
            block,
          )?.[1] ?? "",
      );
    };

    expect(read(":root")).toEqual(HEAT_SCALE_LIGHT);
    expect(read(".dark")).toEqual(HEAT_SCALE_DARK);
  });

  it("共享包把热力色阶接进 @theme inline 的 --color-* 命名空间", () => {
    const block = blockOf(fs.readFileSync(TOKENS_CSS, "utf8"), "@theme inline");
    const missing = HEAT_SCALE_LIGHT.map(
      (_, index) => `--color-heat-${index}`,
    ).filter(
      (name, index) =>
        !new RegExp(`${name}:\\s*var\\(--heat-${index}\\)\\s*;`).test(block),
    );
    expect(missing).toEqual([]);
  });
});

// 守卫:源码里的 `status-*` 颜色类必须对应 tokens.css 的 `--color-status-*`。
// Tailwind v4 不会为未定义的颜色生成 utility 也不报错——类名照样留在 DOM 里,
// 只是没有任何样式(重试卡曾因 bg-status-warning 无底色而退化成一段灰字)。
const STATUS_CLASS_RE =
  /(?<![\w-])(?:bg|text|border|ring|fill|stroke|from|via|to|outline|decoration|divide|accent|caret|shadow)-(status-[a-z]+(?:-[a-z]+)*)(?![\w-])/g;

function definedStatusColors(css: string): Set<string> {
  return new Set(
    [...css.matchAll(/--color-(status-[a-z]+(?:-[a-z]+)*)\s*:/g)].map(
      (m) => m[1],
    ),
  );
}

function undefinedStatusClasses(source: string, defined: Set<string>) {
  return [...source.matchAll(STATUS_CLASS_RE)]
    .map((m) => m[1])
    .filter((name) => !defined.has(name));
}

function sourceFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      return ["node_modules", "dist", "__tests__", "wailsjs"].includes(
        entry.name,
      )
        ? []
        : sourceFiles(full);
    }
    return /\.(ts|tsx)$/.test(entry.name) && !/\.test\./.test(entry.name)
      ? [full]
      : [];
  });
}

describe("status-* 颜色类守卫", () => {
  const defined = definedStatusColors(fs.readFileSync(TOKENS_CSS, "utf8"));

  it("tokens.css 定义了 running / waiting / idle / error", () => {
    expect(defined.has("status-waiting")).toBe(true);
    expect(defined.has("status-error")).toBe(true);
    expect(defined.has("status-warning")).toBe(false);
  });

  it("未定义的 status-* 类会被抓到(含透明度与变体前缀),已定义的不误报", () => {
    const fixture =
      'className="bg-status-warning/10 dark:text-status-warning bg-status-waiting/10 text-status-running-text"';
    expect(undefinedStatusClasses(fixture, defined)).toEqual([
      "status-warning",
      "status-warning",
    ]);
  });

  // 既有违例:provider-sync.tsx 的 bg-status-error-bg 同样没有对应 token(不在本任务
  // 的改动范围内)。收敛后从这里删掉;新增违例不得往这里加。
  const KNOWN_OFFENDERS = new Set([
    "packages/agentre-ui/src/engine/backend-editor/provider-sync.tsx: status-error-bg",
  ]);

  it("源码里没有引用未定义的 status-* 颜色类", () => {
    const roots = [
      path.resolve(__dirname, ".."),
      path.resolve(__dirname, "../../packages/agentre-ui/src"),
    ];
    const offenders = roots
      .flatMap(sourceFiles)
      .flatMap((file) =>
        undefinedStatusClasses(fs.readFileSync(file, "utf8"), defined).map(
          (name) => `${path.relative(process.cwd(), file)}: ${name}`,
        ),
      )
      .filter((entry) => !KNOWN_OFFENDERS.has(entry));
    expect(offenders).toEqual([]);
  });
});
