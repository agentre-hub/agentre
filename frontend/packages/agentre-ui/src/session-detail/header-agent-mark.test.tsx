import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { presetForKey } from "../presets/preset-registry";

import { SessionHeaderAgentMark } from "./header-agent-mark";

describe("SessionHeaderAgentMark", () => {
  it("Given 一个 Agent, When 渲染, Then 是一枚 14px 小头像，可及名是 Agent 名", () => {
    render(<SessionHeaderAgentMark name="Eng" color="agent-1" status="idle" />);

    const avatar = screen.getByRole("img", { name: "Eng" });
    expect(avatar).toHaveClass("size-3.5");
    expect(avatar).toHaveTextContent("E");
  });

  it.each(["running", "waiting", "error"] as const)(
    "Given status=%s, When 渲染, Then 状态点挂在小头像右下角，描一圈顶带色把它和头像分开",
    (status) => {
      render(
        <SessionHeaderAgentMark
          name="Eng"
          color="agent-1"
          status={status}
          badgeTestId="badge"
        />,
      );

      const badge = screen.getByTestId("badge");
      const avatar = screen.getByRole("img", { name: "Eng" });
      expect(badge.parentElement).toBe(avatar.parentElement);
      expect(badge).toHaveClass(
        "absolute",
        "-right-[3px]",
        "-bottom-[3px]",
        "ring-sheet-header",
        `bg-status-${status}`,
      );
    },
  );

  it.each([["idle"], [null]] as const)(
    "Given status=%s, When 渲染, Then 角上不挂点（空闲维持「没有记号」）",
    (status) => {
      render(
        <SessionHeaderAgentMark
          name="Eng"
          color="agent-1"
          status={status}
          badgeTestId="badge"
        />,
      );

      expect(screen.queryByTestId("badge")).toBeNull();
    },
  );

  it("Given 宿主解好的图标, When 渲染, Then 画图标而不是首字母", () => {
    render(
      <SessionHeaderAgentMark
        name="Eng"
        color="agent-1"
        icon={<svg data-testid="icon" />}
        status="idle"
      />,
    );

    expect(screen.getByTestId("icon")).toBeInTheDocument();
  });

  // 规格 2026-11-02 决策 17：这枚记号此前只画首字母，现在按完整身份显示。
  it("Given 上传图片, When 渲染, Then 画的是图片", () => {
    render(
      <SessionHeaderAgentMark
        name="Eng"
        color="agent-1"
        avatarDataUrl="data:image/png;base64,AAAA"
        status="idle"
      />,
    );

    const mark = screen.getByRole("img", { name: "Eng" });
    expect(mark.querySelector("img")?.getAttribute("src")).toBe(
      "data:image/png;base64,AAAA",
    );
    expect(mark).toHaveClass("size-3.5");
  });

  it("Given 预设 key, When 渲染, Then 画注册表里的预设图", () => {
    render(
      <SessionHeaderAgentMark
        name="Eng"
        color="agent-1"
        iconKey="preset:agent-assistant"
        status="idle"
      />,
    );

    const mark = screen.getByRole("img", { name: "Eng" });
    expect(mark.querySelector("img")?.getAttribute("src")).toBe(
      presetForKey("preset:agent-assistant")?.src,
    );
  });

  it("Given 图标 key, When 渲染, Then 画词表里的图标而不是首字母", () => {
    render(
      <SessionHeaderAgentMark
        name="Eng"
        color="agent-1"
        iconKey="hammer"
        status="idle"
      />,
    );

    const mark = screen.getByRole("img", { name: "Eng" });
    expect(mark.querySelector("svg")).not.toBeNull();
    expect(mark).not.toHaveTextContent("E");
  });
});
