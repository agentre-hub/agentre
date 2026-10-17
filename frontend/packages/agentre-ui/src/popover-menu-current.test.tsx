import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  PreviewTabStrip,
  type FilePreviewTab,
} from "./file-preview/preview-tab-strip";
import { contrast, fillOn, type TokenScope } from "./styles/__testing__/tokens";

/**
 * 弹层菜单里「当前那一项」（文件预览标签条的溢出菜单：哪个文件正开着）必须认得出。
 *
 * 原来当前项是 `sidebar-active-bg`：浅色 #ffffff、深色 #292a2e，与 popover 两个主题
 * 都是同一个字节（1.00）——菜单一打开，当前文件那一行和别的行长得一模一样，只有
 * 恰好落了焦点（`accent`）时才看得出来。
 *
 * 断言不看类名，看画出来的面，两个主题各算一遍：
 * - 当前项的底色对 popover 分得开——不然当前项不可见；
 * - 当前项的底色对悬停 / 焦点的 `accent` 分得开——不然「当前」与「鼠标停着」读成一回事；
 * - 还要有一条不靠颜色的线索（WCAG 1.4.1）：`aria-current` + 字重。
 * 门槛用 SURFACE_MIN（见 tokens.test.ts）。
 */
const SURFACE_MIN = 1.08;

const THEMES: Array<[string, TokenScope]> = [
  ["亮色", ":root"],
  ["暗色", ".dark"],
];

function tab(path: string): FilePreviewTab {
  return { path, isPreview: false, isPinned: false };
}

/** 元素上唯一那条 `<variant>:bg-<token>`（不带不透明度后缀）的 token 名。 */
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

describe("弹层菜单的当前项：认得出、且不读成悬停", () => {
  it.each(THEMES)("%s下文件预览溢出菜单的当前文件", async (_label, scope) => {
    render(
      <PreviewTabStrip
        tabs={[tab("docs/a.md"), tab("b.md")]}
        activePath="b.md"
        onActivate={vi.fn()}
        onPromote={vi.fn()}
        onPin={vi.fn()}
        onClose={vi.fn()}
        onCloseOthers={vi.fn()}
        onCloseAll={vi.fn()}
      />,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Open Tab menu" }),
    );
    const [other, current] = await screen.findAllByRole("menuitem");

    const on = (name: string) => {
      const color = fillOn(name, scope, "popover");
      expect(color, `bg-${name} 在 tokens.css 里解析不出颜色`).toBeDefined();
      return color!;
    };
    const popover = on("popover");
    const markName = fillOf(current, "");
    const mark = on(markName);
    expect(
      contrast(mark, popover),
      `当前项 bg-${markName}（${mark}）对弹层 popover ${popover}——当前项不可见`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    for (const variant of ["hover:", "focus:"] as const) {
      const name = fillOf(current, variant);
      const color = on(name);
      expect(
        contrast(mark, color),
        `当前项 bg-${markName}（${mark}）对 ${variant}bg-${name}（${color}）——当前读成悬停`,
      ).toBeGreaterThanOrEqual(SURFACE_MIN);
    }

    // 不靠颜色的线索：读屏认 aria-current，视觉上认字重。
    expect(current).toHaveAttribute("aria-current", "true");
    expect(other).not.toHaveAttribute("aria-current");
    expect(current).toHaveClass("font-medium");
    expect(other).not.toHaveClass("font-medium");
  });
});
