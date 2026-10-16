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
// - 对话标签溢出菜单（也在 popover 上）的当前标签原来也是 `sidebar-active-bg`：同样
//   1.00，菜单打开时认不出哪个是当前标签。与共享包 popover-menu-current.test.tsx 同一条判据。
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Popover, PopoverAnchor } from "@agentre-hub/agentre-ui";
import { describe, expect, it, vi } from "vitest";

import {
  contrast,
  fillOn,
  type TokenScope,
} from "../../../../packages/agentre-ui/src/styles/__testing__/tokens";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { AgentGroup } from "../agent-list";
import { TabOverflowMenu } from "../chat-tabs/tab-overflow-menu";
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

/** 元素上唯一那条 `<variant>bg-<token>` 的 token 名；variant 为空串时取静止底色。 */
function fillOf(el: HTMLElement, variant: "" | "hover:" | "focus:"): string {
  const re = new RegExp(`^${variant}bg-([a-z]+(?:-[a-z]+)*)$`);
  const fills = Array.from(el.classList)
    .map((c) => re.exec(c)?.[1])
    .filter((name): name is string => name !== undefined);
  expect(
    fills,
    `${el.className} 里应当恰好有一条 ${variant || "静止"}底色`,
  ).toHaveLength(1);
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

describe("对话标签溢出菜单（popover 上）的当前标签：认得出、且不读成悬停", () => {
  it.each(THEMES)("%s下当前标签那一行", async (_label, scope) => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const { tabs } = useChatTabsStore.getState();
    useChatTabsStore.getState().setActive(tabs[1].id);

    render(<TabOverflowMenu />);
    await userEvent
      .setup({ pointerEventsCheck: 0 })
      .click(screen.getByLabelText("Open Tab menu"));
    const items = await screen.findAllByRole("menuitem");
    const current = items.find((el) => el.dataset.active === "true");
    const other = items.find((el) => el.dataset.active !== "true");
    expect(current).toBeDefined();
    expect(other).toBeDefined();

    const popover = colorOn("popover", scope, "popover");
    const markName = fillOf(current!, "");
    const mark = colorOn(markName, scope, "popover");
    expect(
      contrast(mark, popover),
      `当前标签 bg-${markName}（${mark}）对弹层 popover ${popover}——当前项不可见`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    for (const variant of ["hover:", "focus:"] as const) {
      const name = fillOf(current!, variant);
      const color = colorOn(name, scope, "popover");
      expect(
        contrast(mark, color),
        `当前标签 bg-${markName}（${mark}）对 ${variant}bg-${name}（${color}）——当前读成悬停`,
      ).toBeGreaterThanOrEqual(SURFACE_MIN);
    }

    // 不靠颜色的线索：读屏认 aria-current，视觉上认字重。
    expect(current).toHaveAttribute("aria-current", "true");
    expect(other).not.toHaveAttribute("aria-current");
    expect(current).toHaveClass("font-medium");
    expect(other).not.toHaveClass("font-medium");
  });
});
