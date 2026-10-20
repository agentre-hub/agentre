import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
});
