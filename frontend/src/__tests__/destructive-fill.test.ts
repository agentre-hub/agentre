import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * 实心红底上的文字,暗色下必须先把底压暗。
 *
 * 根因是 `--destructive` **跨主题翻转了明暗**,而它的前景色没跟着翻:
 *
 * |      | 填充                | 前景              |      |
 * | ---- | ------------------- | ----------------- | ---- |
 * | 亮色 | `#dc2626` 深红      | `#ffffff` 白      | 4.83 |
 * | 暗色 | `#f87171` **浅**红  | `#fafafa` 近白    | 2.65 |
 *
 * 暗色把红调浅是对的(要在深色表面上看得见),但白字压浅红就糊了。
 *
 * 不能简单地把暗色前景改深,因为**同一个 token 服务两种底色**:实心 `#f87171` 上
 * 深字 6.62 没问题,而 `dark:bg-destructive/60` 混出的 `#a05153` 上深字只有 3.31。
 * 又是「一个 hex 两种角色」。
 *
 * 所以约定反过来:**保持前景是白的,让暗色下的实心红底一律压到 /60**
 * (混出 `#a05153`,白字 5.30)。shadcn 的 Button/Badge 本来就是这么写的,
 * 这条守卫只是把那个隐式约定变成全仓强制。
 *
 * 判据刻意收窄成「实心红底 **且** 压着文字」:纯色圆点、状态指示条同样是
 * `bg-destructive` 但没有文字坐在上面,不在管辖内,不该被误伤。
 */
const ROOTS = [
  path.resolve(__dirname, ".."),
  path.resolve(__dirname, "../../packages/agentre-ui/src"),
];

/** 前景色类名:出现其中之一,就说明这块红底上坐着文字或图标。 */
const FOREGROUND_ON_FILL = ["text-destructive-foreground", "text-white"];

/** 实心 `bg-destructive`(允许 hover:/focus: 等变体前缀,但不带 `/N` 不透明度)。 */
const SOLID_FILL = /(?:^|["'\s])(?:[a-z-]+:)*bg-destructive(?![\w/-])/;

/** 暗色下把同一个填充压暗的写法,`dark:` 变体 + 任意 alpha。 */
const DARK_DIMMED = /dark:(?:[a-z-]+:)*bg-destructive\/\d{1,3}/;

function sourceFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      return entry.name === "__tests__" || entry.name === "node_modules"
        ? []
        : sourceFiles(full);
    }
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [full]
      : [];
  });
}

/**
 * 按「类名串」切分:一个文件里可以既有实心红胶囊,又有别处的软底红,
 * 整文件匹配会把两者混为一谈。以引号包裹的字符串字面量为单位判断,
 * 才对得上一个元素实际拿到的那串 class。
 */
function classStrings(source: string): string[] {
  return source.match(/["'`][^"'`\n]*\b(?:bg|text)-[^"'`\n]*["'`]/g) ?? [];
}

describe("暗色下实心 destructive 底上的文字", () => {
  it("每一处实心红底 + 文字,都带着 dark: 的压暗写法", () => {
    const offenders: string[] = [];

    for (const root of ROOTS) {
      for (const file of sourceFiles(root)) {
        const source = fs.readFileSync(file, "utf8");
        for (const chunk of classStrings(source)) {
          const carriesText = FOREGROUND_ON_FILL.some((c) => chunk.includes(c));
          if (!carriesText || !SOLID_FILL.test(chunk)) continue;
          if (DARK_DIMMED.test(chunk)) continue;
          offenders.push(
            `${path.relative(ROOTS[0], file)}: ${chunk.slice(0, 96)}`,
          );
        }
      }
    }

    expect(
      offenders,
      "这些地方在暗色下是白字压浅红 #f87171,只有 2.65。" +
        "补一个 dark:bg-destructive/60(白字 5.30),不要去改全局 --destructive-foreground —— " +
        "那会把 Button/Badge 已经压暗过的底(#a05153)上的白字一起拉低到 3.31。",
    ).toEqual([]);
  });
});
