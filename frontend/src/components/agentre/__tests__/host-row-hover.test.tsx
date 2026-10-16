// 桌面宿主自己的两处列表行：悬停必须是一块看得出的面。
//
// 与共享包 `packages/agentre-ui/src/chrome-row-hover.test.tsx` 同一个做法：不断言类名，
// 从渲染出来的 `hover:bg-*` 反查它落在脚下那块面上合成出的实色，两个主题各算一遍。
// 取色与对比度的算法只有一份，直接用包内的测试件（宿主 tsconfig 本就 include 包源码）。
//
// - 会话索引里的 Agent 组头坐在外框（chrome）上：原来悬停 `sidebar-active-bg`，浅色与
//   选中行的 raised 同一个字节（1.00），鼠标停在组头上就和选中行一样。
// - 「查看全部 N」弹层的会话行坐在 popover 上：`sidebar-active-bg` 两个主题都与
//   popover 同色（1.00），悬停根本没有底色反馈。
import { render, screen } from "@testing-library/react";
import { Popover, PopoverAnchor } from "@agentre-hub/agentre-ui";
import { describe, expect, it, vi } from "vitest";

import {
  contrast,
  fillOn,
  type TokenScope,
} from "../../../../packages/agentre-ui/src/styles/__testing__/tokens";
import { AgentGroup } from "../agent-list";
import { SessionsPopover } from "../sessions-popover";

/** 两块不画线的相邻面「静止时认得出是两块」的线，与包内 tokens.test.ts 的 SURFACE_MIN 同值。 */
const SURFACE_MIN = 1.08;

const THEMES: Array<[string, TokenScope]> = [
  ["亮色", ":root"],
  ["暗色", ".dark"],
];

/** 元素上唯一那条 `hover:bg-<token>`（不带不透明度后缀）的 token 名。 */
function hoverFillOf(el: HTMLElement): string {
  const fills = Array.from(el.classList)
    .map((c) => /^hover:bg-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
    .filter((name): name is string => name !== undefined);
  expect(fills, `${el.className} 里应当恰好有一条 hover 底色`).toHaveLength(1);
  return fills[0];
}

function colorOn(name: string, scope: TokenScope, surface: string): string {
  const color = fillOn(name, scope, surface);
  expect(color, `bg-${name} 在 tokens.css 里解析不出颜色`).toBeDefined();
  return color!;
}

describe("会话索引的 Agent 组头（外框上）：悬停与选中是两块面", () => {
  it.each(THEMES)("%s下组头的悬停底色", (_label, scope) => {
    render(<AgentGroup name="Eva" onHeaderClick={vi.fn()} />);
    const header = screen.getByRole("button", {
      name: "Open Eva recent session",
    });

    const fill = hoverFillOf(header);
    const hover = colorOn(fill, scope, "chrome");
    const chrome = colorOn("chrome", scope, "chrome");
    const raised = colorOn("raised", scope, "chrome");

    const onChrome = contrast(hover, chrome);
    expect(
      onChrome,
      `悬停 bg-${fill}（${hover}）对外框 ${chrome} 只有 ${onChrome.toFixed(3)}`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      contrast(hover, raised),
      `悬停 bg-${fill}（${hover}）对选中面 raised ${raised}——悬停读成选中`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      contrast(raised, chrome),
      `选中面 raised 离外框应当比悬停 bg-${fill} 更远`,
    ).toBeGreaterThan(onChrome);
  });
});

describe("「查看全部 N」弹层的会话行（popover 上）：悬停有底色反馈", () => {
  it.each(THEMES)("%s下会话行的悬停底色", async (_label, scope) => {
    render(
      <Popover open>
        <PopoverAnchor />
        <SessionsPopover
          header={{ name: "Eng" }}
          loader={() =>
            Promise.resolve({
              sessions: [
                { id: 1, title: "session 1", status: "idle", lastMessageAt: 0 },
              ],
              total: 1,
              hasMore: false,
            })
          }
          onClose={() => {}}
          onSelectSession={() => {}}
        />
      </Popover>,
    );
    const row = (await screen.findByText("session 1")).closest("button");
    expect(row).not.toBeNull();

    const fill = hoverFillOf(row!);
    const hover = colorOn(fill, scope, "popover");
    const popover = colorOn("popover", scope, "popover");
    expect(
      contrast(hover, popover),
      `悬停 bg-${fill}（${hover}）对弹层 popover ${popover}——悬停没有反馈`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
  });
});
