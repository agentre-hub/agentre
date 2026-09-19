import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AIChatInput } from "../index";

/**
 * iOS 会在聚焦一个字号小于 16px 的可编辑区时自动放大整个视口 —— 用户在手机上点
 * 一下输入框，页面自己跳大一次，得手动缩回去才看得全。composer 的正文此前是
 * `text-sm`（14px），正好落在那条线以下，而这条自动放大**没有任何开关可以关掉**
 * （唯一的办法是 `user-scalable=no`，那会把用户自己要的双指缩放一起禁掉，不能走）。
 *
 * 修法照共享包 `<Input>` 已有的口径：移动端 16px、md 及以上回到 14px
 * （`text-base md:text-sm`）。断点用 md(768px)，与两端宿主判定移动形态的那条
 * 媒体查询同一条，避免出现「布局已经是移动形态、字号还按桌面算」的夹缝。
 *
 * 为什么守这条：字号是**响应式契约**，不是一次排版微调。改回裸 `text-sm` 不会
 * 报错、不会红任何既有测试，只有真机点一下输入框才看得出来（jsdom 不跑 CSS）。
 */
function editableOf(container: HTMLElement): HTMLElement {
  const el = container.querySelector<HTMLElement>(".ProseMirror");

  if (!el) {
    throw new Error("editor contenteditable (.ProseMirror) not found");
  }

  return el;
}

describe("AIChatInput 可编辑区的字号", () => {
  it("移动端是 16px、md 以上回到 14px", () => {
    const { container } = render(<AIChatInput onSubmit={() => {}} />);
    const className = editableOf(container).className;

    expect(className).toContain("text-base");
    expect(className).toContain("md:text-sm");
  });

  it("不在可编辑区上留裸的 text-sm（它会与移动端的 16px 打架）", () => {
    const { container } = render(<AIChatInput onSubmit={() => {}} />);
    const className = editableOf(container).className.split(/\s+/);

    // 裸 `text-sm` 与 `text-base` 同属字号工具类，谁生效取决于产出顺序 ——
    // 这种不确定正是 iOS 上时灵时不灵那类 bug 的来源，所以直接禁掉并存。
    expect(className).not.toContain("text-sm");
  });
});
