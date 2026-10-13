import { describe, expect, it } from "vitest";

import { contrast, readTokens } from "../styles/__testing__/tokens";

import { statusConfig } from "./agent-status";
import type { AgentStatus } from "./agent-status";

/**
 * 状态胶囊上的字读得出。
 *
 * `statusConfig` 把每个状态投影成一对类名（底 + 字），而对比度是这一对的事，
 * 不是其中任何一个 token 的事：`--status-error` 自己没毛病，压在
 * `--destructive-soft` 上当文字才只有 4.41。`tokens.test.ts` 管的是 token 之间
 * 该有的关系，这条管的是「这张表实际选了哪两个 token」。
 */
const light = readTokens(":root");
// 深色没有覆写的 token 沿用 :root —— 按级联后的有效值算。
const dark = readTokens(".dark");

/** 从一串类名里取出 `bg-x` / `text-x` 指向的 token 名。 */
function tokenOf(className: string, utility: "bg" | "text"): string {
  const hit = className.split(/\s+/).find((c) => c.startsWith(`${utility}-`));
  if (!hit) throw new Error(`「${className}」里没有 ${utility}- 类`);
  return hit.slice(utility.length + 1);
}

const STATUSES = Object.keys(statusConfig) as AgentStatus[];

describe("状态胶囊上的字对它自己的底达标", () => {
  it.each(
    (
      [
        ["亮色", light],
        ["暗色", dark],
      ] as const
    ).flatMap(([label, tokens]) =>
      STATUSES.map((status) => [label, status, tokens] as const),
    ),
  )("%s下 %s 胶囊", (_label, status, tokens) => {
    const { pillClassName } = statusConfig[status];
    const bg = tokens[tokenOf(pillClassName, "bg")];
    const fg = tokens[tokenOf(pillClassName, "text")];

    expect(bg, `${pillClassName} 的底不是一个已声明的 token`).toBeDefined();
    expect(fg, `${pillClassName} 的字不是一个已声明的 token`).toBeDefined();
    expect(
      contrast(fg, bg),
      `${status}：${fg} 落在 ${bg} 上`,
    ).toBeGreaterThanOrEqual(4.5);
  });
});

describe("状态当文字写在卡片上时达标", () => {
  it.each(
    (
      [
        ["亮色", light],
        ["暗色", dark],
      ] as const
    ).flatMap(([label, tokens]) =>
      STATUSES.map((status) => [label, status, tokens] as const),
    ),
  )("%s下 %s 文字", (_label, status, tokens) => {
    const fg = tokens[tokenOf(statusConfig[status].textClassName, "text")];

    // --secondary 是会话索引分组头与标签条的底，状态文字最暗的落点。
    for (const surface of ["card", "secondary"]) {
      expect(
        contrast(fg, tokens[surface]),
        `${status}：${fg} 落在 --${surface} ${tokens[surface]} 上`,
      ).toBeGreaterThanOrEqual(4.5);
    }
  });
});
