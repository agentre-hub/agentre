import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { presetForKey } from "@agentre-hub/agentre-ui";

import { AgentGroup } from "../agent-list";

// 规格 2026-11-02 决策 17：Agent 分组的组头此前只画首字母 + 颜色，现在按
// 上传图片 ▸ 预设 ▸ 图标 ▸ 首字母显示完整身份。
describe("Given Agent 有完整身份, When Agent 分组组头渲染, Then 记号按完整身份显示", () => {
  function renderHeaderAvatar(identity: {
    avatarIcon?: string;
    avatarDataUrl?: string;
  }) {
    render(<AgentGroup name="设计师" color="agent-9" {...identity} />);

    return screen.getByRole("img", { name: "设计师" });
  }

  it("上传图片：组头画的是图片", () => {
    const avatar = renderHeaderAvatar({
      avatarDataUrl: "data:image/png;base64,AAAA",
    });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      "data:image/png;base64,AAAA",
    );
  });

  it("预设 key：组头画注册表里的预设图", () => {
    const avatar = renderHeaderAvatar({ avatarIcon: "preset:agent-assistant" });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      presetForKey("preset:agent-assistant")?.src,
    );
  });

  it("图标 key：组头画词表里的图标，而不是首字母", () => {
    const avatar = renderHeaderAvatar({ avatarIcon: "hammer" });

    expect(avatar.querySelector("svg")).not.toBeNull();
    expect(avatar).not.toHaveTextContent("设");
  });

  it("没有图标：组头照旧画首字母", () => {
    const avatar = renderHeaderAvatar({});

    expect(avatar).toHaveTextContent("设");
  });
});
