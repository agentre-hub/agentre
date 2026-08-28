import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProjectGlyphPicker } from "./project-glyph-picker";

/**
 * 图标与颜色合成的是**同一枚记号**（侧栏里那个 `ProjectGlyph`），所以挑的时候得在
 * 一起，且当场看得见结果。
 *
 * 合之前它们是表单里隔着两格的两个字段，其中「图标」在桌面端还是一个要手打 key 的
 * 输入框（placeholder 就是「folder / briefcase / 自定义 emoji」），而同一个仓库里
 * 「新建项目」用的是现成的 `IconPicker`。同一个概念、两个弹窗、两套 UX。
 *
 * 颜色与图标网格**都归包**：那张 key → 图标的词表本来就住在包里
 * （`org/icon-registry`，Agent 头像 / 部门 / 项目字形同一份），项目字形又没有「上传
 * 图片」那一档（那是 Agent 头像才有的，也正是它的选择器留在宿主的原因），所以没有
 * 理由让两个宿主各画一次网格。
 */

function open(
  over: Partial<React.ComponentProps<typeof ProjectGlyphPicker>> = {},
) {
  const onPickIcon = vi.fn();
  const onPickColor = vi.fn();
  const view = render(
    <ProjectGlyphPicker
      name="Atlas"
      color="agent-3"
      onPickIcon={onPickIcon}
      onPickColor={onPickColor}
      {...over}
    />,
  );
  return { ...view, onPickIcon, onPickColor };
}

describe("字形预览", () => {
  it("宿主给不出图标时退回项目名首字 —— 猜一个图标比一个字母更糟", () => {
    open();
    expect(screen.getByTestId("project-glyph-preview")).toHaveTextContent("A");
  });

  it("有 icon key 时画词表里那一枚，不再退回首字", () => {
    open({ icon: "rocket" });
    const preview = screen.getByTestId("project-glyph-preview");
    expect(preview.querySelector("svg")).not.toBeNull();
    expect(preview).not.toHaveTextContent("A");
  });
});

describe("弹层", () => {
  it("颜色行归包：挑一个就交出 token", async () => {
    const user = userEvent.setup();
    const { onPickColor } = open();
    await user.click(screen.getByTestId("project-glyph-trigger"));
    await user.click(await screen.findByTestId("project-glyph-color-agent-7"));
    expect(onPickColor).toHaveBeenCalledWith("agent-7");
  });

  it("当前颜色带得出选中记号 —— 十六颗一样的圆点里认不出选的是哪一颗", async () => {
    const user = userEvent.setup();
    open({ color: "agent-3" });
    await user.click(screen.getByTestId("project-glyph-trigger"));
    expect(
      await screen.findByTestId("project-glyph-color-agent-3"),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("project-glyph-color-agent-4")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("图标网格画在颜色下面，挑完交出 key 并把弹层关掉", async () => {
    const user = userEvent.setup();
    const { onPickIcon } = open();
    await user.click(screen.getByTestId("project-glyph-trigger"));
    await user.click(await screen.findByTestId("project-glyph-icon-rocket"));
    expect(onPickIcon).toHaveBeenCalledWith("rocket");
    await waitFor(() =>
      expect(
        screen.queryByTestId("project-glyph-icons"),
      ).not.toBeInTheDocument(),
    );
  });

  it("当前那一枚在网格里标得出来", async () => {
    const user = userEvent.setup();
    open({ icon: "rocket" });
    await user.click(screen.getByTestId("project-glyph-trigger"));
    expect(
      await screen.findByTestId("project-glyph-icon-rocket"),
    ).toHaveAttribute("aria-pressed", "true");
  });

  /** 三十枚图标一屏摆不下，所以要搜；搜不到时说的是「没有匹配」而不是留白。 */
  it("搜得动，搜不到时不留白", async () => {
    const user = userEvent.setup();
    open();
    await user.click(screen.getByTestId("project-glyph-trigger"));
    const search = await screen.findByTestId("project-glyph-icon-search");
    await user.type(search, "rock");
    expect(screen.getByTestId("project-glyph-icon-rocket")).toBeInTheDocument();
    expect(screen.queryByTestId("project-glyph-icon-hammer")).toBeNull();

    await user.clear(search);
    await user.type(search, "zzzz");
    expect(
      await screen.findByTestId("project-glyph-icon-none"),
    ).toBeInTheDocument();
  });
});
