import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Textarea } from "./textarea";

/**
 * 字号在移动端不只是排版：iOS 会在聚焦一个字号小于 16px 的输入控件时自动放大
 * 整个视口，而且不缩回去 —— 手机上点一下多行输入框，整页跳大一次。这条行为没有
 * 开关可以关（唯一能压的 `user-scalable=no` 会把用户自己要的双指缩放一起禁掉）。
 *
 * 同包的 `<Input>` 早就用 `text-base md:text-sm` 规避了，`Textarea` 当时漏了。
 * 这里把口径钉住：移动端 16px、md 及以上 14px。
 */
describe("Textarea 的字号", () => {
  it("移动端 16px、md 及以上 14px", () => {
    render(<Textarea aria-label="说明" />);

    const className = screen.getByRole("textbox", { name: "说明" }).className;

    expect(className).toContain("text-base");
    expect(className).toContain("md:text-sm");
  });

  it("不留裸 text-sm（与移动端的 16px 同属字号工具类，会打架）", () => {
    render(<Textarea aria-label="说明" />);

    const className = screen
      .getByRole("textbox", { name: "说明" })
      .className.split(/\s+/);

    expect(className).not.toContain("text-sm");
  });
});
