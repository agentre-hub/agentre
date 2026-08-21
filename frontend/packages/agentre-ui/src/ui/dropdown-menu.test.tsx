import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./dropdown-menu";

/**
 * 合并后的唯一一份 DropdownMenu。
 *
 * 三份副本里 agentre-server 那份是**精简版**：没有子菜单 / 勾选项 / 单选项，也
 * 没有碰撞避让与最大高度——菜单一长就顶出视口。合并取结构上的超集（桌面端与包内
 * 那份），阴影取 token 化的 shadow-overlay。
 *
 * 逐项的交互（cursor、hover 高亮）由桌面端既有的
 * src/components/ui/__tests__/dropdown-menu.test.tsx 盖着，那份在本仓改成转发之后
 * 验的就是这里这一份，所以这里不重复。
 */
function openMenu() {
  render(
    <DropdownMenu open>
      <DropdownMenuTrigger>更多</DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem>重命名</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>,
  );

  const content = document.querySelector('[data-slot="dropdown-menu-content"]');
  if (!content) {
    throw new Error("菜单没有渲染出来");
  }
  return content;
}

describe("DropdownMenu", () => {
  it("被限制在可用高度内，且内部可滚 —— 长菜单不越出视口", () => {
    // agentre-server 那份两条都没有，控制台里但凡菜单长一点就被截断在屏幕外。
    const content = openMenu();

    expect(content).toHaveClass(
      "max-h-(--radix-dropdown-menu-content-available-height)",
    );
    expect(content).toHaveClass("overflow-y-auto");
  });

  it("阴影走 token，不用 Tailwind 内建的 shadow-md", () => {
    // 三份里只有 agentre-server 那份是 shadow-overlay。合并取它。
    const content = openMenu();

    expect(content).toHaveClass("shadow-overlay");
    expect(content).not.toHaveClass("shadow-md");
  });

  it("默认最小宽度是通用的那一档，宽菜单由调用点自己要", () => {
    // agentre-server 控制台那几处要 212px，但那是它那几屏的局部设计、不是通用
    // 默认；让调用点传 className 即可，不给包加一个只有一个宿主用的 size variant。
    const content = openMenu();

    expect(content).toHaveClass("min-w-[8rem]");
  });
});
