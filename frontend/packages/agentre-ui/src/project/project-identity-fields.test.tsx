import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ProjectIdentityFields } from "./project-identity-fields";

/**
 * 身份区：字形 + 名字 + 简介一块，两个弹窗共用那一份。
 *
 * 合之前它们是「设置」与「新建」各写一遍的一组标签字段（名字 / 简介 / 图标 / 颜色），
 * 于是分叉了：新建用现成的 `IconPicker`，设置要人手打 icon key。抽成一份就是为了
 * 不让它分叉第二次。
 *
 * 它是**受控**的：设置那一侧要在 blur 时提交，新建那一侧只攒草稿，两种语义都由调用
 * 方决定，这里不替它们选。
 */

function open(
  over: Partial<React.ComponentProps<typeof ProjectIdentityFields>> = {},
) {
  const props = {
    name: "Atlas",
    description: "地图那条线",
    onNameChange: vi.fn(),
    onDescriptionChange: vi.fn(),
    onPickIcon: vi.fn(),
    onPickColor: vi.fn(),
    testIdPrefix: "project-settings",
    ...over,
  };
  return { ...render(<ProjectIdentityFields {...props} />), props };
}

describe("受控的两格", () => {
  it("敲字交给宿主，blur 各自回调一次", () => {
    const onNameBlur = vi.fn();
    const onDescriptionBlur = vi.fn();
    const { props } = open({ onNameBlur, onDescriptionBlur });

    const name = screen.getByTestId("project-settings-name");
    fireEvent.change(name, { target: { value: "Atlas 2" } });
    fireEvent.blur(name);
    expect(props.onNameChange).toHaveBeenCalledWith("Atlas 2");
    expect(onNameBlur).toHaveBeenCalledTimes(1);

    const desc = screen.getByTestId("project-settings-description");
    fireEvent.blur(desc);
    expect(onDescriptionBlur).toHaveBeenCalledTimes(1);
  });

  it("testId 前缀跟着调用方走 —— 两个弹窗在同一棵树上要分得开", () => {
    open({ testIdPrefix: "project-create" });
    expect(screen.getByTestId("project-create-name")).toBeInTheDocument();
    expect(
      screen.queryByTestId("project-settings-name"),
    ).not.toBeInTheDocument();
  });
});

describe("字段级失败", () => {
  /**
   * 失败要说在出事的地方。此前所有写失败都落到脚部那一行——它在滚动正文的下面，
   * 而点了那一格的人视线就在那一格上。
   */
  it("名字写失败时那一句紧贴名字输入框，且报给读屏", () => {
    open({ nameError: "已经有一个叫这个名字的项目了。" });
    const err = screen.getByTestId("project-settings-name-error");
    expect(err).toHaveTextContent("已经有一个叫这个名字的项目了。");
    expect(err).toHaveAttribute("role", "alert");
    expect(screen.getByTestId("project-settings-name")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("没有失败就不留一个空位", () => {
    open();
    expect(
      screen.queryByTestId("project-settings-name-error"),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("project-settings-name")).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });
});

describe("字形", () => {
  it("图标与颜色经字形那一枚透传出去", async () => {
    const { props } = open();
    fireEvent.click(screen.getByTestId("project-glyph-trigger"));
    fireEvent.click(await screen.findByTestId("project-glyph-color-agent-5"));
    expect(props.onPickColor).toHaveBeenCalledWith("agent-5");
    fireEvent.click(screen.getByTestId("project-glyph-icon-rocket"));
    expect(props.onPickIcon).toHaveBeenCalledWith("rocket");
  });
});
